package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/openlibrary"
)

func fakeOpenLibrary(t *testing.T) *openlibrary.Client {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/authors.json":
			fmt.Fprint(w, `{"docs":[{"key":"OL1394865A","name":"Brandon Sanderson","top_work":"Mistborn","work_count":3}]}`)
		case "/authors/OL1394865A.json":
			fmt.Fprint(w, `{"name":"Brandon Sanderson"}`)
		case "/search.json":
			fmt.Fprint(w, `{"numFound":3,"docs":[
				{"key":"/works/OL1W","title":"Mistborn","first_publish_year":2006,"cover_i":7,"edition_count":90},
				{"key":"/works/OL2W","title":"Elantris","first_publish_year":2005,"edition_count":60},
				{"key":"/works/OL3W","title":"Warbreaker","first_publish_year":2009,"edition_count":40}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	c := openlibrary.New("test")
	c.Base, c.Spacing = hs.URL, time.Millisecond
	return c
}

type authorOut struct {
	ID               int64 `json:"id"`
	Books            int   `json:"books"`
	WantedEbooks     int   `json:"wantedEbooks"`
	WantedAudiobooks int   `json:"wantedAudiobooks"`
	NewAudiobooks    bool  `json:"newAudiobooks"`
	BookList         []struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		Cover         string `json:"cover"`
		WantEbook     bool   `json:"wantEbook"`
		WantAudiobook bool   `json:"wantAudiobook"`
	} `json:"bookList"`
}

// Ebooks and audiobooks share one list of authors; each format needs its
// switch on and a root folder, and each book is wanted per format.
func TestAuthorsAndBooks(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Catalog = &catalog.Service{Store: s.Store}
	s.Books = &books.Service{Store: s.Store, OpenLibrary: fakeOpenLibrary(t)}
	add := `{"key":"OL1394865A","format":"audiobook","all":true,"new":true}`
	if code := call(t, hs, "GET", "/api/v1/lookup/authors?q=sanderson", "", nil); code != 409 {
		t.Fatalf("lookup with books off: %d", code)
	}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"audiobook":true}}`, nil)
	if code := call(t, hs, "POST", "/api/v1/authors", add, nil); code != 400 {
		t.Fatalf("add with no audiobook folder: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"audiobook","path":"`+t.TempDir()+`"}`, nil); code != 201 {
		t.Fatal(code)
	}
	if code := call(t, hs, "POST", "/api/v1/authors", `{"key":"OL1394865A","format":"ebook"}`, nil); code != 409 {
		t.Fatalf("add for ebooks while they're off: %d", code)
	}
	var a authorOut
	if code := call(t, hs, "POST", "/api/v1/authors", add, &a); code != 201 {
		t.Fatal(code)
	}
	if a.Books != 3 || a.WantedAudiobooks != 3 || a.WantedEbooks != 0 || !a.NewAudiobooks {
		t.Fatalf("added %+v", a)
	}
	if code := call(t, hs, "POST", "/api/v1/authors", add, nil); code != 409 {
		t.Fatalf("added twice: %d", code)
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/authors/%d", a.ID), "", &a)
	if a.BookList[0].Title != "Mistborn" || a.BookList[0].Cover != "https://covers.openlibrary.org/b/id/7-M.jpg" {
		t.Fatalf("books %+v", a.BookList)
	}
	elantris := a.BookList[1].ID
	if code := call(t, hs, "PUT", fmt.Sprintf("/api/v1/books/%d/wanted", elantris), `{"format":"audiobook","wanted":false}`, nil); code != 200 {
		t.Fatal(code)
	}
	if code := call(t, hs, "PUT", fmt.Sprintf("/api/v1/books/%d/wanted", elantris), `{"format":"ebook","wanted":true}`, nil); code != 409 {
		t.Fatalf("an ebook while ebooks are off: %d", code)
	}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"ebook":true}}`, nil)
	if code := call(t, hs, "PUT", fmt.Sprintf("/api/v1/authors/%d/wanted", a.ID), `{"format":"ebook","wanted":true}`, &a); code != 200 || a.WantedEbooks != 3 {
		t.Fatalf("want every ebook: %d %+v", code, a)
	}
	// A refresh keeps the choices made.
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/authors/%d/refresh", a.ID), "", &a); code != 200 || a.WantedAudiobooks != 2 || a.WantedEbooks != 3 {
		t.Fatalf("refresh: %d %+v", code, a)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/authors/%d", a.ID), "", nil); code != 204 {
		t.Fatal(code)
	}
}
