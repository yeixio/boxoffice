package quality

import (
	"fmt"
	"regexp"
	"strings"
)

// A profile can say how big a copy may be for its running time, in
// megabytes a minute, with numbers of its own for each way of packing
// video: a good AV1 copy is far smaller than an H.264 one of the same
// film, so one number for all would be too loose for one or too tight
// for the other.

// SizeLimit is the least and most megabytes a minute a copy may be. Zero
// means no limit on that side.
type SizeLimit struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Sizes are a profile's size limits. H264 also holds for older codecs and
// for releases whose names don't say how they're packed.
type Sizes struct {
	H264 SizeLimit `json:"h264"`
	HEVC SizeLimit `json:"hevc"`
	AV1  SizeLimit `json:"av1"`
}

// Any says whether any limit is set.
func (s Sizes) Any() bool { return s != Sizes{} }

func (s Sizes) check() error {
	for _, l := range []struct {
		name string
		SizeLimit
	}{{"H.264", s.H264}, {"H.265", s.HEVC}, {"AV1", s.AV1}} {
		switch {
		case l.Min < 0 || l.Max < 0:
			return fmt.Errorf("a size limit for %s is below zero", l.name)
		case l.Min > maxPerMinute || l.Max > maxPerMinute:
			return fmt.Errorf("a size limit for %s is more than %d MB a minute, which is bigger than any release", l.name, maxPerMinute)
		case l.Max > 0 && l.Min > l.Max:
			return fmt.Errorf("the smallest size for %s is more than the biggest", l.name)
		}
	}
	return nil
}

// maxPerMinute is the largest limit taken: a 4K remux is about 600 MB a
// minute, so more than this is a slip of the hand.
const maxPerMinute = 5000

var (
	reAV1  = regexp.MustCompile(`(?i)\bAV1\b`)
	reHEVC = regexp.MustCompile(`(?i)\b(?:[xh] ?265|hevc)\b`)
	reH264 = regexp.MustCompile(`(?i)\b(?:[xh] ?264|avc)\b`)
)

// Codec is how a release's name says its video is packed: "av1", "hevc",
// "h264", or "" when it doesn't say.
func Codec(name string) string {
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	switch {
	case reAV1.MatchString(name):
		return "av1"
	case reHEVC.MatchString(name):
		return "hevc"
	case reH264.MatchString(name):
		return "h264"
	}
	return ""
}

// SizeProblem says why a release of size bytes, for minutes of running
// time, is outside the profile's limits, or "" when it's fine. Nothing is
// said when the size or the running time isn't known.
func (p Profile) SizeProblem(name string, size int64, minutes int) string {
	if size <= 0 || minutes <= 0 || !p.Sizes.Any() {
		return ""
	}
	limit, codec := p.Sizes.H264, "H.264"
	switch Codec(name) {
	case "av1":
		limit, codec = p.Sizes.AV1, "AV1"
	case "hevc":
		limit, codec = p.Sizes.HEVC, "H.265"
	case "":
		codec = "H.264 (its name doesn't say how it's packed)"
	}
	per := float64(size) / 1e6 / float64(minutes)
	said := fmt.Sprintf("at %s for %d minutes it's %s MB a minute", sizeWords(size), minutes, perWords(per))
	switch {
	case limit.Max > 0 && per > limit.Max:
		return fmt.Sprintf("%s, and the %s profile takes at most %s for %s", said, p.Name, perWords(limit.Max), codec)
	case limit.Min > 0 && per < limit.Min:
		return fmt.Sprintf("%s, and the %s profile takes at least %s for %s", said, p.Name, perWords(limit.Min), codec)
	}
	return ""
}

func perWords(v float64) string {
	if v < 10 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
	}
	return fmt.Sprintf("%.0f", v)
}

func sizeWords(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}
