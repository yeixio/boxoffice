package problems

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Media is what FFmpeg says is in a video, so the admin can tell copies
// apart: how long it runs, its picture and its sound.
type Media struct {
	Duration  float64  `json:"duration"` // seconds
	Width     int      `json:"width,omitempty"`
	Height    int      `json:"height,omitempty"`
	Video     string   `json:"video,omitempty"` // codec, such as HEVC
	HDR       string   `json:"hdr,omitempty"`   // HDR10, HLG or Dolby Vision
	Audio     []string `json:"audio,omitempty"` // such as "TrueHD 7.1 eng"
	Subtitles int      `json:"subtitles,omitempty"`
	// Title is the title the file gives itself, which often names its cut.
	Title string `json:"title,omitempty"`
}

var codecNames = map[string]string{
	"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "mpeg2video": "MPEG-2", "vc1": "VC-1", "vp9": "VP9", "mpeg4": "MPEG-4",
	"truehd": "TrueHD", "dts": "DTS", "ac3": "AC-3", "eac3": "E-AC-3", "aac": "AAC", "flac": "FLAC", "opus": "Opus", "mp3": "MP3", "pcm_s16le": "PCM", "pcm_s24le": "PCM", "pcm_bluray": "PCM",
}

func codecName(c string) string {
	if n, ok := codecNames[c]; ok {
		return n
	}
	return strings.ToUpper(c)
}

func channels(n int, layout string) string {
	switch {
	case n == 8 || strings.HasPrefix(layout, "7.1"):
		return "7.1"
	case n == 6 || strings.HasPrefix(layout, "5.1"):
		return "5.1"
	case n == 2:
		return "2.0"
	case n == 1:
		return "1.0"
	case n > 0:
		return strconv.Itoa(n) + "ch"
	}
	return ""
}

// Inspect reads what's in a video. It only reads the file's headers.
func (t Tools) Inspect(ctx context.Context, path string) (*Media, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-i", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var r struct {
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			CodecType     string            `json:"codec_type"`
			CodecName     string            `json:"codec_name"`
			Profile       string            `json:"profile"`
			Width         int               `json:"width"`
			Height        int               `json:"height"`
			Channels      int               `json:"channels"`
			Layout        string            `json:"channel_layout"`
			ColorTransfer string            `json:"color_transfer"`
			Duration      string            `json:"duration"`
			Tags          map[string]string `json:"tags"`
			Disposition   map[string]int    `json:"disposition"`
			SideData      []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	m := &Media{}
	m.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	for k, v := range r.Format.Tags {
		if strings.EqualFold(k, "title") {
			m.Title = strings.TrimSpace(v)
		}
	}
	for _, s := range r.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 || m.Video != "" {
				continue
			}
			m.Video, m.Width, m.Height = codecName(s.CodecName), s.Width, s.Height
			switch s.ColorTransfer {
			case "smpte2084":
				m.HDR = "HDR10"
			case "arib-std-b67":
				m.HDR = "HLG"
			}
			for _, sd := range s.SideData {
				if strings.Contains(sd.Type, "DOVI") || strings.Contains(sd.Type, "Dolby Vision") {
					m.HDR = "Dolby Vision"
				}
			}
			if m.Duration == 0 {
				m.Duration, _ = strconv.ParseFloat(s.Duration, 64)
			}
		case "audio":
			a := codecName(s.CodecName)
			if s.CodecName == "dts" && strings.Contains(s.Profile, "MA") {
				a = "DTS-HD MA"
			} else if s.CodecName == "dts" && strings.Contains(s.Profile, "X") {
				a = "DTS:X"
			} else if strings.Contains(s.Profile, "Atmos") {
				a += " Atmos"
			}
			if c := channels(s.Channels, s.Layout); c != "" {
				a += " " + c
			}
			if l := s.Tags["language"]; l != "" && l != "und" {
				a += " " + l
			}
			m.Audio = append(m.Audio, a)
		case "subtitle":
			m.Subtitles++
		}
	}
	return m, nil
}

// editionRe finds the cut a file name says it is.
var editionRe = regexp.MustCompile(`(?i)\{edition-([^}]+)\}|\b(extended(?: (?:cut|edition))?|director'?s cut|theatrical(?: cut)?|unrated|uncut|remastered|imax|special edition|final cut|ultimate (?:cut|edition)|criterion)\b`)

// Edition is the cut a file's name says it is, or "".
func Edition(name string) string {
	m := editionRe.FindStringSubmatch(strings.NewReplacer(".", " ", "_", " ").Replace(filepath.Base(name)))
	if m == nil {
		return ""
	}
	e := m[1]
	if e == "" {
		e = m[2]
	}
	return strings.ToUpper(e[:1]) + strings.ToLower(e[1:])
}

// copyLength is how long each part must run to be taken for a whole
// copy: parts of one movie are rarely this long each. Matinee uses the
// same length to tell parts it plays as one from versions of a movie.
const copyLength = 80 * 60

// inspect reads what's in the files of problems where it helps to choose:
// copies of a title, and parts. Parts that each run as long as a movie,
// about as long as each other, are likely copies; the fix then suggests
// keeping the best one, and joining is still there to pick.
func (s *Service) inspect(ctx context.Context, rep *Report) {
	tools, err := s.tools()
	for i := range rep.Problems {
		p := &rep.Problems[i]
		if p.Kind != Parts && p.Kind != Duplicate {
			continue
		}
		for j := range p.Files {
			f := &p.Files[j]
			f.Edition = Edition(f.Path)
			if err != nil || ctx.Err() != nil {
				continue
			}
			if m, ierr := tools.Inspect(ctx, f.Path); ierr == nil {
				f.Media = m
				if f.Edition == "" && m.Title != "" {
					f.Edition = Edition(m.Title)
				}
			} else if s.Log != nil {
				s.Log.Debug("problem files: couldn't read a video", "path", f.Path, "err", ierr)
			}
		}
		if p.Kind == Parts && p.Fix != nil && likelyCopies(p.Files) {
			p.Copies = true
			p.Fix.Keep = best(p.Files)
		}
		if p.Kind == Duplicate && p.Fix != nil && !slices.ContainsFunc(p.Files, func(f File) bool { return f.Recorded }) {
			p.Fix.Keep = best(p.Files)
		}
		// Different cuts of a movie aren't duplicates.
		asEditions(p)
	}
}

// likelyCopies says whether parts each run as long as a whole movie.
func likelyCopies(files []File) bool {
	for _, f := range files {
		if f.Media == nil || f.Media.Duration < copyLength {
			return false
		}
	}
	return len(files) > 1
}

// best is the copy with the most picture, then the longest, then the
// biggest.
func best(files []File) string {
	score := func(f File) (int, int, float64, int64) {
		if f.Media == nil {
			return 0, 0, 0, f.Size
		}
		hdr := 0
		if f.Media.HDR != "" {
			hdr = 1
		}
		return f.Media.Width * f.Media.Height, hdr, f.Media.Duration, f.Size
	}
	b := files[0]
	for _, f := range files[1:] {
		p1, h1, d1, s1 := score(f)
		p0, h0, d0, s0 := score(b)
		if p1 > p0 || p1 == p0 && (h1 > h0 || h1 == h0 && (d1 > d0+60 || d1 >= d0-60 && s1 > s0)) {
			b = f
		}
	}
	return b.Path
}
