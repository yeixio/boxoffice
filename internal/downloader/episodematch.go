package downloader

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/namefix"
	"github.com/yeixio/boxoffice/internal/store"
)

// VideoTags is what a video file says about itself in its own metadata:
// many web releases carry the episode's title or the store's synopsis of
// it even when the file's name is a random hash.
type VideoTags struct {
	Title       string
	Description string
	Season      int
	Episode     int
	// Duration is how long the video runs, when its headers say.
	Duration time.Duration
}

// ffprobeTags reads a video's title, synopsis and season/episode tags.
// Without ffprobe it reads nothing.
func ffprobeTags(ctx context.Context, path string) (VideoTags, error) {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return VideoTags{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-v", "error", "-show_entries", "format_tags", "-of", "json", "-i", path).Output()
	if err != nil {
		return VideoTags{}, err
	}
	return parseTags(out)
}

func parseTags(out []byte) (VideoTags, error) {
	var p struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return VideoTags{}, err
	}
	tags := map[string]string{}
	for k, v := range p.Format.Tags {
		tags[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	var t VideoTags
	t.Title = tags["title"]
	for _, k := range []string{"description", "synopsis", "summary", "comment", "long_description"} {
		if v := tags[k]; len(v) > len(t.Description) {
			t.Description = v
		}
	}
	// MP4s from TV stores (and Matroska files tagged by the same tools)
	// can say which episode they are outright.
	for _, k := range []string{"season_number", "season"} {
		if n, err := strconv.Atoi(tags[k]); err == nil && t.Season == 0 {
			t.Season = n
		}
	}
	for _, k := range []string{"episode_sort", "episode", "part_number"} {
		if n, err := strconv.Atoi(tags[k]); err == nil && t.Episode == 0 {
			t.Episode = n
		}
	}
	return t, nil
}

var (
	reSeasonOnly = regexp.MustCompile(`(?i)(?:^|[\W_])S(\d{1,2})(?:[\W_]|$)|(?:^|[\W_])Season[ ._-]?(\d{1,2})(?:[\W_]|$)`)
)

// releaseSeason is the season a release is named for when it's a whole
// season (Show.S05.576p.WEB-DL), or 0.
func releaseSeason(name string) int {
	if _, n := episodeNumbers(name); len(n) > 0 {
		return 0
	}
	m := reSeasonOnly.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1] + m[2])
	return n
}

// matchByTags works out which episode each unnamed video is from what the
// files themselves say: season/episode tags first, then the title tag,
// then the synopsis compared with each episode's overview. A video is only
// matched when one episode stands out clearly from the rest, and each
// episode goes to one video at most; the others are left unmatched.
//
// runtime is the show's usual episode length: a video far longer or
// shorter than that (a double-length special, say) isn't matched by its
// synopsis, as the episode it most resembles is unlikely to be it.
func matchByTags(tags map[string]VideoTags, eps []store.Episode, season int, runtime time.Duration) map[string]store.Episode {
	cands := eps
	if season > 0 {
		cands = slices.DeleteFunc(slices.Clone(eps), func(e store.Episode) bool { return e.Season != season })
	}
	out := map[string]store.Episode{}
	taken := map[int64]bool{}
	var names []string
	for v := range tags {
		names = append(names, v)
	}
	slices.Sort(names)
	// Told outright.
	for _, v := range names {
		t := tags[v]
		s, n := t.Season, t.Episode
		if ts, tn := episodeNumbers(t.Title); len(tn) > 0 {
			s, n = ts, tn[0]
		}
		if s == 0 && season > 0 && n > 0 {
			s = season
		}
		if n == 0 || season > 0 && s != season {
			continue
		}
		if i := slices.IndexFunc(cands, func(e store.Episode) bool { return e.Season == s && e.Number == n }); i >= 0 && !taken[cands[i].ID] {
			out[v], taken[cands[i].ID] = cands[i], true
		}
	}
	// Named by title: only a title that's one episode's alone counts.
	for _, v := range names {
		if _, ok := out[v]; ok {
			continue
		}
		want := normalTitle(tags[v].Title)
		if want == "" {
			continue
		}
		var hits []store.Episode
		for _, e := range cands {
			if normalTitle(e.Title) == want {
				hits = append(hits, e)
			}
		}
		if len(hits) == 1 && !taken[hits[0].ID] {
			out[v], taken[hits[0].ID] = hits[0], true
		}
	}
	// Told by the synopsis. matchRound reads the rest as it needs them.
	var docs [][]string
	for _, e := range cands {
		docs = append(docs, words(e.Title+" "+e.Overview))
	}
	idf := idfOf(docs)
	// Rounds: each match takes its episode out of the running, which can
	// make another video's best episode stand out next round.
	for {
		before := len(out)
		matchRound(tags, names, cands, docs, idf, runtime, out, taken)
		if len(out) == before {
			break
		}
	}
	return out
}

