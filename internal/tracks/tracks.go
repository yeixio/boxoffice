// Package tracks drops the audio and subtitle tracks nobody here wants
// from a video as it comes into the library: remuxes and web releases
// often carry a dozen of each. The picture and the tracks kept aren't
// re-encoded. It's off until turned on in Settings → Library.
//
// What stays: every track in the language the title was made in, in the
// languages chosen in the settings, and (for subtitles) in the languages
// Settings → Subtitles wants; every track that doesn't say its language;
// and all of the audio when none of it would be left. The file as it was
// goes to the library folder's recycle folder, like any copy replaced.
package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/subtitles"
)

const (
	settingConfig = "tracks"
	settingSaved  = "tracks:saved"
)

// Config is what admins set in Settings → Library → Unwanted tracks.
type Config struct {
	// On drops unwanted tracks from movies and episodes as they come in.
	On bool `json:"on"`
	// Languages (ISO 639-1) are kept, next to the one each title was
	// made in.
	Languages []string `json:"languages"`
}

// MaxLanguages is how many languages can be kept.
const MaxLanguages = 10

// Normalize tidies what was typed.
func (c *Config) Normalize() {
	langs := []string{}
	for _, l := range c.Languages {
		if code := subtitles.Code(l); code != "" && !slices.Contains(langs, code) {
			langs = append(langs, code)
		}
	}
	c.Languages = langs
}

// Check says what's wrong with the settings, in words for the admin.
func (c Config) Check() error {
	if len(c.Languages) > MaxLanguages {
		return errors.New("pick at most 10 languages")
	}
	if c.On && len(c.Languages) == 0 {
		return errors.New("pick the languages to keep, so a dub you listen to isn't dropped")
	}
	return nil
}

// LoadConfig reads the settings; off until saved.
func LoadConfig(ctx context.Context, st *store.Store) (Config, error) {
	var c Config
	v, err := st.Setting(ctx, settingConfig)
	if err != nil {
		return c, err
	}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &c); err != nil {
			return c, err
		}
	}
	c.Normalize()
	return c, nil
}

// SaveConfig stores the settings.
func SaveConfig(ctx context.Context, st *store.Store, c Config) error {
	c.Normalize()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, settingConfig, string(b))
}

// Saved is how much dropping tracks has saved so far.
type Saved struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// Stream is one track of a video.
type Stream struct {
	Index int
	// Kind is video, audio, subtitle, attachment or data.
	Kind string
	// Language is its ISO 639-1 code, or "" when it doesn't say or Box
	// Office doesn't know the language.
	Language string
}

// Plan picks the tracks to drop: audio and subtitles that say their
// language, when it's none of keep (for subtitles, none of keep and
// keepSubs). The audio is left whole when none of it would stay.
func Plan(streams []Stream, keep, keepSubs []string) []Stream {
	var drop []Stream
	audio, audioDropped := 0, 0
	for _, st := range streams {
		switch st.Kind {
		case "audio":
			audio++
			if st.Language != "" && !slices.Contains(keep, st.Language) {
				audioDropped++
				drop = append(drop, st)
			}
		case "subtitle":
			if st.Language != "" && !slices.Contains(keep, st.Language) && !slices.Contains(keepSubs, st.Language) {
				drop = append(drop, st)
			}
		}
	}
	if audio > 0 && audioDropped == audio {
		drop = slices.DeleteFunc(drop, func(st Stream) bool { return st.Kind == "audio" })
	}
	return drop
}

// Service drops unwanted tracks from files as they come in.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// Original gives the language (ISO 639-1) a title was made in; kind
	// is movie or series.
	Original func(ctx context.Context, kind string, tmdbID int64) (string, error)
	// Enter keeps a title's folder from being moved to another root
	// folder meanwhile (package relocate); nil for no such check.
	Enter func(kind string, id int64) (leave func(), ok bool)
	// FFmpeg and FFprobe are the tools' paths; found on PATH when "".
	FFmpeg, FFprobe string

	mu sync.Mutex // one file at a time: each is read and written whole

	// made is the language of the title last asked about, so a season's
	// episodes ask once.
	madeMu sync.Mutex
	made   struct {
		kind string
		id   int64
		lang string
	}
}

func (s *Service) original(ctx context.Context, kind string, tmdbID int64) (string, error) {
	if s.Original == nil {
		return "", nil
	}
	s.madeMu.Lock()
	defer s.madeMu.Unlock()
	if s.made.kind == kind && s.made.id == tmdbID {
		return s.made.lang, nil
	}
	lang, err := s.Original(ctx, kind, tmdbID)
	if err != nil {
		return "", err
	}
	s.made.kind, s.made.id, s.made.lang = kind, tmdbID, lang
	return lang, nil
}

