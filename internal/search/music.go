package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// JobSearchAlbum searches for one album and grabs the best release.
const JobSearchAlbum = "search-album"

// Newznab's audio categories. Music videos, audiobooks and podcasts are
// left out of an album search.
const (
	catAudio        = 3000
	catMP3          = 3010
	catMusicVideo   = 3020
	catLossless     = 3040
	catPodcast      = 3050
	catAudioLast    = 3999
	minAlbumSize    = 2 << 20 // under this it's a track, not an album
	maxAlbumSize    = 6 << 30 // over this it's a discography or a box of them
	musicQualityKey = "music_quality"
)

// Music qualities: which of FLAC and MP3 an album is wanted in.
const (
	// MusicFLACFirst takes FLAC when there is one, and MP3 when there isn't.
	MusicFLACFirst = "flac-first"
	// MusicFLAC takes FLAC only.
	MusicFLAC = "flac"
	// MusicMP3 takes MP3 only, which is about a third of the size.
	MusicMP3 = "mp3"
)

// MusicQualities are the choices, in the order they're offered.
var MusicQualities = []string{MusicFLACFirst, MusicFLAC, MusicMP3}

// MusicQuality is which of FLAC and MP3 albums are wanted in.
func MusicQuality(ctx context.Context, st *store.Store) (string, error) {
	v, err := st.Setting(ctx, musicQualityKey)
	if err != nil {
		return "", err
	}
	if !slices.Contains(MusicQualities, v) {
		return MusicFLACFirst, nil
	}
	return v, nil
}

// SetMusicQuality saves which of FLAC and MP3 albums are wanted in.
func SetMusicQuality(ctx context.Context, st *store.Store, q string) error {
	if !slices.Contains(MusicQualities, q) {
		return fmt.Errorf("there's no music quality %q", q)
	}
	return st.SetSetting(ctx, musicQualityKey, q)
}

// ErrMusicOff means music isn't turned on in Settings.
var ErrMusicOff = errors.New("turn on Music in Settings first")

// albumCats are the categories to search an indexer in for an album.
func albumCats(ix store.Indexer) []int {
	out := slices.DeleteFunc(cats(ix, catAudio, catAudioLast, catAudio), func(c int) bool {
		return c == catMusicVideo || c == catAudiobook || c == catPodcast
	})
	if len(out) == 0 {
		out = []int{catAudio}
	}
	return out
}

