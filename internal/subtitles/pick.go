package subtitles

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// Rank orders what the providers found for one language, best first, and
// leaves out what shouldn't be used by itself: machine translations, and
// forced subtitles that only cover the foreign parts.
//
// Timing matters most, so a subtitle made for this very file comes first,
// then one made for the same release (its group, then its source and
// size of picture), then trusted uploads and the most downloaded.
// preferSDH puts those for the deaf and hard of hearing ahead of the rest
// when all else is equal; otherwise they come after.
func Rank(found []Found, lang, release string, preferSDH bool) []Found {
	want := releaseOf(release)
	type scored struct {
		f     Found
		score int
	}
	var list []scored
	for _, f := range found {
		if f.Language != lang || f.Machine || f.Forced {
			continue
		}
		s := 0
		if f.MatchesFile {
			s += 1000
		}
		best := 0
		for _, r := range strings.Fields(f.Release) {
			best = max(best, want.match(releaseOf(r)))
		}
		if best == 0 && f.Release != "" {
			best = want.match(releaseOf(f.Release))
		}
		s += best
		if f.Trusted {
			s += 5
		}
		if f.SDH == preferSDH {
			s += 3
		}
		list = append(list, scored{f, s})
	}
	slices.SortStableFunc(list, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(b.f.Downloads, a.f.Downloads)
	})
	out := make([]Found, len(list))
	for i, s := range list {
		out[i] = s.f
	}
	return out
}

// release is what a release name says that matters for a subtitle's
// timing: who made it, from what, and how big its picture is.
type release struct {
	group, source, resolution string
}

var (
	reGroup      = regexp.MustCompile(`-([A-Za-z0-9]+)(?:\[[^\]]*\])?$`)
	reResolution = regexp.MustCompile(`(?i)\b(2160p|1080p|1080i|720p|576p|480p|4k)\b`)
	sources      = []struct{ name, re string }{
		{"bluray", `blu-?ray|bdrip|brrip|bdremux|\bbd\b|remux`},
		{"webdl", `web-?dl|webrip|\bweb\b|amzn|nf\b|dsnp|hmax|atvp`},
		{"hdtv", `hdtv|pdtv|dsr`},
		{"dvd", `dvdrip|dvd`},
	}
	sourceRes = func() []*regexp.Regexp {
		out := make([]*regexp.Regexp, len(sources))
		for i, s := range sources {
			out[i] = regexp.MustCompile(`(?i)` + s.re)
		}
		return out
	}()
)

func releaseOf(name string) release {
	name = strings.TrimSpace(name)
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".srt", ".m4v"} {
		name = strings.TrimSuffix(strings.TrimSuffix(name, ext), strings.ToUpper(ext))
	}
	var r release
	if m := reGroup.FindStringSubmatch(name); m != nil {
		r.group = strings.ToLower(m[1])
	}
	if m := reResolution.FindString(name); m != "" {
		r.resolution = strings.ToLower(m)
		if r.resolution == "4k" {
			r.resolution = "2160p"
		}
	}
	for i, re := range sourceRes {
		if re.MatchString(name) {
			r.source = sources[i].name
			break
		}
	}
	return r
}

// match scores how alike two releases are for timing.
func (r release) match(o release) int {
	s := 0
	if r.group != "" && r.group == o.group {
		s += 100
	}
	if r.source != "" && r.source == o.source {
		s += 30
	}
	if r.resolution != "" && r.resolution == o.resolution {
		s += 10
	}
	return s
}