// matchRound matches each video whose best untaken episode stands out
// from the rest, best matches first.
func matchRound(tags map[string]VideoTags, names []string, cands []store.Episode, docs [][]string, idf map[string]float64, runtime time.Duration, out map[string]store.Episode, taken map[int64]bool) {
	type pair struct {
		v     string
		e     int
		score float64
	}
	var pairs []pair
	best := map[string][2]float64{} // each video's two best scores
	for _, v := range names {
		if _, ok := out[v]; ok || tags[v].Description == "" || !usualLength(tags[v].Duration, runtime) {
			continue
		}
		w := words(tags[v].Description)
		for i := range cands {
			if taken[cands[i].ID] {
				continue
			}
			s := similarity(w, docs[i], idf)
			pairs = append(pairs, pair{v, i, s})
			b := best[v]
			switch {
			case s > b[0]:
				b = [2]float64{s, b[0]}
			case s > b[1]:
				b[1] = s
			}
			best[v] = b
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		return 0
	})
	for _, p := range pairs {
		e := cands[p.e]
		if _, ok := out[p.v]; ok || taken[e.ID] {
			continue
		}
		// Clearly this episode: alike enough, and well ahead of the
		// runner-up for this video.
		b := best[p.v]
		if p.score != b[0] || p.score < minMatch || p.score-b[1] < minLead {
			continue
		}
		out[p.v], taken[e.ID] = e, true
	}
}

// matchPack matches a download's videos by their tags: first among the
// season's episodes (or all of them), then the ones left over among the
// show's specials, which packs often carry along, where length isn't held
// against them, as specials run any length.
func matchPack(tags map[string]VideoTags, eps []store.Episode, season int, runtime time.Duration) map[string]store.Episode {
	out := matchByTags(tags, eps, season, runtime)
	left := map[string]VideoTags{}
	for v, t := range tags {
		if _, ok := out[v]; !ok {
			left[v] = t
		}
	}
	var specials []store.Episode
	for _, e := range eps {
		if e.Season == 0 && !slices.ContainsFunc(slices.Collect(maps.Values(out)), func(o store.Episode) bool { return o.ID == e.ID }) {
			specials = append(specials, e)
		}
	}
	if len(left) == 0 || len(specials) == 0 {
		return out
	}
	maps.Copy(out, matchByTags(left, specials, 0, 0))
	return out
}

// usualLength says whether a video runs about as long as the show's
// episodes do, or either isn't known.
func usualLength(d, runtime time.Duration) bool {
	if d <= 0 || runtime <= 0 {
		return true
	}
	return d*2 >= runtime && d*2 <= runtime*3
}

const (
	minMatch = 0.2
	minLead  = 0.1
)

func normalTitle(s string) string {
	return strings.Join(words(s), " ")
}

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for with that this from his her its their they them you your our are was were has have had not but all can will who what when where why how into out about over then than just also more most some any one two each both very off only own same too there here which while would could should been being does did doing yet get gets got make makes made show shows episode season`) {
		stopWords[w] = true
	}
}

// words are a text's distinct words worth comparing: lower case, without
// short and common ones, plurals folded.
func words(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(namefix.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' }) {
		w = strings.Trim(w, "'")
		w = strings.TrimSuffix(w, "'s")
		if len(w) < 3 || stopWords[w] {
			continue
		}
		if w = stem(w); seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// stem folds a word's common endings (loaves and loaf, flatness and flat,
// cooking and cook) so the same idea worded differently still meets.
func stem(w string) string {
	for _, suf := range []string{"ness", "ing", "ed"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			w = strings.TrimSuffix(w, suf)
			break
		}
	}
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ves"):
		w = strings.TrimSuffix(w, "ves") + "f"
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		w = strings.TrimSuffix(w, "ies") + "y"
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us"):
		w = strings.TrimSuffix(w, "s")
	}
	return w
}

// idfOf weighs each word by how few of the episodes use it, so words every
// episode shares (the host's name, say) count for nothing.
func idfOf(docs [][]string) map[string]float64 {
	n := map[string]int{}
	for _, d := range docs {
		for _, w := range d {
			n[w]++
		}
	}
	idf := map[string]float64{}
	for w, c := range n {
		idf[w] = math.Log(float64(len(docs)+1) / float64(c))
	}
	return idf
}

// similarity compares a synopsis with an episode's words, as the cosine
// of their weighted word sets. Synopsis words no episode uses can't tell
// episodes apart, so they're left out rather than diluting the rest.
func similarity(a, b []string, idf map[string]float64) float64 {
	in := map[string]bool{}
	var na, nb, dot float64
	for _, w := range b {
		x := idf[w]
		nb += x * x
		in[w] = true
	}
	for _, w := range a {
		x, ok := idf[w]
		if !ok {
			continue
		}
		na += x * x
		if in[w] {
			dot += x * x
		}
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
