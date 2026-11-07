// Package qualitycheck looks inside movie and episode files, with
// ffprobe, to see what their picture really is, and corrects the quality
// Box Office has for them: names are often wrong, and files Box Office
// found on disk often have none. It only reads files, never changes them,
// and goes one file at a time so a library on a slow share stays usable.
//
// Upgrades compare against these qualities, so the check is careful not
// to set off a wave of them: only the picture's size is taken as fact, a
// recorded quality whose size matches is kept, and a file whose source
// isn't known gets the profile's quality closest to it that already meets
// the cutoff. See quality.Correct.
package qualitycheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Timings: the first pass waits a while after a start, so it doesn't add
// to the work a start does; after that new files are looked at every few
// minutes, with a pause between files.
var (
	firstWait = 5 * time.Minute
	every     = 10 * time.Minute
	pause     = 250 * time.Millisecond
)

// batch is how many files a pass reads from the database at a time.
const batch = 100

// Service runs the check in the background.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// Probe looks inside a file; ffprobe when nil.
	Probe func(ctx context.Context, path string) (quality.Media, error)

	mu      sync.Mutex
	running bool
	done    int
	lastErr string
	wake    chan struct{}
}

// Status is where the check stands.
type Status struct {
	Running bool `json:"running"`
	// Done is how many files this pass has looked at.
	Done int `json:"done"`
	// FFprobe says whether ffprobe is installed where Box Office runs.
	FFprobe bool   `json:"ffprobe"`
	Error   string `json:"error,omitempty"`
	store.CheckCounts
}

func (s *Service) wakeCh() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

func (s *Service) probe() (func(context.Context, string) (quality.Media, error), error) {
	if s.Probe != nil {
		return s.Probe, nil
	}
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, errors.New("ffprobe isn't installed where Box Office runs")
	}
	return func(ctx context.Context, file string) (quality.Media, error) { return FFprobe(ctx, path, file) }, nil
}

// State is the status, with counts from the database.
func (s *Service) State(ctx context.Context) (Status, error) {
	s.mu.Lock()
	st := Status{Running: s.running, Done: s.done, Error: s.lastErr}
	s.mu.Unlock()
	_, err := s.probe()
	st.FFprobe = err == nil
	st.CheckCounts, err = s.Store.CheckCounts(ctx)
	return st, err
}

// Wake starts a pass now, unless one is running.
func (s *Service) Wake() {
	select {
	case s.wakeCh() <- struct{}{}:
	default:
	}
}

// Run looks at new and changed files every few minutes until ctx ends.
func (s *Service) Run(ctx context.Context) {
	wake := s.wakeCh()
	t := time.NewTimer(firstWait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("quality check failed", "err", err)
		}
		t.Reset(every)
	}
}

// titlesKey notes that movie files have been looked inside again for the
// title they give themselves, which earlier looks didn't keep.
const titlesKey = "qualitycheck:titles"

// Pass looks inside every file not looked at yet, one at a time.
func (s *Service) Pass(ctx context.Context) error {
	if v, err := s.Store.Setting(ctx, titlesKey); err == nil && v == "" {
		// Once: movie copies are looked at again for their own title, so
		// it can say which cut they are. It only reads them, and a quality
		// that matches is never changed (see quality.Correct).
		if err := s.Store.CheckMoviesAgain(ctx); err != nil {
			return err
		}
		if err := s.Store.SetSetting(ctx, titlesKey, "1"); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running, s.done, s.lastErr = true, 0, ""
	s.mu.Unlock()
	err := s.pass(ctx)
	s.mu.Lock()
	s.running = false
	if err != nil {
		s.lastErr = err.Error()
	}
	s.mu.Unlock()
	return err
}

func (s *Service) pass(ctx context.Context) error {
	probe, err := s.probe()
	if err != nil {
		return err
	}
	profiles := map[int]quality.Profile{}
	profileOf := func(id int) quality.Profile {
		if p, ok := profiles[id]; ok {
			return p
		}
		p, err := s.Store.Profile(ctx, id)
		if err != nil {
			p = quality.Defaults()[0]
		}
		profiles[id] = p
		return p
	}
	var corrected, filled int
	for {
		files, err := s.Store.FilesToCheck(ctx, batch)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			break
		}
		for _, f := range files {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m, err := s.look(ctx, probe, f.Path)
			q := f.Quality
			if err != nil {
				m = quality.Media{Error: err.Error()}
				s.Log.Debug("quality check: couldn't look inside", "path", f.Path, "err", err)
			} else {
				q = quality.Correct(f.Quality, nameOf(f.Path), m, profileOf(f.ProfileID))
			}
			if err := s.Store.SaveCheck(ctx, f.File, m, q); err != nil {
				return err
			}
			if q != f.Quality {
				if f.Quality == 0 {
					filled++
				} else {
					corrected++
				}
				was, _ := quality.ByID(f.Quality)
				now, _ := quality.ByID(q)
				s.Log.Info("quality check: corrected", "path", f.Path, "was", was.Name, "now", now.Name,
					"picture", fmt.Sprintf("%dx%d", m.Width, m.Height))
			}
			s.mu.Lock()
			s.done++
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
	}
	if corrected+filled > 0 {
		s.Log.Info("quality check finished", "looked", s.doneCount(), "corrected", corrected, "given a quality", filled)
	}
	return nil
}

func (s *Service) doneCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// look checks a file is there before looking inside it.
func (s *Service) look(ctx context.Context, probe func(context.Context, string) (quality.Media, error), path string) (quality.Media, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return quality.Media{}, errors.New("the file isn't there")
		}
		return quality.Media{}, err
	}
	return probe(ctx, path)
}

