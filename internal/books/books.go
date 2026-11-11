// Package books is Box Office's list of books: authors from Open Library,
// the books they've written, and which of those are wanted as ebooks,
// audiobooks or both. Ebooks and audiobooks share the one list of authors,
// and each format works only while it's turned on in Settings. It never
// touches files.
package books

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/wikidata"
)

// ErrOff means neither ebooks nor audiobooks, or not the format asked
// about, is turned on in Settings.
type ErrOff struct{ Format string }

func (e ErrOff) Error() string {
	switch e.Format {
	case store.Ebook:
		return "turn on Ebooks in Settings first"
	case store.Audiobook:
		return "turn on Audiobooks in Settings first"
	}
	return "turn on Ebooks or Audiobooks in Settings first"
}

// Service is the list of books.
type Service struct {
	Store       *store.Store
	OpenLibrary *openlibrary.Client
	// Jobs runs a search for a book as soon as it's wanted, when set.
	Jobs *jobs.Runner
	// Wikidata fills in author photos and series Open Library doesn't
	// have, when set.
	Wikidata *wikidata.Client
	Log      *slog.Logger

	wakeOnce sync.Once
	wake     chan struct{}
}

func checkFormat(format string) error {
	if format != store.Ebook && format != store.Audiobook {
		return &catalog.Error{Message: "say whether it's for ebooks or audiobooks"}
	}
	return nil
}

// on checks a format is turned on; "" asks whether either is.
func (s *Service) on(ctx context.Context, format string) error {
	formats := []string{format}
	if format == "" {
		formats = []string{store.Ebook, store.Audiobook}
	}
	for _, f := range formats {
		on, err := mediatypes.Enabled(ctx, s.Store, f) // the formats are media type keys too
		if err != nil {
			return err
		}
		if on {
			return nil
		}
	}
	return ErrOff{format}
}

// LookupAuthor is an author found on Open Library, with their id here if
// they're on the list.
type LookupAuthor struct {
	openlibrary.Author
	ID int64 `json:"id,omitempty"`
}

// Lookup searches Open Library for authors.
func (s *Service) Lookup(ctx context.Context, term string) ([]LookupAuthor, error) {
	if err := s.on(ctx, ""); err != nil {
		return nil, err
	}
	if strings.TrimSpace(term) == "" {
		return []LookupAuthor{}, nil
	}
	found, err := s.OpenLibrary.SearchAuthors(ctx, term)
	if err != nil {
		return nil, err
	}
	out := make([]LookupAuthor, 0, len(found))
	for _, a := range found {
		l := LookupAuthor{Author: a}
		if have, err := s.Store.AuthorByKey(ctx, a.Key); err == nil {
			l.ID = have.ID
		}
		out = append(out, l)
	}
	return out, nil
}

// AddOptions are the choices made when adding an author.
type AddOptions struct {
	Key string `json:"key"`
	// Format is what the author's being added for: ebook or audiobook.
	Format string `json:"format"`
	// All wants every book already out in that format; otherwise they're
	// picked one by one.
	All bool `json:"all"`
	// New wants books that come out from now on, in that format.
	New bool `json:"new"`
}

