package books

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/wikidata"
)

// Tolkien: Open Library has a photo and puts two of the three Lord of the
// Rings books in their series; Wikidata knows the third, and the one-volume
// Lord of the Rings goes with them by its name. The Hobbit is on its own.
func TestDetails(t *testing.T) {
	ol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authors/OL26320A.json":
			fmt.Fprint(w, `{"name":"J.R.R. Tolkien","photos":[-1,6155606],"remote_ids":{"wikidata":"Q892"}}`)
		case "/works/OL27513W.json":
			fmt.Fprint(w, `{"title":"The Fellowship of the Ring","series":[{"series":{"key":"/series/OL330052L"},"position":"1"}]}`)
		case "/works/OL27479W.json":
			fmt.Fprint(w, `{"title":"The Two Towers","series":[{"series":{"key":"/series/OL330052L"},"position":"2"}]}`)
		case "/series/OL330052L.json":
			fmt.Fprint(w, `{"name":"The Lord of the Rings"}`)
		case "/works/OL27448W.json", "/works/OL27516W.json", "/works/OL262758W.json":
			fmt.Fprint(w, `{"title":"x"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ol.Close()
	wd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "wd:Q892") || !strings.HasPrefix(r.Header.Get("User-Agent"), "BoxOffice/") {
			t.Errorf("query %q", q)
		}
		const e = "http://www.wikidata.org/entity/"
		row := func(work, title, series, name, ord, olid string) string {
			s := fmt.Sprintf(`{"work":{"value":"%s%s"},"workLabel":{"value":%q},"series":{"value":"%s%s"},"seriesLabel":{"value":%q}`, e, work, title, e, series, name)
			if ord != "" {
				s += fmt.Sprintf(`,"ord":{"value":%q}`, ord)
			}
			if olid != "" {
				s += fmt.Sprintf(`,"olid":{"value":%q}`, olid)
			}
			return s + "}"
		}
		fmt.Fprintf(w, `{"results":{"bindings":[%s]}}`, strings.Join([]string{
			row("Q220306", "The Return of the King", "Q15228", "The Lord of the Rings", "3", ""),
			row("Q220306", "The Return of the King", "Q81738", "Middle-earth legendarium", "", ""),
			row("Q74287", "The Hobbit", "Q81738", "Middle-earth legendarium", "", "OL262758W"),
			row("Q1", "A Book Not Listed", "Q2", "Other", "1", ""),
		}, ","))
	}))
	defer wd.Close()

	st := storetest.Open(t)
	ctx := context.Background()
	mediatypes.SetEnabled(ctx, st, store.Ebook, true)
	a, _ := st.AddAuthor(ctx, store.Author{OLKey: "OL26320A", Name: "J.R.R. Tolkien"})
	for i, b := range []struct{ key, title string }{
		{"OL27448W", "The Lord of the Rings"}, {"OL262758W", "The Hobbit"}, {"OL27513W", "The Fellowship of the Ring"},
		{"OL27479W", "The Two Towers"}, {"OL27516W", "The Return of the King"},
	} {
		st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: b.key, Title: b.title, Editions: 100 - i})
	}
	olc := openlibrary.New("test")
	olc.Base, olc.Spacing = ol.URL, time.Millisecond
	wdc := wikidata.New("test")
	wdc.Base, wdc.Spacing = wd.URL, time.Millisecond
	s := &Service{Store: st, OpenLibrary: olc, Wikidata: wdc}

	if err := s.Details(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Author(ctx, a.ID)
	if got.Photo != "https://covers.openlibrary.org/a/id/6155606-M.jpg" || got.Wikidata != "Q892" || got.DetailsAt.IsZero() {
		t.Fatalf("author %+v", got)
	}
	bs, _ := st.Books(ctx, a.ID)
	want := map[string]string{
		"The Lord of the Rings":      "The Lord of the Rings ",
		"The Hobbit":                 "Middle-earth legendarium ",
		"The Fellowship of the Ring": "The Lord of the Rings 1",
		"The Two Towers":             "The Lord of the Rings 2",
		"The Return of the King":     "The Lord of the Rings 3",
	}
	for _, b := range bs {
		if g := b.Series + " " + b.SeriesPosition; g != want[b.Title] {
			t.Errorf("%s: %q, want %q", b.Title, g, want[b.Title])
		}
	}
	if left, _ := st.AuthorsToDetail(ctx, time.Now().Add(-time.Hour)); len(left) != 0 {
		t.Fatalf("still to look up: %+v", left)
	}

	// Wanting a series wants just its books.
	if err := s.WantSeries(ctx, a.ID, "The Lord of the Rings", store.Ebook, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Author(ctx, a.ID); got.WantedEbooks != 4 {
		t.Fatalf("wanted %d", got.WantedEbooks)
	}

	// With no photo on Open Library, Wikimedia Commons has one.
	ol2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authors/OL26320A.json" {
			fmt.Fprint(w, `{"name":"J.R.R. Tolkien","remote_ids":{"wikidata":"Q892"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ol2.Close()
	wd2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "P18") {
			fmt.Fprint(w, `{"results":{"bindings":[{"img":{"value":"http://commons.wikimedia.org/wiki/Special:FilePath/J.%20R.%20R.%20Tolkien%2C%201940s.jpg"}}]}}`)
			return
		}
		fmt.Fprint(w, `{"results":{"bindings":[]}}`)
	}))
	defer wd2.Close()
	olc.Base, wdc.Base = ol2.URL, wd2.URL
	if err := s.Details(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Author(ctx, a.ID)
	if got.Photo != "https://commons.wikimedia.org/wiki/Special:FilePath/J.%20R.%20R.%20Tolkien%2C%201940s.jpg?width=300" {
		t.Fatalf("photo %q", got.Photo)
	}
}
