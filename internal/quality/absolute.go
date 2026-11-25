package quality

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/namefix"
)

// Anime releases mostly number a show's episodes straight through
// ("[Group] Show - 137 [1080p]") instead of by season, and say whether the
// sound is dubbed.

var (
	reLeadTags = regexp.MustCompile(`^(?:\s*[\[(][^\])]*[\])])+\s*`)
	// After the show's name: an optional year, then the number, alone or
	// as a range (001-012), with an optional version (137v2).
	absTail = `(?:[\s._]*\(?(?:19|20)\d\d\)?)?[\s._]*-?[\s._]*(?:(?:episode|ep|e)[\s._]*)?(\d{1,4})(?:v\d+)?(?:[\s._]*[-~][\s._]*(\d{1,4})(?:v\d+)?)?(?:$|[\s._\[(-])`
	// With no name to go by: "Anything - 137".
	reAbsLoose = regexp.MustCompile(`^.+?[\s._]-[\s._]+(?:(?:episode|ep|e)[\s._]*)?(\d{1,4})(?:v\d+)?(?:[\s._]*[-~][\s._]*(\d{1,4})(?:v\d+)?)?(?:$|[\s._\[(])`)
	reTitleSep = regexp.MustCompile(`[^\pL\pN]+`)
)

// StripLeadTags takes the bracketed tags off the front of a release's
// name: "[Group] Show - 05" is "Show - 05".
func StripLeadTags(name string) string { return reLeadTags.ReplaceAllString(name, "") }

// AbsoluteEpisodes reads the episode numbers of a release, or a file, that
// numbers a show's episodes straight through: "[Group] Show - 137
// [1080p]", "Show.137.1080p", "Show - 001-012". The number has to follow
// the show's title. With loose, a name that doesn't start with the title
// (a release under the show's name in another language, found by the
// show's id) is read too, when it's written "Name - 137". It gives nil
// when the name has no such number.
func AbsoluteEpisodes(name, title string, loose bool) []int {
	name = namefix.Fold(reLeadTags.ReplaceAllString(name, ""))
	var m []string
	if words := strings.Fields(reTitleSep.ReplaceAllString(namefix.Fold(title), " ")); len(words) > 0 {
		for i, w := range words {
			words[i] = regexp.QuoteMeta(w)
		}
		if re, err := regexp.Compile(`(?i)^` + strings.Join(words, `[^\pL\pN]*`) + absTail); err == nil {
			m = re.FindStringSubmatch(name)
		}
	}
	if m == nil && loose {
		m = reAbsLoose.FindStringSubmatch(name)
	}
	if m == nil {
		return nil
	}
	first, _ := strconv.Atoi(m[1])
	if first == 0 {
		return nil
	}
	out := []int{first}
	if last, _ := strconv.Atoi(m[2]); last > first && last-first < 500 {
		for n := first + 1; n <= last; n++ {
			out = append(out, n)
		}
	}
	return out
}

var (
	reDual   = regexp.MustCompile(`(?i)\b(?:dual[\s._-]?audio|dual|multi(?:[\s._-]?audio)?|multi\d?)\b`)
	reDubbed = regexp.MustCompile(`(?i)\b(?:dubbed|dub|eng(?:lish)?[\s._-]?dub|dublado)\b`)
	reSubbed = regexp.MustCompile(`(?i)\b(?:subbed|eng(?:lish)?[\s._-]?sub(?:s|bed)?|vostfr|multi[\s._-]?subs?)\b`)
)

// Dubbing is what a release's name says about its sound: Dubbed when it's
// dubbed (alone or next to the original, as Dual Audio and MULTi are),
// and Original when the original sound is there (said outright with
// "subbed", or by being dual). A name that says nothing gives neither.
func Dubbing(name string) (dubbed, original bool) {
	dual := reDual.MatchString(name)
	if reSubbed.MatchString(name) && !dual {
		return false, true
	}
	return dual || reDubbed.MatchString(name), dual
}
