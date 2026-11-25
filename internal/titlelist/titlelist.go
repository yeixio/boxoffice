// Package titlelist saves the list of titles Box Office keeps (movies,
// shows, artists and authors, with their ids and how each is watched) as a
// file a person can read, in JSON or CSV, and loads such a file into
// another Box Office, adding what's missing there. It never changes or
// removes a title that's already on the list, and it never touches a file
// on disk.
package titlelist

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/store"
)

const (
	Movie  = "movie"
	Show   = "show"
	Artist = "artist"
	Author = "author"

	// Most is the most titles read from one file, and MaxBytes the
	// biggest file read.
	Most     = 20000
	MaxBytes = 16 << 20
	// format is the version of the file's layout.
	format = 1
)

// Kinds are the kinds of title, in the order they're written.
var Kinds = []string{Movie, Show, Artist, Author}

// Error is a mistake in the file, fit to show whoever loaded it.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(f string, a ...any) error { return &Error{fmt.Sprintf(f, a...)} }

// Entry is one title on the list.
type Entry struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// What it's known by: TMDB (and IMDb, TVDB) for movies and shows,
	// MusicBrainz for artists, Open Library for authors.
	TMDBID int64  `json:"tmdbId,omitempty"`
	IMDBID string `json:"imdbId,omitempty"`
	TVDBID int64  `json:"tvdbId,omitempty"`
	MBID   string `json:"musicbrainzId,omitempty"`
	OLKey  string `json:"openLibraryKey,omitempty"`
	// Monitored is whether it's watched for. An author has no such
	// switch: theirs says whether anything of theirs is wanted.
	Monitored bool `json:"monitored"`
	// Profile is the quality profile's name, and RootFolder the library
	// folder it's in. Where a Box Office loading the file has neither,
	// its own defaults are used.
	Profile    string `json:"profile,omitempty"`
	RootFolder string `json:"rootFolder,omitempty"`
	// Seasons are the seasons of a show that are watched for, and
	// KeepAhead its space saver.
	Seasons   []int `json:"seasons,omitempty"`
	KeepAhead int   `json:"keepAhead,omitempty"`
	// AlbumTypes are the kinds of album an artist is watched for.
	AlbumTypes []string `json:"albumTypes,omitempty"`
	// NewEbooks and NewAudiobooks say an author's new books are wanted in
	// that format, and Books are the ones already picked.
	NewEbooks     bool   `json:"newEbooks,omitempty"`
	NewAudiobooks bool   `json:"newAudiobooks,omitempty"`
	Books         []Book `json:"books,omitempty"`
}

// Book is a book of an author's that's wanted.
type Book struct {
	Key       string `json:"openLibraryKey"`
	Title     string `json:"title,omitempty"`
	Ebook     bool   `json:"ebook,omitempty"`
	Audiobook bool   `json:"audiobook,omitempty"`
}

// Name is the title as a person would say it: with its year, when known.
func (e Entry) Name() string {
	if e.Year > 0 {
		return fmt.Sprintf("%s (%d)", e.Title, e.Year)
	}
	return e.Title
}

// key is what makes an entry the same title as another.
func (e Entry) key() string {
	switch e.Kind {
	case Movie, Show:
		switch {
		case e.TMDBID > 0:
			return fmt.Sprintf("%s tmdb %d", e.Kind, e.TMDBID)
		case e.Kind == Movie && e.IMDBID != "":
			return "movie imdb " + e.IMDBID
		case e.Kind == Show && e.TVDBID > 0:
			return fmt.Sprintf("show tvdb %d", e.TVDBID)
		}
	case Artist:
		if musicbrainz.IsMBID(e.MBID) {
			return "artist " + strings.ToLower(e.MBID)
		}
	case Author:
		if openlibrary.IsAuthorKey(e.OLKey) {
			return "author " + e.OLKey
		}
	}
	return ""
}

// File is the list as it's saved.
type File struct {
	App      string    `json:"app"`
	Format   int       `json:"format"`
	Exported time.Time `json:"exported"`
	Titles   []Entry   `json:"titles"`
}

