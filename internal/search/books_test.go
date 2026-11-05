package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

func bookItem(title, guid string, size int64, cat int) string {
	return strings.Replace(item(title, guid, size), `value="2040"`, fmt.Sprintf(`value="%d"`, cat), 1)
}

func bookSetup(t *testing.T, srvURL string) (*Service, *store.Store, store.Author, store.Book) {
	s, st, _ := setup(t, srvURL)
	ctx := context.Background()
	a, err := st.AddAuthor(ctx, store.Author{OLKey: "OL1394865A", Name: "Brandon Sanderson"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: "/works/OL1W", Title: "Mistborn: The Final Empire", Year: 2006, WantEbook: true, WantAudiobook: true}); err != nil {
		t.Fatal(err)
	}
	bs, _ := st.Books(ctx, a.ID)
	return s, st, a, bs[0]
}

func TestBookSearch(t *testing.T) {
	srv, asked := indexer(t,
		bookItem("Brandon Sanderson - Mistborn - The Final Empire (2006) [PDF]", "pdf", 4e6, 7020),
		bookItem("Brandon Sanderson - Mistborn - The Final Empire (2006) [EPUB]", "epub", 2e6, 7020),
		bookItem("Brandon.Sanderson.Mistborn.Unabridged.M4B", "m4b", 6e8, 3030),
		bookItem("Brandon Sanderson - Mistborn Trilogy Collection EPUB", "coll", 9e6, 7020),
		bookItem("Brandon Sanderson - Elantris (2005) [EPUB]", "other", 2e6, 7020),
		bookItem("Mistborn - Comics CBR", "comic", 9e7, 7030),
		bookItem("Mistborn EPUB", "noauthor", 2e6, 7020),
	)
	s, st, a, b := bookSetup(t, srv.URL)
	ctx := context.Background()

	if _, err := s.Book(ctx, b.ID, store.Ebook); !errors.As(err, new(FormatOffError)) {
		t.Fatalf("searched with ebooks off: %v", err)
	}
	mediatypes.SetEnabled(ctx, st, store.Ebook, true)
	mediatypes.SetEnabled(ctx, st, store.Audiobook, true)
	res, err := s.Book(ctx, b.ID, store.Ebook)
	if err != nil {
		t.Fatal(err)
	}
	q := (*asked)[len(*asked)-1]
	if !strings.Contains(q, "t=search") || !strings.Contains(q, "cat=7020") || !strings.Contains(q, "q=brandon+sanderson+mistborn") {
		t.Fatalf("asked %s", q)
	}
	if got := res.Releases[0]; got.GUID != "epub" || !got.Accepted || got.Format != "EPUB" {
		t.Fatalf("best %+v", got)
	}
	for _, c := range res.Releases {
		want := c.GUID == "epub" || c.GUID == "pdf"
		if c.Accepted != want {
			t.Errorf("%s accepted %v: %v", c.Title, c.Accepted, c.Rejections)
		}
	}

	// An audiobook search asks the audiobook category and takes the M4B.
	res, err = s.Book(ctx, b.ID, store.Audiobook)
	if err != nil {
		t.Fatal(err)
	}
	if q := (*asked)[len(*asked)-1]; !strings.Contains(q, "cat=3030") {
		t.Fatalf("asked %s", q)
	}
	if got := res.Releases[0]; got.GUID != "m4b" || !got.Accepted {
		t.Fatalf("best audiobook %+v", got)
	}

	// The job grabs the best, once downloading is on.
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	r, err := s.jobBook(ctx, BookJob(b.ID, store.Ebook))
	if err != nil || r.Status != "done" || !strings.Contains(r.Message, "grabbed Brandon Sanderson - Mistborn - The Final Empire (2006) [EPUB]") {
		t.Fatalf("job %+v %v", r, err)
	}
	pending, _ := st.Pending(ctx, store.Ebook, b.ID)
	if len(pending) != 1 || pending[0].Kind != store.Ebook || pending[0].TitleID != b.ID {
		t.Fatalf("pending %+v", pending)
	}
	if r, _ := s.jobBook(ctx, BookJob(b.ID, store.Ebook)); r.Status != "skipped" {
		t.Fatalf("grabbed twice: %+v", r)
	}

	// Do not touch: no searches.
	a.Locked = true
	st.UpdateAuthor(ctx, a)
	if _, err := s.Book(ctx, b.ID, store.Ebook); !errors.Is(err, store.ErrLocked) || !errors.Is(err, store.ErrAuthorLocked) {
		t.Fatalf("searched a locked author: %v", err)
	}
	if r, _ := s.jobBook(ctx, BookJob(b.ID, store.Audiobook)); r.Status != "skipped" {
		t.Fatalf("job for a locked author: %+v", r)
	}
}

func TestWantedBooks(t *testing.T) {
	srv, _ := indexer(t)
	s, st, a, b := bookSetup(t, srv.URL)
	ctx := context.Background()
	st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: "/works/OL9W", Title: "Not Out Yet", Year: 2999, WantEbook: true})
	count := func() (ebooks, audiobooks int) {
		list, err := s.WantedList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range list {
			switch w.Kind {
			case store.Ebook:
				ebooks++
			case store.Audiobook:
				audiobooks++
			}
		}
		return
	}
	if e, au := count(); e+au != 0 {
		t.Fatalf("books wanted while their switches are off: %d %d", e, au)
	}
	mediatypes.SetEnabled(ctx, st, store.Ebook, true)
	if e, au := count(); e != 1 || au != 0 {
		t.Fatalf("wanted %d ebooks, %d audiobooks", e, au)
	}
	st.AddFile(ctx, store.File{Kind: store.Ebook, OwnerID: b.ID, Path: "/books/x.epub"})
	if e, _ := count(); e != 0 {
		t.Fatal("a book with a copy is still wanted")
	}
}

func TestBookMatches(t *testing.T) {
	for _, c := range []struct {
		release, title, author string
		ok                     bool
	}{
		{"Brandon.Sanderson-The.Way.of.Kings.2010.RETAIL.EPUB-GRP", "The Way of Kings", "Brandon Sanderson", true},
		{"Sanderson, Brandon - Way of Kings (epub)", "The Way of Kings", "Brandon Sanderson", true},
		{"Brandon Sanderson - Words of Radiance EPUB", "The Way of Kings", "Brandon Sanderson", false},
		{"Kings Way of the Brandon Sanderson", "The Way of Kings", "Brandon Sanderson", false},
		{"Frank Herbert - Dune Messiah", "Dune", "Frank Herbert", true}, // the year and the rest of the name sort it out
		{"Dune - Brian Herbert", "Dune", "Frank Herbert", true},
		{"Dune - Someone Else", "Dune", "Frank Herbert", false},
	} {
		if ok, _ := bookMatches(c.release, c.title, c.author); ok != c.ok {
			t.Errorf("%q for %q by %s: %v", c.release, c.title, c.author, ok)
		}
	}
}