// Add puts an author on the list and fetches their books.
func (s *Service) Add(ctx context.Context, o AddOptions) (store.Author, error) {
	if err := checkFormat(o.Format); err != nil {
		return store.Author{}, err
	}
	if err := s.on(ctx, o.Format); err != nil {
		return store.Author{}, err
	}
	if !openlibrary.IsAuthorKey(o.Key) {
		return store.Author{}, &catalog.Error{Message: "pick an author from the search"}
	}
	if _, err := s.Store.AuthorByKey(ctx, o.Key); err == nil {
		return store.Author{}, catalog.ErrExists
	}
	if folders, err := s.Store.RootFolders(ctx, o.Format); err != nil {
		return store.Author{}, err
	} else if len(folders) == 0 {
		return store.Author{}, &catalog.Error{Message: "add " + map[string]string{store.Ebook: "an ebook", store.Audiobook: "an audiobook"}[o.Format] +
			" root folder under Settings first"}
	}
	a, err := s.OpenLibrary.Author(ctx, o.Key)
	if errors.Is(err, openlibrary.ErrNotFound) {
		return store.Author{}, &catalog.Error{Message: "Open Library doesn't know that author"}
	}
	if err != nil {
		return store.Author{}, err
	}
	photo := ""
	if a.HasPhoto {
		photo = a.Photo
	}
	added, err := s.Store.AddAuthor(ctx, store.Author{OLKey: a.Key, Name: a.Name, Born: a.Born, Died: a.Died, Bio: a.Bio, Photo: photo, Wikidata: a.Wikidata,
		NewEbooks: o.Format == store.Ebook && o.New, NewAudiobooks: o.Format == store.Audiobook && o.New})
	if errors.Is(err, store.ErrExists) {
		return store.Author{}, catalog.ErrExists
	}
	if err != nil {
		return store.Author{}, err
	}
	// The photo from Wikidata, when Open Library has none, and the series
	// come in the background.
	defer s.kick()
	if err := s.refreshBooks(ctx, added, o.All && o.Format == store.Ebook, o.All && o.Format == store.Audiobook); err != nil {
		return added, err
	}
	// Books already in the root folders count as there, not missing.
	if _, err := s.FindOnDisk(ctx, added.ID); err != nil {
		return added, err
	}
	return s.Store.Author(ctx, added.ID)
}

// Refresh brings an author's details and books up to date from Open
// Library. New books are wanted in the formats the author's new books are
// wanted in; the rest keep their choices.
func (s *Service) Refresh(ctx context.Context, id int64) (store.Author, error) {
	if err := s.on(ctx, ""); err != nil {
		return store.Author{}, err
	}
	have, err := s.Store.Author(ctx, id)
	if err != nil {
		return store.Author{}, err
	}
	a, err := s.OpenLibrary.Author(ctx, have.OLKey)
	if err != nil {
		return store.Author{}, err
	}
	have.Name, have.Born, have.Died, have.Bio = a.Name, a.Born, a.Died, a.Bio
	have.SortName = store.SortTitle(a.Name)
	if err := s.Store.UpdateAuthor(ctx, have); err != nil {
		return store.Author{}, err
	}
	// The photo and series are looked up again in the background, keeping
	// Open Library's photo, or the one there was, till then.
	photo := have.Photo
	if a.HasPhoto {
		photo = a.Photo
	}
	if err := s.Store.RedoAuthorDetails(ctx, id, photo); err != nil {
		return store.Author{}, err
	}
	defer s.kick()
	if err := s.refreshBooks(ctx, have, have.NewEbooks, have.NewAudiobooks); err != nil {
		return store.Author{}, err
	}
	if _, err := s.FindOnDisk(ctx, id); err != nil {
		return store.Author{}, err
	}
	return s.Store.Author(ctx, id)
}

// refreshBooks fetches an author's books; books not on the list yet are
// wanted as an ebook and as an audiobook as given.
func (s *Service) refreshBooks(ctx context.Context, a store.Author, ebook, audiobook bool) error {
	found, err := s.OpenLibrary.Books(ctx, a.OLKey)
	if err != nil {
		return err
	}
	for _, b := range found {
		if _, err := s.Store.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: b.Key, Title: b.Title, Subtitle: b.Subtitle, Year: b.Year,
			CoverID: b.CoverID, Editions: b.Editions, WantEbook: ebook, WantAudiobook: audiobook}); err != nil {
			return err
		}
	}
	return s.Store.AuthorRefreshed(ctx, a.ID)
}

// Edit is a change to an author; nil fields stay as they are.
type Edit struct {
	NewEbooks     *bool `json:"newEbooks"`
	NewAudiobooks *bool `json:"newAudiobooks"`
	// Locked is Do not touch: no searches, downloads or file changes.
	Locked *bool `json:"locked"`
}