// Export reads the whole list.
func Export(ctx context.Context, st *store.Store) (File, error) {
	out := File{App: "Box Office", Format: format, Exported: time.Now().UTC().Truncate(time.Second), Titles: []Entry{}}
	ps, err := st.Profiles(ctx)
	if err != nil {
		return out, err
	}
	profile := map[int]string{}
	for _, p := range ps {
		profile[p.ID] = p.Name
	}
	movies, err := st.Movies(ctx)
	if err != nil {
		return out, err
	}
	for _, m := range movies {
		out.Titles = append(out.Titles, Entry{Kind: Movie, Title: m.Title, Year: m.Year, TMDBID: m.TMDBID, IMDBID: m.IMDBID,
			Monitored: m.Monitored, Profile: profile[m.ProfileID], RootFolder: m.RootFolder})
	}
	shows, err := st.SeriesAll(ctx)
	if err != nil {
		return out, err
	}
	for _, t := range shows {
		e := Entry{Kind: Show, Title: t.Title, Year: t.Year, TMDBID: t.TMDBID, IMDBID: t.IMDBID, TVDBID: t.TVDBID,
			Monitored: t.Monitored, Profile: profile[t.ProfileID], RootFolder: t.RootFolder, KeepAhead: t.KeepAhead}
		for _, se := range t.Seasons {
			if se.Monitored {
				e.Seasons = append(e.Seasons, se.Number)
			}
		}
		slices.Sort(e.Seasons)
		out.Titles = append(out.Titles, e)
	}
	artists, err := st.Artists(ctx)
	if err != nil {
		return out, err
	}
	for _, a := range artists {
		out.Titles = append(out.Titles, Entry{Kind: Artist, Title: a.Name, MBID: a.MBID, Monitored: a.Monitored,
			RootFolder: a.RootFolder, AlbumTypes: a.AlbumTypes})
	}
	authors, err := st.Authors(ctx)
	if err != nil {
		return out, err
	}
	for _, a := range authors {
		e := Entry{Kind: Author, Title: a.Name, OLKey: a.OLKey, NewEbooks: a.NewEbooks, NewAudiobooks: a.NewAudiobooks}
		bs, err := st.Books(ctx, a.ID)
		if err != nil {
			return out, err
		}
		for _, b := range bs {
			if b.WantEbook || b.WantAudiobook {
				e.Books = append(e.Books, Book{Key: b.OLKey, Title: b.Title, Ebook: b.WantEbook, Audiobook: b.WantAudiobook})
			}
		}
		e.Monitored = e.NewEbooks || e.NewAudiobooks || len(e.Books) > 0
		out.Titles = append(out.Titles, e)
	}
	return out, nil
}

// WriteJSON writes the list as JSON, laid out to be read.
func (f File) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(f)
}

// columns are the CSV's, in order.
var columns = []string{"kind", "title", "year", "tmdb_id", "imdb_id", "tvdb_id", "musicbrainz_id", "openlibrary_key",
	"monitored", "profile", "root_folder", "seasons", "keep_ahead", "album_types", "new_ebooks", "new_audiobooks", "books"}

