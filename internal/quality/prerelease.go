package quality

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// dvd is the DVD quality, which every DVD's copy is whatever its size.
var dvd, _ = ByID(2)

// preReleaseWords are the words that say a copy was made before the film
// came out, worst first, as prefNorm spells them. The short ones (ts, tc,
// wp, scr, r5) only count after the title, so a film called Cam isn't
// taken for one.
var preReleaseWords = []struct {
	id    int
	words []string
}{
	{18, []string{"cam", "camrip", "cam rip", "hdcam", "hd cam", "new cam"}},
	{19, []string{"ts", "hdts", "hd ts", "telesync", "tsrip", "ts rip", "pdvd", "predvd", "pre dvd"}},
	{20, []string{"tc", "hdtc", "hd tc", "telecine"}},
	{21, []string{"workprint", "wp"}},
	{23, []string{"scr", "screener", "screeners", "dvdscr", "dvd scr", "dvdscreener", "dvd screener", "ddc",
		"bdscr", "bd scr", "webscr", "web scr", "hdscr", "hd scr"}},
	{22, []string{"r5", "r6", "regional"}},
}

// hdScreener are screener words that say the copy is HD when the name
// gives no size.
var hdScreener = []string{"bdscr", "bd scr", "webscr", "web scr", "hdscr", "hd scr"}

// videoExt is a file's own ending, cut off before looking for words so a
// .ts file isn't taken for a telesync.
var videoExt = regexp.MustCompile(`(?i)\.(ts|m2ts|mts|mkv|mp4|m4v|avi|wmv|mov|mpg|mpeg|webm|vob|iso|divx|flv|ogm|nzb)$`)

// preRelease finds a pre-release quality in a release's or file's name.
func preRelease(name string) (Quality, bool) {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = videoExt.ReplaceAllString(name, "")
	tail := prefTail(name)
	full := " " + prefNorm(tail) + " "
	// The release group at the end ("x264-GRP") isn't a word about the
	// copy: a group called TS is no telesync. Longer words still count
	// there, as in "720p.HD-TS".
	if i := strings.LastIndex(tail, "-"); i >= 0 && !strings.ContainsAny(tail[i+1:], " ._") {
		tail = tail[:i]
	}
	words := " " + prefNorm(tail) + " "
	has := func(w string) bool {
		return strings.Contains(words, " "+w+" ") || (len(w) >= 5 || strings.Contains(w, " ")) && strings.Contains(full, " "+w+" ")
	}
	for _, p := range preReleaseWords {
		if !slices.ContainsFunc(p.words, has) {
			continue
		}
		if p.id == 23 {
			res := resolutionOf(strings.NewReplacer(".", " ", "_", " ").Replace(name))
			if res >= 720 || res == 0 && slices.ContainsFunc(hdScreener, has) {
				p.id = 24
			}
		}
		q, _ := ByID(p.id)
		return q, true
	}
	return Quality{}, false
}

// screenerAt is the screener quality for a picture res lines high.
func screenerAt(res int) Quality {
	id := 23
	if res >= 720 {
		id = 24
	}
	q, _ := ByID(id)
	return q
}