// nameOf is what's said about a file's quality by its name and its
// folder's.
func nameOf(path string) string {
	return filepath.Base(path) + " " + filepath.Base(filepath.Dir(path))
}

// FFprobe looks inside a file with ffprobe, reading only as much as it
// needs to name its streams.
func FFprobe(ctx context.Context, ffprobe, path string) (quality.Media, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-i", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return quality.Media{}, fmt.Errorf("ffprobe: %s", msg)
	}
	return Parse(out)
}

// Parse reads ffprobe's JSON.
func Parse(out []byte) (quality.Media, error) {
	var r struct {
		Format struct {
			Duration string            `json:"duration"`
			BitRate  string            `json:"bit_rate"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Profile       string `json:"profile"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			PixFmt        string `json:"pix_fmt"`
			BitsPerRaw    string `json:"bits_per_raw_sample"`
			ColorTransfer string `json:"color_transfer"`
			Channels      int    `json:"channels"`
			Disposition   struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
			SideData []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return quality.Media{}, err
	}
	var m quality.Media
	m.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	m.Bitrate, _ = strconv.ParseInt(r.Format.BitRate, 10, 64)
	for k, v := range r.Format.Tags {
		if strings.EqualFold(k, "title") {
			m.Title = strings.TrimSpace(v)
		}
	}
	video, audio := false, false
	for _, st := range r.Streams {
		switch {
		case st.CodecType == "video" && st.Disposition.AttachedPic == 0 && !video && st.Width > 0:
			video = true
			m.Width, m.Height, m.VideoCodec = st.Width, st.Height, st.CodecName
			m.BitDepth, _ = strconv.Atoi(st.BitsPerRaw)
			if m.BitDepth == 0 {
				switch {
				case strings.Contains(st.PixFmt, "p10"):
					m.BitDepth = 10
				case strings.Contains(st.PixFmt, "p12"):
					m.BitDepth = 12
				case st.PixFmt != "":
					m.BitDepth = 8
				}
			}
			switch st.ColorTransfer {
			case "smpte2084":
				m.HDR = "HDR10"
			case "arib-std-b67":
				m.HDR = "HLG"
			}
			for _, sd := range st.SideData {
				switch {
				case strings.Contains(sd.Type, "DOVI"):
					m.HDR = "Dolby Vision"
				case strings.Contains(sd.Type, "2094-40") && m.HDR != "Dolby Vision":
					m.HDR = "HDR10+"
				}
			}
		case st.CodecType == "audio" && !audio:
			audio = true
			m.AudioCodec, m.Channels = audioName(st.CodecName, st.Profile), st.Channels
		}
	}
	if !video {
		return m, errors.New("there's no picture in it")
	}
	return m, nil
}

// audioName is how people know a sound format.
func audioName(codec, profile string) string {
	switch codec {
	case "truehd":
		return "TrueHD"
	case "dts":
		switch {
		case strings.Contains(profile, "MA"):
			return "DTS-HD MA"
		case strings.Contains(profile, "HD"):
			return "DTS-HD"
		case strings.Contains(profile, "X"):
			return "DTS:X"
		}
		return "DTS"
	case "eac3":
		return "E-AC-3"
	case "ac3":
		return "AC-3"
	case "aac":
		return "AAC"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "mp3":
		return "MP3"
	}
	if strings.HasPrefix(codec, "pcm") {
		return "PCM"
	}
	return strings.ToUpper(codec)
}
