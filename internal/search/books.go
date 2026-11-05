package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// JobSearchBook searches for one book in one format, and grabs the best
// release.
const JobSearchBook = "search-book"

// Newznab's book categories. Comics (7030) and magazines (7010) are left
// out: Box Office doesn't get those.
const (
	catBooks     = 7000
	catMags      = 7010
	catEbook     = 7020
	catComics    = 7030
	catAudiobook = 3030
)

// FormatOffError means the book's format isn't turned on in Settings.
type FormatOffError struct{ Format string }

func (e FormatOffError) Error() string {
	if e.Format == store.Audiobook {
		return "turn on Audiobooks in Settings first"
	}
	return "turn on Ebooks in Settings first"
}

// BookPayload is what a book search job is for.
type BookPayload struct {
	ID     int64  `json:"id"`
	Format string `json:"format"`
}

// bookCats are the categories to search an indexer in for a format: the
// ones it's set to use in that range, or else the standard one.
func bookCats(ix store.Indexer, format string) []int {
	if format == store.Audiobook {
		return cats(ix, catAudiobook, catAudiobook, catAudiobook)
	}
	out := slices.DeleteFunc(cats(ix, catBooks, catBooks+999, catEbook), func(c int) bool { return c == catComics || c == catMags })
	if len(out) == 0 {
		out = []int{catEbook}
	}
	return out
}

// Book formats as release names give them, best first.
var (
	ebookWords = []struct {
		re   *regexp.Regexp
		name string
	}{
		{regexp.MustCompile(`(?i)\b(epub|kepub)\b`), "EPUB"},
		{regexp.MustCompile(`(?i)\bazw3\b`), "AZW3"},
		{regexp.MustCompile(`(?i)\bkfx\b`), "KFX"},
		{regexp.MustCompile(`(?i)\bmobi\b`), "MOBI"},
		{regexp.MustCompile(`(?i)\bazw\b`), "AZW"},
		{regexp.MustCompile(`(?i)\bpdf\b`), "PDF"},
	}
	audioWords = []struct {
		re   *regexp.Regexp
		name string
	}{
		{regexp.MustCompile(`(?i)\bm4b\b`), "M4B"},
		{regexp.MustCompile(`(?i)\bflac\b`), "FLAC"},
		{regexp.MustCompile(`(?i)\b(mp3|\d{2,3}\s?kbps)\b`), "MP3"},
		{regexp.MustCompile(`(?i)\b(m4a|aac)\b`), "AAC"},
	}
	reAudiobook  = regexp.MustCompile(`(?i)\b(audio ?books?|unabridged|abridged|narrated|read by)\b`)
	reCollection = regexp.MustCompile(`(?i)\b(collection|omnibus|box ?set|complete works|books? \d+ ?- ?\d+|\d+ books)\b`)
	reComic      = regexp.MustCompile(`(?i)\b(cbr|cbz|comics?)\b`)
)

// bookFormat reads the format a release name gives, and how good it is
// (higher is better; 0 when it doesn't say).
func bookFormat(title, format string) (string, int) {
	words := ebookWords
	if format == store.Audiobook {
		words = audioWords
	}
	for i, w := range words {
		if w.re.MatchString(title) {
			return w.name, len(words) - i
		}
	}
	return "", 0
}

// Words left out when matching a book's title to a release name.
var stopWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "and": true}

// bookMatches says whether a release is named for a book: every word of
// its title (before any colon) is in the name, in order, and so is the
// author's last name.
func bookMatches(release, title, author string) (bool, string) {
	rel := " " + normalize(release) + " "
	main, _, _ := strings.Cut(title, ":")
	var want []string
	for _, w := range strings.Fields(normalize(main)) {
		if !stopWords[w] {
			want = append(want, w)
		}
	}
	if len(want) == 0 {
		want = strings.Fields(normalize(main))
	}
	at := 0
	for _, w := range want {
		i := strings.Index(rel[at:], " "+w+" ")
		if i < 0 {
			return false, "its name doesn't match the book's"
		}
		at += i + len(w) + 1
	}
	names := strings.Fields(normalize(author))
	if len(names) > 0 && !strings.Contains(rel, " "+names[len(names)-1]+" ") {
		return false, "it doesn't name the author"
	}
	return true, ""
}

// bookQuery is the text to search for: the author and the title, without
// a subtitle or punctuation, which indexers match poorly.
func bookQuery(a store.Author, b store.Book) string {
	main, _, _ := strings.Cut(b.Title, ":")
	return strings.TrimSpace(normalize(a.Name) + " " + normalize(main))
}

// Sizes that can't be one ebook or one audiobook.
const (
	maxEbookSize     int64 = 300 << 20
	minAudiobookSize int64 = 5 << 20
)

func formatNoun(format string) string {
	if format == store.Audiobook {
		return "audiobook"
	}
	return "ebook"
}

func (s *Service) formatOn(ctx context.Context, format string) error {
	if !store.IsBookKind(format) {
		return errors.New("say whether it's for ebooks or audiobooks")
	}
	on, err := mediatypes.Enabled(ctx, s.Store, format)
	if err != nil {
		return err
	}
	if !on {
		return FormatOffError{format}
	}
	return nil
}