func (s *Service) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) tool(set, name string) string {
	if set != "" {
		return set
	}
	p, _ := exec.LookPath(name)
	return p
}

// Ready says whether ffmpeg and ffprobe are there to do it with.
func (s *Service) Ready() bool {
	return s.tool(s.FFmpeg, "ffmpeg") != "" && s.tool(s.FFprobe, "ffprobe") != ""
}

// Saved reads how much has been saved so far.
func (s *Service) Saved(ctx context.Context) (Saved, error) {
	var out Saved
	v, err := s.Store.Setting(ctx, settingSaved)
	if err != nil || v == "" {
		return out, err
	}
	return out, json.Unmarshal([]byte(v), &out)
}

func (s *Service) addSaved(ctx context.Context, bytes int64) {
	have, _ := s.Saved(ctx)
	have.Files++
	have.Bytes += bytes
	b, _ := json.Marshal(have)
	if err := s.Store.SetSetting(ctx, settingSaved, string(b)); err != nil {
		s.logger().Warn("couldn't note the space saved by dropping tracks", "err", err)
	}
}

// Imported drops the unwanted tracks of the files a download brought in,
// when that's turned on. Titles on a Do not touch profile are left be.
func (s *Service) Imported(ctx context.Context, d store.Download) {
	if d.ImportedTo == "" || (d.Kind != "movie" && d.Kind != "series") {
		return
	}
	c, err := LoadConfig(ctx, s.Store)
	if err != nil || !c.On || len(c.Languages) == 0 {
		return
	}
	if !s.Ready() {
		s.logger().Warn("unwanted tracks are left in: ffmpeg or ffprobe isn't installed")
		return
	}
	files, err := s.Store.SubtitleFiles(ctx)
	if err != nil {
		return
	}
	keepSubs := []string{}
	if sc, err := subtitles.LoadConfig(ctx, s.Store); err == nil {
		keepSubs = sc.Languages
	}
	for _, f := range files {
		pack := d.Kind == "series" && f.Kind == "episode" && (slices.Contains(d.EpisodeIDs, f.OwnerID) || filepath.Dir(f.Path) == d.ImportedTo)
		if f.Path != d.ImportedTo && !pack {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		saved, err := s.Strip(ctx, f, c.Languages, keepSubs)
		switch {
		case err != nil:
			s.logger().Warn("couldn't drop a file's unwanted tracks; it's left as it came", "file", f.Path, "err", err)
		case saved > 0:
			s.logger().Info("unwanted tracks dropped", "file", f.Path, "saved", saved)
		}
	}
}

// space is how much room is wanted free beyond the file's own size
// before a copy of it is written.
const space = 1 << 30

// Strip drops a file's unwanted tracks and says how many bytes that
// saved; 0 when there was nothing to drop, or the file isn't one it
// handles (only Matroska files are, and never a Do not touch title's).
func (s *Service) Strip(ctx context.Context, f store.SubtitleFile, keep, keepSubs []string) (int64, error) {
	if f.Locked || !strings.EqualFold(filepath.Ext(f.Path), ".mkv") {
		return 0, nil
	}
	kind := "movie"
	if f.Kind == "episode" {
		kind = "series"
	}
	root, err := s.root(ctx, kind, f.TitleID, f.Path)
	if err != nil {
		return 0, err
	}
	original, err := s.original(ctx, kind, f.TMDBID)
	if err != nil {
		return 0, fmt.Errorf("couldn't learn the language it was made in: %w", err)
	}
	// TMDB says "cn" for Cantonese; tracks say Chinese for both.
	if original == "cn" {
		original = "zh"
	}
	original = subtitles.Code(original)
	if original == "" {
		return 0, nil // made in no language, or one Box Office doesn't know: nothing is guessed
	}
	keep = append(slices.Clone(keep), original)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Enter != nil {
		leave, ok := s.Enter(kind, f.TitleID)
		if !ok {
			return 0, errors.New("its folder is being moved to another root folder")
		}
		defer leave()
	}
	before, err := os.Stat(f.Path)
	if err != nil {
		return 0, err
	}
	ffmpeg, ffprobe := s.tool(s.FFmpeg, "ffmpeg"), s.tool(s.FFprobe, "ffprobe")
	streams, length, err := Probe(ctx, ffprobe, f.Path)
	if err != nil {
		return 0, err
	}
	drop := Plan(streams, keep, keepSubs)
	if len(drop) == 0 {
		return 0, nil
	}
	if free := arrapi.FreeSpace(filepath.Dir(f.Path)); free > 0 && free < before.Size()+space {
		return 0, errors.New("there isn't room on the disk to write the file again")
	}

	// Written next to the file, under a name no player takes for a video.
	tmp := f.Path + ".tracks-tmp"
	defer os.Remove(tmp)
	args := []string{"-nostdin", "-v", "error", "-y", "-i", f.Path, "-map", "0"}
	for _, st := range drop {
		args = append(args, "-map", "-0:"+strconv.Itoa(st.Index))
	}
	args = append(args, "-c", "copy", "-map_metadata", "0", "-map_chapters", "0", "-f", "matroska", tmp)
	if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("ffmpeg: %w: %s", err, lastLine(out))
	}

	// The copy must hold everything that was to stay, at full length.
	got, gotLength, err := Probe(ctx, ffprobe, tmp)
	if err != nil {
		return 0, fmt.Errorf("the file written couldn't be read back: %w", err)
	}
	for _, k := range []string{"video", "audio", "subtitle"} {
		want := count(streams, k) - count(drop, k)
		if have := count(got, k); have != want {
			return 0, fmt.Errorf("the file written has %d %s tracks where %d were to stay", have, k, want)
		}
	}
	if d := length - gotLength; length > 0 && (d > 2 || d < -2) {
		return 0, fmt.Errorf("the file written runs %.0f seconds where the original runs %.0f", gotLength, length)
	}
	after, err := os.Stat(tmp)
	if err != nil {
		return 0, err
	}
	saved := before.Size() - after.Size()
	if saved <= 0 {
		return 0, nil // no smaller: not worth replacing the file for
	}
	// The file may have been replaced (an upgrade) while this ran.
	if now, err := os.Stat(f.Path); err != nil || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return 0, errors.New("the file changed meanwhile")
	}
	if cur, err := s.Store.FileByID(ctx, f.ID); err != nil || cur.Path != f.Path {
		return 0, errors.New("the file changed meanwhile")
	}
	aside, err := arrapi.RecycleTo(root, f.Path)
	if err != nil {
		return 0, fmt.Errorf("couldn't move the original to the recycle folder: %w", err)
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		if aside != "" {
			os.Rename(aside, f.Path)
		}
		return 0, err
	}
	os.Chtimes(f.Path, time.Now(), before.ModTime())
	if err := s.Store.SetFileSize(ctx, f.ID, after.Size()); err != nil {
		s.logger().Warn("couldn't note a file's new size", "file", f.Path, "err", err)
	}
	s.addSaved(ctx, saved)
	return saved, nil
}