// Audio formats as release names give them.
var (
	reFLAC        = regexp.MustCompile(`(?i)\b(flac|lossless|alac|24 ?bit|16 ?bit)\b`)
	reMP3         = regexp.MustCompile(`(?i)\b(mp3|320 ?k?(bps)?|v0|v2|vbr|cbr|aac|m4a|\d{3} ?kbps)\b`)
	reDiscography = regexp.MustCompile(`(?i)\b(discography|discografia|complete (albums|studio albums|discography|collection)|anthology|box ?set|the albums|\d+ albums)\b`)
	reMusicVideo  = regexp.MustCompile(`(?i)\b(1080p|720p|2160p|x264|x265|h ?264|hevc|blu ?ray|bdrip|dvdrip|web ?dl|music ?video|concert|live at .* (dvd|bd))\b`)
	reAnyYear     = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

// albumFormat reads the format a release name gives: FLAC, MP3, or "" when
// it doesn't say.
func albumFormat(r Release) string {
	switch {
	case reFLAC.MatchString(r.Title), slices.Contains(r.Categories, catLossless):
		return "FLAC"
	case reMP3.MatchString(r.Title), slices.Contains(r.Categories, catMP3):
		return "MP3"
	}
	return ""
}

// formatRank is how good a format is for the quality wanted, higher
// being better.
func formatRank(format, want string) int {
	switch {
	case format == "FLAC" && want != MusicMP3:
		return 2
	case format == "MP3" && want == MusicMP3:
		return 2
	case format == "MP3":
		return 1
	}
	return 0
}

// words are a name's words, without the ones left out of matching.
func words(s string) []string {
	var out []string
	for _, w := range strings.Fields(normalize(s)) {
		if !stopWords[w] {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		out = strings.Fields(normalize(s))
	}
	return out
}

// inOrder says whether every word is in text (spaced, normalized), in
// order, from at; it gives where the last one ends, or -1.
func inOrder(text string, ws []string, at int) int {
	for _, w := range ws {
		i := strings.Index(text[at:], " "+w+" ")
		if i < 0 {
			return -1
		}
		at += i + len(w) + 1
	}
	return at
}

// albumMatches says whether a release is named for an album: the artist's
// name and then the album's title, each word in order. A title that's only
// a word or two must come right after the artist, so "Weezer" doesn't
// match every Weezer album.
func albumMatches(release, artist, title string) (bool, string) {
	rel := " " + normalize(release) + " "
	at := inOrder(rel, words(artist), 0)
	if at < 0 {
		return false, "it doesn't name the artist"
	}
	main := reTrailingTag.ReplaceAllString(title, "")
	tw := words(main)
	if inOrder(rel, tw, at) < 0 {
		return false, "its name doesn't match the album's"
	}
	if len(tw) <= 2 && !strings.HasPrefix(rel[at:], " "+strings.Join(tw, " ")+" ") {
		return false, "its name doesn't match the album's"
	}
	return true, ""
}

var reTrailingTag = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]\s*$`)

// albumQuery is the text to search for: the artist and the title, without
// punctuation, which indexers match poorly.
func albumQuery(a store.Artist, b store.Album) string {
	return strings.TrimSpace(normalize(a.Name) + " " + normalize(reTrailingTag.ReplaceAllString(b.Title, "")))
}

// AlbumSubject is how an album is named in jobs and logs: "OK Computer by
// Radiohead".
func AlbumSubject(a store.Artist, b store.Album) string {
	return fmt.Sprintf("%s by %s", b.Title, a.Name)
}

// AlbumJob is the payload of a search job for an album.
func AlbumJob(id int64) string {
	p, _ := json.Marshal(struct {
		ID int64 `json:"id"`
	}{id})
	return string(p)
}

func (s *Service) musicOn(ctx context.Context) error {
	on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Music)
	if err != nil {
		return err
	}
	if !on {
		return ErrMusicOff
	}
	return nil
}

// Album searches for an album and judges each release.
func (s *Service) Album(ctx context.Context, id int64) (Results, error) {
	if err := s.musicOn(ctx); err != nil {
		return Results{}, err
	}
	b, err := s.Store.Album(ctx, id)
	if err != nil {
		return Results{}, err
	}
	a, err := s.Store.Artist(ctx, b.ArtistID)
	if err != nil {
		return Results{}, err
	}
	want, err := MusicQuality(ctx, s.Store)
	if err != nil {
		return Results{}, err
	}
	text := albumQuery(a, b)
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		return Query{Type: "search", Text: text, Cats: albumCats(ix)}
	})
	if err != nil {
		return Results{}, err
	}
	blocked, err := s.blocked(ctx, store.AlbumKind, b.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		out.Releases = append(out.Releases, s.judgeAlbum(r, a, b, want, blocked))
	}
	s.rank(ctx, &out)
	s.markSearched(ctx, store.AlbumKind, b.ID, out)
	s.logResults(AlbumSubject(a, b), out)
	return out, nil
}

// judgeAlbum says whether a release is the album in the quality wanted.
func (s *Service) judgeAlbum(r Release, a store.Artist, b store.Album, want string, blocked map[string]bool) Candidate {
	c := Candidate{Release: r, Accepted: true, Key: key(r), kind: store.AlbumKind, titleID: b.ID}
	c.Format = albumFormat(r)
	c.formatRank = formatRank(c.Format, want)
	if r.Protocol != "usenet" {
		c.reject("torrent downloads aren't supported yet")
	}
	if blocked[strings.ToLower(r.Title)] {
		c.reject("it's on the blocklist")
	}
	if lockedNoPassword(r) {
		c.reject(whyLocked)
	}
	if ok, why := albumMatches(r.Title, a.Name, b.Title); !ok {
		c.reject(why)
	}
	switch {
	case slices.Contains(r.Categories, catAudiobook) || reAudiobook.MatchString(r.Title):
		c.reject("it's an audiobook")
	case slices.Contains(r.Categories, catMusicVideo) || reMusicVideo.MatchString(r.Title):
		c.reject("it's a video")
	}
	if reDiscography.MatchString(r.Title) && !reDiscography.MatchString(b.Title) {
		c.reject("it's a collection of albums, not this one")
	}
	if y := releasedYear(b.Released); y > 0 && yearOff(r.Title, y) {
		// A year far off is a remaster, a reissue or another album of the
		// same name; it isn't refused, only ranked below.
		c.formatRank--
	}
	switch {
	case want == MusicFLAC && c.Format != "FLAC":
		c.reject("albums are wanted in FLAC only, and it doesn't say it's FLAC")
	case want == MusicMP3 && c.Format == "FLAC":
		c.reject("albums are wanted in MP3, and it's FLAC")
	}
	if r.Size > 0 && r.Size < minAlbumSize {
		c.reject(fmt.Sprintf("at %s it's too small to be an album", humanSize(r.Size)))
	}
	if r.Size > maxAlbumSize {
		c.reject(fmt.Sprintf("at %s it's too big to be one album", humanSize(r.Size)))
	}
	if b.File != nil {
		c.reject("the album's in the library already")
	}
	return c
}

// yearOff says whether a release name gives years and none of them is
// within a year of y.
func yearOff(title string, y int) bool {
	m := reAnyYear.FindAllString(title, -1)
	return len(m) > 0 && !slices.ContainsFunc(m, func(s string) bool {
		var n int
		fmt.Sscanf(s, "%d", &n)
		return n >= y-1 && n <= y+1
	})
}

// releasedYear is the year in a "YYYY-MM-DD", "YYYY-MM" or "YYYY" date.
func releasedYear(d string) int {
	var y int
	if len(d) >= 4 {
		fmt.Sscanf(d[:4], "%d", &y)
	}
	return y
}

func (s *Service) jobAlbum(ctx context.Context, payload string) (jobs.Result, error) {
	id, err := payloadID(payload)
	if err != nil {
		return jobs.Result{}, err
	}
	res, err := s.Album(ctx, id)
	if errors.Is(err, ErrNoIndexers) || errors.Is(err, ErrMusicOff) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, store.AlbumKind, id, 0)
}

// wantedAlbums are the albums wanted that are out and have no copy in the
// library yet. Before listing them, each of their artists' folders is
// looked at for albums there already, so an album Lidarr got is never
// downloaded again.
func (s *Service) wantedAlbums(ctx context.Context, now time.Time) ([]Wanted, error) {
	if on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Music); err != nil || !on {
		return nil, err
	}
	today := now.UTC().Format(time.DateOnly)
	list, err := s.Store.WantedAlbums(ctx, today)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	artists := map[int64]store.Artist{}
	found := 0
	for _, b := range list {
		if _, ok := artists[b.ArtistID]; ok {
			continue
		}
		a, err := s.Store.Artist(ctx, b.ArtistID)
		if err != nil {
			return nil, err
		}
		artists[a.ID] = a
		n, err := downloader.FindAlbums(ctx, s.Store, a)
		if err != nil {
			s.logger().Warn("couldn't look for an artist's albums on disk", "artist", a.Name, "err", err)
		}
		found += n
	}
	if found > 0 {
		if list, err = s.Store.WantedAlbums(ctx, today); err != nil {
			return nil, err
		}
	}
	searched, err := s.Store.Searched(ctx, store.AlbumKind)
	if err != nil {
		return nil, err
	}
	var out []Wanted
	for _, b := range list {
		out = append(out, Wanted{Kind: store.AlbumKind, ID: b.ID, Subject: AlbumSubject(artists[b.ArtistID], b),
			Searched: searched[b.ID].At, Misses: searched[b.ID].Misses})
	}
	return out, nil
}