// Book searches for a book in a format (store.Ebook or store.Audiobook)
// and judges each release.
func (s *Service) Book(ctx context.Context, id int64, format string) (Results, error) {
	if err := s.formatOn(ctx, format); err != nil {
		return Results{}, err
	}
	b, err := s.Store.Book(ctx, id)
	if err != nil {
		return Results{}, err
	}
	if err := s.Store.CheckUnlocked(ctx, format, b.ID); err != nil {
		return Results{}, err
	}
	a, err := s.Store.Author(ctx, b.AuthorID)
	if err != nil {
		return Results{}, err
	}
	text := bookQuery(a, b)
	rs, notes, n, err := s.ask(ctx, func(ix store.Indexer) Query {
		return Query{Type: "search", Text: text, Cats: bookCats(ix, format)}
	})
	if err != nil {
		return Results{}, err
	}
	blocked, err := s.blocked(ctx, format, b.ID)
	if err != nil {
		return Results{}, err
	}
	out := Results{Errors: notes, Searched: n}
	for _, r := range rs {
		out.Releases = append(out.Releases, s.judgeBook(r, a, b, format, blocked))
	}
	s.rank(ctx, &out)
	s.markSearched(ctx, format, b.ID, out)
	s.logResults(fmt.Sprintf("%s by %s (%s)", b.Title, a.Name, formatNoun(format)), out)
	return out, nil
}

// judgeBook says whether a release is the book in the format wanted.
func (s *Service) judgeBook(r Release, a store.Author, b store.Book, format string, blocked map[string]bool) Candidate {
	c := Candidate{Release: r, Accepted: true, Key: key(r), kind: format, titleID: b.ID}
	c.Format, c.formatRank = bookFormat(r.Title, format)
	if r.Protocol != "usenet" {
		c.reject("torrent downloads aren't supported yet")
	}
	if blocked[strings.ToLower(r.Title)] {
		c.reject("it's on the blocklist")
	}
	if slices.Contains(r.Categories, catComics) || reComic.MatchString(r.Title) {
		c.reject("it's a comic")
	}
	if ok, why := bookMatches(r.Title, b.Title, a.Name); !ok {
		c.reject(why)
	}
	if reCollection.MatchString(r.Title) && !reCollection.MatchString(b.Title) {
		c.reject("it's a collection of books, not this one")
	}
	switch format {
	case store.Ebook:
		if f, _ := bookFormat(r.Title, store.Audiobook); f != "" || reAudiobook.MatchString(r.Title) || slices.Contains(r.Categories, catAudiobook) {
			c.reject("it's an audiobook")
		}
		if r.Size > maxEbookSize {
			c.reject(fmt.Sprintf("at %s it's too big to be one ebook", humanSize(r.Size)))
		}
	case store.Audiobook:
		if c.Format == "" {
			if f, _ := bookFormat(r.Title, store.Ebook); f != "" {
				c.reject("it's an ebook")
			}
		}
		if r.Size > 0 && r.Size < minAudiobookSize {
			c.reject(fmt.Sprintf("at %s it's too small to be an audiobook", humanSize(r.Size)))
		}
	}
	if b.File(format) != nil {
		c.reject(fmt.Sprintf("there's %s copy already", map[string]string{store.Ebook: "an ebook", store.Audiobook: "an audiobook"}[format]))
	}
	return c
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

// BookSubject is how a book is named in jobs and logs: "Dune by Frank
// Herbert (ebook)".
func BookSubject(a store.Author, b store.Book, format string) string {
	return fmt.Sprintf("%s by %s (%s)", b.Title, a.Name, formatNoun(format))
}

// BookJob is the payload of a search job for a book.
func BookJob(id int64, format string) string {
	p, _ := json.Marshal(BookPayload{ID: id, Format: format})
	return string(p)
}

func (s *Service) jobBook(ctx context.Context, payload string) (jobs.Result, error) {
	var p BookPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil || p.ID == 0 {
		return jobs.Result{}, errors.New("the job doesn't say which book")
	}
	res, err := s.Book(ctx, p.ID, p.Format)
	var off FormatOffError
	if errors.Is(err, ErrNoIndexers) || errors.Is(err, store.ErrLocked) || errors.As(err, &off) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, p.Format, p.ID, 0)
}

// wantedBooks are the books wanted in the formats turned on that have no
// copy yet and are out (Open Library gives a year, so this year's count).
func (s *Service) wantedBooks(ctx context.Context, year int) ([]Wanted, error) {
	var out []Wanted
	var authors map[int64]store.Author
	for _, format := range []string{store.Ebook, store.Audiobook} {
		if on, err := mediatypes.Enabled(ctx, s.Store, format); err != nil {
			return nil, err
		} else if !on {
			continue
		}
		list, err := s.Store.WantedBooks(ctx, format)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			continue
		}
		if authors == nil {
			as, err := s.Store.Authors(ctx)
			if err != nil {
				return nil, err
			}
			authors = map[int64]store.Author{}
			for _, a := range as {
				authors[a.ID] = a
			}
		}
		searched, err := s.Store.Searched(ctx, format)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			if b.Year > year {
				continue
			}
			out = append(out, Wanted{Kind: format, ID: b.ID, Subject: BookSubject(authors[b.AuthorID], b, format),
				Searched: searched[b.ID].At, Misses: searched[b.ID].Misses})
		}
	}
	return out, nil
}