// Update changes an author.
func (s *Service) Update(ctx context.Context, id int64, e Edit) (store.Author, error) {
	if e.NewEbooks != nil {
		if err := s.on(ctx, store.Ebook); err != nil {
			return store.Author{}, err
		}
	}
	if e.NewAudiobooks != nil {
		if err := s.on(ctx, store.Audiobook); err != nil {
			return store.Author{}, err
		}
	}
	a, err := s.Store.Author(ctx, id)
	if err != nil {
		return store.Author{}, err
	}
	if e.NewEbooks != nil {
		a.NewEbooks = *e.NewEbooks
	}
	if e.NewAudiobooks != nil {
		a.NewAudiobooks = *e.NewAudiobooks
	}
	if e.Locked != nil {
		a.Locked = *e.Locked
	}
	if err := s.Store.UpdateAuthor(ctx, a); err != nil {
		return store.Author{}, err
	}
	return s.Store.Author(ctx, id)
}

// Want says whether a book is wanted in a format.
func (s *Service) Want(ctx context.Context, bookID int64, format string, on bool) (store.Book, error) {
	if err := checkFormat(format); err != nil {
		return store.Book{}, err
	}
	if err := s.on(ctx, format); err != nil {
		return store.Book{}, err
	}
	if err := s.Store.SetBookWanted(ctx, bookID, format, on); err != nil {
		return store.Book{}, err
	}
	b, err := s.Store.Book(ctx, bookID)
	if err == nil && on {
		s.searchSoon(ctx, b, format)
	}
	return b, err
}

// searchSoon queues a search for a book just wanted, when Box Office can
// download it: downloading is on, the author isn't on Do not touch, and
// there's no copy yet. Otherwise the hourly search gets to it in turn.
func (s *Service) searchSoon(ctx context.Context, b store.Book, format string) {
	if s.Jobs == nil || b.File(format) != nil {
		return
	}
	if set, err := downloader.LoadSettings(ctx, s.Store); err != nil || !set.Enabled {
		return
	}
	a, err := s.Store.Author(ctx, b.AuthorID)
	if err != nil || a.Locked {
		return
	}
	s.Jobs.Queue(ctx, search.JobSearchBook, search.BookSubject(a, b, format), search.BookJob(b.ID, format))
}

// Search queues a search for a book in a format now, whether or not it's
// wanted, and grabs the best release it finds.
func (s *Service) Search(ctx context.Context, bookID int64, format string) error {
	if err := checkFormat(format); err != nil {
		return err
	}
	if err := s.on(ctx, format); err != nil {
		return err
	}
	if err := s.Store.CheckUnlocked(ctx, format, bookID); err != nil {
		return err
	}
	b, err := s.Store.Book(ctx, bookID)
	if err != nil {
		return err
	}
	a, err := s.Store.Author(ctx, b.AuthorID)
	if err != nil {
		return err
	}
	if s.Jobs == nil {
		return errors.New("searching isn't set up")
	}
	_, err = s.Jobs.Queue(ctx, search.JobSearchBook, search.BookSubject(a, b, format), search.BookJob(b.ID, format))
	return err
}

// WantAll says whether every one of an author's books is wanted in a
// format.
func (s *Service) WantAll(ctx context.Context, authorID int64, format string, on bool) error {
	if err := checkFormat(format); err != nil {
		return err
	}
	if err := s.on(ctx, format); err != nil {
		return err
	}
	if _, err := s.Store.Author(ctx, authorID); err != nil {
		return err
	}
	return s.Store.SetAuthorBooksWanted(ctx, authorID, format, on)
}

// WantSeries says whether every book of one of an author's series is
// wanted in a format.
func (s *Service) WantSeries(ctx context.Context, authorID int64, series, format string, on bool) error {
	if err := checkFormat(format); err != nil {
		return err
	}
	if err := s.on(ctx, format); err != nil {
		return err
	}
	if strings.TrimSpace(series) == "" {
		return &catalog.Error{Message: "say which series"}
	}
	if _, err := s.Store.Author(ctx, authorID); err != nil {
		return err
	}
	n, err := s.Store.SetSeriesWanted(ctx, authorID, series, format, on)
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
