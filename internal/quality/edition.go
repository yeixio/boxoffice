package quality

import (
	"regexp"
	"strings"
)

// editions are the movie editions release names carry, most specific
// first, with the name Box Office and Matinee show for each.
var editions = []struct {
	name string
	re   *regexp.Regexp
}{
	{"Director's Cut", regexp.MustCompile(`(?i)\bdirector'?s[ ._-]?cut\b|\bdc\b[ ._-](?:remastered|bluray|web|1080p|2160p|720p)`)},
	{"Final Cut", regexp.MustCompile(`(?i)\bfinal[ ._-]?cut\b`)},
	{"Ultimate Edition", regexp.MustCompile(`(?i)\bultimate[ ._-]?(?:edition|cut)\b`)},
	{"Extended", regexp.MustCompile(`(?i)\bextended(?:[ ._-]?(?:edition|cut|version))?\b`)},
	{"Unrated", regexp.MustCompile(`(?i)\bunrated\b`)},
	{"Uncut", regexp.MustCompile(`(?i)\buncut\b`)},
	{"Theatrical", regexp.MustCompile(`(?i)\btheatrical(?:[ ._-]?(?:edition|cut|version))?\b`)},
	{"IMAX", regexp.MustCompile(`(?i)\bimax\b`)},
	{"Criterion", regexp.MustCompile(`(?i)\bcriterion\b`)},
	{"Anniversary Edition", regexp.MustCompile(`(?i)\b(?:\d+(?:th|st|nd|rd)[ ._-]?)?anniversary(?:[ ._-]?edition)?\b`)},
	{"Special Edition", regexp.MustCompile(`(?i)\bspecial[ ._-]?edition\b`)},
	{"Remastered", regexp.MustCompile(`(?i)\bremaster(?:ed)?\b`)},
}

// reEditionTag is the {edition-…} tag Box Office, Plex and Jellyfin put in
// file names.
var reEditionTag = regexp.MustCompile(`(?i)\{edition-([^}]+)\}`)

// Edition is the movie edition a release or file name says it is, by the
// name Box Office shows ("Director's Cut"), or "" for none named.
func Edition(name string) string {
	if m := reEditionTag.FindStringSubmatch(name); m != nil {
		for _, e := range editions {
			if e.re.MatchString(m[1] + " cut") {
				return e.name
			}
		}
		return strings.TrimSpace(m[1])
	}
	for _, e := range editions {
		if e.re.MatchString(name) {
			return e.name
		}
	}
	return ""
}

// Editions are the names Edition can give, for a picker.
func Editions() []string {
	out := make([]string, 0, len(editions))
	for _, e := range editions {
		out = append(out, e.name)
	}
	return out
}

// SameEdition says whether two edition names are the same edition, so
// "Directors Cut" and "Director's Cut" match.
func SameEdition(a, b string) bool {
	norm := func(s string) string {
		if e := Edition(s); e != "" {
			return e
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
	return norm(a) == norm(b)
}

// EditionTag is the file-name tag for an edition, like
// "{edition-Director's Cut}"; "" for none.
func EditionTag(edition string) string {
	edition = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|{}`, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(edition))
	if edition == "" {
		return ""
	}
	return "{edition-" + edition + "}"
}

// Copies of a movie that run this much apart are different cuts of it:
// at least 5 minutes, and 5% of the shorter. A copy made for PAL TV runs
// about 4% short, and copies differ by a few seconds of logos, so neither
// counts.
const (
	cutMinutes = 5
	cutShare   = 0.05
)

// DifferentCut says whether two copies' running times (seconds) are far
// enough apart to be different cuts. Unknown times (0) never are.
func DifferentCut(a, b float64) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	lo, hi := min(a, b), max(a, b)
	return hi-lo >= cutMinutes*60 && hi-lo >= cutShare*lo
}

// LengthEdition guesses the edition of a copy whose name doesn't say, by
// how long it runs (seconds) against the movie's other copies and its
// runtime as TMDB has it (minutes, 0 when unknown): a copy notably longer
// than another is the Extended cut and one notably shorter the
// Theatrical; one alone that runs well past the listed runtime (12% and
// 10 minutes, past what credits and logos add) is Extended. "" when
// nothing says.
func LengthEdition(seconds float64, others []float64, runtime int) string {
	if seconds <= 0 {
		return ""
	}
	longer, shorter := false, false
	for _, o := range others {
		if !DifferentCut(seconds, o) {
			continue
		}
		if seconds > o {
			longer = true
		} else {
			shorter = true
		}
	}
	switch {
	case longer:
		return "Extended"
	case shorter:
		return "Theatrical"
	case runtime > 0 && len(others) == 0:
		listed := float64(runtime * 60)
		if seconds-listed >= 10*60 && seconds >= listed*1.12 {
			return "Extended"
		}
	}
	return ""
}

// EditionOf is a movie copy's edition: what its name says, or else (with
// byLength set) what its running time suggests; see LengthEdition.
func EditionOf(name string, seconds float64, others []float64, runtime int) (edition string, byLength bool) {
	if e := Edition(name); e != "" {
		return e, false
	}
	if e := LengthEdition(seconds, others, runtime); e != "" {
		return e, true
	}
	return "", false
}