// root is the library folder a title's file is in.
func (s *Service) root(ctx context.Context, kind string, id int64, path string) (string, error) {
	var root string
	if kind == "movie" {
		m, err := s.Store.Movie(ctx, id)
		if err != nil {
			return "", err
		}
		root = m.RootFolder
	} else {
		t, err := s.Store.Series(ctx, id)
		if err != nil {
			return "", err
		}
		root = t.RootFolder
	}
	if rel, err := filepath.Rel(root, path); root == "" || err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("the file isn't inside its title's library folder")
	}
	return root, nil
}

func count(streams []Stream, kind string) int {
	n := 0
	for _, st := range streams {
		if st.Kind == kind {
			n++
		}
	}
	return n
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// probeTimeout is how long ffprobe gets to read a file's tracks.
const probeTimeout = 2 * time.Minute

// Probe lists a video's tracks and how long it runs, in seconds (0 when
// it doesn't say), with ffprobe.
func Probe(ctx context.Context, ffprobe, path string) ([]Stream, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries",
		"format=duration:stream=index,codec_type:stream_tags=language:stream_disposition=attached_pic", "-of", "json", "-i", path).Output()
	if err != nil {
		return nil, 0, fmt.Errorf("ffprobe: %w", err)
	}
	return ParseProbe(out)
}

// ParseProbe reads ffprobe's list of tracks.
func ParseProbe(out []byte) ([]Stream, float64, error) {
	var r struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index int               `json:"index"`
			Type  string            `json:"codec_type"`
			Tags  map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, 0, err
	}
	var streams []Stream
	for _, st := range r.Streams {
		lang := ""
		for k, v := range st.Tags {
			if strings.EqualFold(k, "language") {
				lang = subtitles.Code(v)
			}
		}
		streams = append(streams, Stream{Index: st.Index, Kind: st.Type, Language: lang})
	}
	length, _ := strconv.ParseFloat(r.Format.Duration, 64)
	return streams, length, nil
}