// WriteCSV writes the list as a spreadsheet: one row a title. Lists inside
// a cell (seasons, kinds of album, books) are separated by spaces, and a
// book is its Open Library key, a colon, and e, a or both for the formats
// wanted.
func (f File) WriteCSV(w io.Writer) error {
	c := csv.NewWriter(w)
	if err := c.Write(columns); err != nil {
		return err
	}
	for _, e := range f.Titles {
		seasons := make([]string, len(e.Seasons))
		for i, n := range e.Seasons {
			seasons[i] = strconv.Itoa(n)
		}
		books := make([]string, 0, len(e.Books))
		for _, b := range e.Books {
			s := b.Key + ":"
			if b.Ebook {
				s += "e"
			}
			if b.Audiobook {
				s += "a"
			}
			books = append(books, s)
		}
		row := []string{e.Kind, guard(e.Title), num(int64(e.Year)), num(e.TMDBID), e.IMDBID, num(e.TVDBID), e.MBID, e.OLKey,
			yes(e.Monitored), guard(e.Profile), e.RootFolder, strings.Join(seasons, " "), num(int64(e.KeepAhead)),
			strings.Join(e.AlbumTypes, " "), yesOrBlank(e.NewEbooks), yesOrBlank(e.NewAudiobooks), strings.Join(books, " ")}
		if err := c.Write(row); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}

// guard keeps a spreadsheet from running a name as a formula: one that
// starts like a formula gets an apostrophe in front, which Read takes off.
func guard(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func unguard(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(s[1])) {
		return s[1:]
	}
	return s
}

func num(n int64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func yesOrBlank(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

// Read reads a list from a file's contents: JSON as WriteJSON writes it
// (or just the array of titles), or CSV with a header row.
func Read(data []byte) (File, error) {
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(data) == 0 {
		return File{}, badf("the file is empty")
	}
	var f File
	var err error
	switch data[0] {
	case '{':
		if json.Unmarshal(data, &f) != nil {
			return f, badf("the file isn't a list of titles: it couldn't be read as JSON")
		}
		if f.Format > format {
			return f, badf("the file was saved by a newer Box Office; update this one to load it")
		}
	case '[':
		if json.Unmarshal(data, &f.Titles) != nil {
			return f, badf("the file isn't a list of titles: it couldn't be read as JSON")
		}
	default:
		if f.Titles, err = readCSV(data); err != nil {
			return f, err
		}
	}
	if len(f.Titles) == 0 {
		return f, badf("there are no titles in the file")
	}
	if len(f.Titles) > Most {
		return f, badf("the file has %d titles; the most one file can hold is %d", len(f.Titles), Most)
	}
	for i := range f.Titles {
		e := &f.Titles[i]
		e.Kind = kindOf(e.Kind)
		e.Title, e.Profile, e.RootFolder = strings.TrimSpace(e.Title), strings.TrimSpace(e.Profile), strings.TrimSpace(e.RootFolder)
		e.IMDBID, e.MBID, e.OLKey = strings.TrimSpace(e.IMDBID), strings.ToLower(strings.TrimSpace(e.MBID)), olKey(e.OLKey)
	}
	return f, nil
}

// olKey takes an author's key as Open Library writes it in links too.
func olKey(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndex(s, "/")+1:]
}

func kindOf(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "movie", "movies", "film":
		return Movie
	case "show", "shows", "series", "tv", "tv show":
		return Show
	case "artist", "artists", "music":
		return Artist
	case "author", "authors":
		return Author
	}
	return strings.TrimSpace(s)
}

// headers are the names a CSV column may go by, squeezed to letters and
// digits, and the column each means.
var headers = map[string]string{
	"kind": "kind", "type": "kind",
	"title": "title", "name": "title",
	"year":   "year",
	"tmdbid": "tmdb_id", "tmdb": "tmdb_id",
	"imdbid": "imdb_id", "imdb": "imdb_id", "const": "imdb_id",
	"tvdbid": "tvdb_id", "tvdb": "tvdb_id",
	"musicbrainzid": "musicbrainz_id", "mbid": "musicbrainz_id",
	"openlibrarykey": "openlibrary_key", "olkey": "openlibrary_key", "openlibraryid": "openlibrary_key",
	"monitored": "monitored",
	"profile":   "profile", "qualityprofile": "profile",
	"rootfolder": "root_folder",
	"seasons":    "seasons",
	"keepahead":  "keep_ahead",
	"albumtypes": "album_types",
	"newebooks":  "new_ebooks", "newaudiobooks": "new_audiobooks",
	"books": "books",
}

func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(s))
}

