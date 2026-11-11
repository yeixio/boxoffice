package libscan

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/yeixio/boxoffice/internal/names"
)

// Guess is what a folder's name says about the title in it.
type Guess struct {
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	TMDBID int64  `json:"tmdbId,omitempty"`
	TVDBID int64  `json:"tvdbId,omitempty"`
	IMDBID string `json:"imdbId,omitempty"`
}

var (
	// Ids as Radarr, Sonarr, Plex and Jellyfin put them in folder names:
	// "{tmdb-603}", "[imdbid-tt0133093]", "{tvdb-371980}".
	reID = regexp.MustCompile(`(?i)[\[{(]\s*(tmdb|tmdbid|imdb|imdbid|tvdb|tvdbid)\s*[-=: ]\s*(tt\d+|\d+)\s*[\]})]`)
	// A year on its own: "(1999)", " 1999", ".1999.".
	reYear = regexp.MustCompile(`(?:^|[\s(\[])((?:18|19|20)\d{2})(?:[)\]\s]|$)`)
	// Where a release name's tags start, when there's no year to cut at.
	reTags = regexp.MustCompile(`(?i)\s(2160p|1080p|720p|576p|480p|4k|uhd|bluray|blu-ray|bdrip|brrip|web-?dl|webrip|web|hdtv|dvdrip|remux|x264|x265|h\.?264|h\.?265|hevc|complete|s\d{1,2}(e\d{1,3})?|season\s?\d+)\b.*$`)
)

// ParseName reads a title's folder name, such as "The Matrix (1999)",
// "The.Matrix.1999.1080p.BluRay-GRP" or "Severance {tvdb-371980}".
func ParseName(name string) Guess {
	// "Ame\u0301lie" as macOS writes it, or garbled "LÃ©on", is the title
	// it was meant to be.
	name = names.Repair(name)
	var g Guess
	for _, m := range reID.FindAllStringSubmatch(name, -1) {
		switch strings.ToLower(m[1]) {
		case "tmdb", "tmdbid":
			g.TMDBID, _ = strconv.ParseInt(m[2], 10, 64)
		case "tvdb", "tvdbid":
			g.TVDBID, _ = strconv.ParseInt(m[2], 10, 64)
		case "imdb", "imdbid":
			if strings.HasPrefix(m[2], "tt") {
				g.IMDBID = m[2]
			}
		}
	}
	s := reID.ReplaceAllString(name, " ")
	// Scene names use dots or underscores for spaces.
	if !strings.Contains(s, " ") {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	}
	// The last year that isn't the whole title ("1917", "2012") ends it.
	if ms := reYear.FindAllStringSubmatchIndex(s, -1); len(ms) > 0 {
		for i := len(ms) - 1; i >= 0; i-- {
			start := ms[i][2]
			if strings.TrimSpace(strings.Trim(s[:start], " ([-")) == "" {
				continue
			}
			g.Year, _ = strconv.Atoi(s[ms[i][2]:ms[i][3]])
			s = s[:start]
			break
		}
	}
	s = reTags.ReplaceAllString(" "+s, "")
	s = strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("([-_", r) })
	g.Title = strings.Join(strings.Fields(s), " ")
	return g
}

// same says two titles are the same but for case, accents, punctuation,
// "and" for "&" and a leading "The".
func same(a, b string) bool { return norm(a) != "" && norm(a) == norm(b) }

func norm(s string) string {
	s = names.Fold(strings.ReplaceAll(s, "&", " and "))
	s = strings.TrimPrefix(strings.TrimSpace(s), "the ")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// reEpisode finds an episode's code in a file name: S01E02, S01E02E03,
// 1x02.
var reEpisode = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})(?:[ ._-]?E(\d{1,3}))?\b|\b(\d{1,2})x(\d{2,3})\b`)

// episodeCode reads the season and episode a file is, or ok false.
func episodeCode(name string) (season, episode int, ok bool) {
	m := reEpisode.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	if m[1] != "" {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
	} else {
		season, _ = strconv.Atoi(m[4])
		episode, _ = strconv.Atoi(m[5])
	}
	return season, episode, true
}