func readCSV(data []byte) ([]Entry, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	// A spreadsheet in some countries saves with semicolons.
	if first, _, _ := bytes.Cut(data, []byte("\n")); bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		r.Comma = ';'
	}
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, badf("the file isn't a list of titles: it's neither JSON nor a spreadsheet saved as CSV")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		if name, ok := headers[squeeze(h)]; ok {
			if _, dup := col[name]; !dup {
				col[name] = i
			}
		}
	}
	if _, ok := col["kind"]; !ok {
		return nil, badf(`the file needs a first row naming its columns, with one called "kind" (movie, show, artist or author)`)
	}
	out := make([]Entry, 0, len(rows)-1)
	for _, row := range rows[1:] {
		cell := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		if cell("kind") == "" && cell("title") == "" {
			continue
		}
		e := Entry{Kind: cell("kind"), Title: unguard(cell("title")), Year: int(whole(cell("year"))), TMDBID: whole(cell("tmdb_id")),
			IMDBID: cell("imdb_id"), TVDBID: whole(cell("tvdb_id")), MBID: cell("musicbrainz_id"), OLKey: cell("openlibrary_key"),
			Profile: unguard(cell("profile")), RootFolder: cell("root_folder"), KeepAhead: int(whole(cell("keep_ahead"))),
			NewEbooks: truthy(cell("new_ebooks"), false), NewAudiobooks: truthy(cell("new_audiobooks"), false)}
		if types := strings.Fields(cell("album_types")); len(types) > 0 {
			e.AlbumTypes = types
		}
		// A file with no such column, or a blank cell, means watch it.
		e.Monitored = truthy(cell("monitored"), true)
		for _, s := range strings.FieldsFunc(cell("seasons"), func(r rune) bool { return r == ' ' || r == ',' }) {
			if n, err := strconv.Atoi(s); err == nil {
				e.Seasons = append(e.Seasons, n)
			}
		}
		for _, s := range strings.Fields(cell("books")) {
			key, formats, _ := strings.Cut(s, ":")
			e.Books = append(e.Books, Book{Key: key, Ebook: formats == "" || strings.Contains(formats, "e"), Audiobook: strings.Contains(formats, "a")})
		}
		out = append(out, e)
	}
	return out, nil
}

func whole(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return max(n, 0)
}

func truthy(s string, blank bool) bool {
	switch strings.ToLower(s) {
	case "":
		return blank
	case "yes", "y", "true", "1", "on", "x":
		return true
	}
	return false
}

// Skip is a title in the file that can't be added, and why.
type Skip struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Plan is what loading a file would do.
type Plan struct {
	// New are the titles to add, and Counts how many of each kind.
	New    []Entry        `json:"-"`
	Counts map[string]int `json:"counts"`
	// Have is how many are on the list already; they're left as they are.
	Have int `json:"have"`
	// Skipped can't be added.
	Skipped []Skip `json:"skipped"`
	// OtherProfile and OtherFolder count titles to add whose quality
	// profile or root folder isn't one here, so the defaults are used.
	OtherProfile int `json:"otherProfile"`
	OtherFolder  int `json:"otherFolder"`
}

// here is what a Box Office has that a file's titles are checked against.
type here struct {
	profiles map[string]int
	roots    map[string]map[string]bool // by root folder kind
	on       map[string]bool            // media types turned on
}

func look(ctx context.Context, st *store.Store) (here, error) {
	h := here{profiles: map[string]int{}, roots: map[string]map[string]bool{}, on: map[string]bool{}}
	ps, err := st.Profiles(ctx)
	if err != nil {
		return h, err
	}
	for _, p := range ps {
		if _, ok := h.profiles[strings.ToLower(p.Name)]; !ok {
			h.profiles[strings.ToLower(p.Name)] = p.ID
		}
	}
	for _, kind := range []string{"movie", "series", mediatypes.Music, store.Ebook, store.Audiobook} {
		fs, err := st.RootFolders(ctx, kind)
		if err != nil {
			return h, err
		}
		h.roots[kind] = map[string]bool{}
		for _, f := range fs {
			h.roots[kind][f.Path] = true
		}
	}
	for _, key := range []string{mediatypes.Music, mediatypes.Ebook, mediatypes.Audiobook} {
		if h.on[key], err = mediatypes.Enabled(ctx, st, key); err != nil {
			return h, err
		}
	}
	return h, nil
}

// rootKind is the kind of root folder a kind of title lives in.
func rootKind(kind string) string {
	switch kind {
	case Movie:
		return "movie"
	case Show:
		return "series"
	case Artist:
		return mediatypes.Music
	}
	return ""
}

// format picks the book format an author is added for.
func (h here) format(e Entry) string {
	wantsA := e.NewAudiobooks || slices.ContainsFunc(e.Books, func(b Book) bool { return b.Audiobook })
	wantsE := e.NewEbooks || slices.ContainsFunc(e.Books, func(b Book) bool { return b.Ebook })
	usable := func(f string) bool { return h.on[f] && len(h.roots[f]) > 0 }
	switch {
	case wantsE && usable(store.Ebook):
		return store.Ebook
	case wantsA && usable(store.Audiobook):
		return store.Audiobook
	case wantsE || wantsA:
		return ""
	case usable(store.Ebook):
		return store.Ebook
	case usable(store.Audiobook):
		return store.Audiobook
	}
	return ""
}

// MakePlan works out what loading a file would add, without adding it.
func MakePlan(ctx context.Context, st *store.Store, f File) (Plan, error) {
	p := Plan{Counts: map[string]int{}, Skipped: []Skip{}}
	h, err := look(ctx, st)
	if err != nil {
		return p, err
	}
	seen := map[string]bool{}
	for _, e := range f.Titles {
		skip := func(format string, a ...any) {
			p.Skipped = append(p.Skipped, Skip{cmp.Or(e.Name(), "(no title)"), fmt.Sprintf(format, a...)})
		}
		if !slices.Contains(Kinds, e.Kind) {
			skip("its kind is %q; it has to be movie, show, artist or author", e.Kind)
			continue
		}
		key := e.key()
		if key == "" {
			skip("%s", map[string]string{
				Movie:  "it has no TMDB or IMDb id",
				Show:   "it has no TMDB or TVDB id",
				Artist: "it has no MusicBrainz id",
				Author: "it has no Open Library key",
			}[e.Kind])
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		have, err := has(ctx, st, e)
		if err != nil {
			return p, err
		}
		if have {
			p.Have++
			continue
		}
		switch e.Kind {
		case Movie, Show:
			if len(h.profiles) == 0 {
				skip("there's no quality profile here yet")
				continue
			}
			if len(h.roots[rootKind(e.Kind)]) == 0 {
				skip("there's no root folder for %ss here yet; add one under Settings → Library", e.Kind)
				continue
			}
		case Artist:
			if !h.on[mediatypes.Music] {
				skip("music is turned off here")
				continue
			}
			if len(h.roots[mediatypes.Music]) == 0 {
				skip("there's no root folder for music here yet")
				continue
			}
		case Author:
			if h.format(e) == "" {
				skip("ebooks and audiobooks are turned off here, or have no root folder yet")
				continue
			}
		}
		if e.Kind == Movie || e.Kind == Show {
			if _, ok := h.profiles[strings.ToLower(e.Profile)]; e.Profile != "" && !ok {
				p.OtherProfile++
			}
		}
		if k := rootKind(e.Kind); k != "" && e.RootFolder != "" && !h.roots[k][e.RootFolder] {
			p.OtherFolder++
		}
		p.New = append(p.New, e)
		p.Counts[e.Kind]++
	}
	return p, nil
}

// has says whether a title is on the list here already.
func has(ctx context.Context, st *store.Store, e Entry) (bool, error) {
	var err error
	switch e.Kind {
	case Movie:
		if e.TMDBID == 0 {
			return false, nil // found out when it's looked up
		}
		_, err = st.MovieByTMDB(ctx, e.TMDBID)
	case Show:
		if e.TMDBID == 0 {
			return false, nil
		}
		_, err = st.SeriesByTMDB(ctx, e.TMDBID)
	case Artist:
		_, err = st.ArtistByMBID(ctx, e.MBID)
	case Author:
		_, err = st.AuthorByKey(ctx, e.OLKey)
	}
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
