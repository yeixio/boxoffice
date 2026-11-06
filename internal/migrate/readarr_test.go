package migrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/store"
)

func jsonHandler(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(v) }
}

// fakeOpenLibrary knows Ursula K. Le Guin and Andy Weir, and two books
// of each.
func fakeOpenLibrary(t *testing.T) *httptest.Server {
	t.Helper()
	works := map[string][]map[string]any{
		"OL31353A": {{"key": "/works/OL59863W", "title": "A Wizard of Earthsea", "first_publish_year": 1968, "edition_count": 90},
			{"key": "/works/OL59864W", "title": "The Left Hand of Darkness", "first_publish_year": 1969, "edition_count": 80}},
		"OL7234434A": {{"key": "/works/OL17091839W", "title": "Project Hail Mary", "first_publish_year": 2021, "edition_count": 40},
			{"key": "/works/OL17091840W", "title": "The Martian", "first_publish_year": 2011, "edition_count": 60}},
	}
	names := map[string]string{"OL31353A": "Ursula K. Le Guin", "OL7234434A": "Andy Weir"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search/authors.json", func(w http.ResponseWriter, r *http.Request) {
		docs := []map[string]any{}
		for key, name := range names {
			if strings.EqualFold(r.URL.Query().Get("q"), name) {
				docs = append(docs, map[string]any{"key": key, "name": name, "work_count": 10},
					map[string]any{"key": "OL9A", "name": name + " Society", "work_count": 1})
			}
		}
		jsonHandler(map[string]any{"docs": docs})(w, r)
	})
	mux.HandleFunc("GET /authors/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(r.PathValue("key"), ".json")
		jsonHandler(map[string]any{"name": names[key]})(w, r)
	})
	mux.HandleFunc("GET /search.json", func(w http.ResponseWriter, r *http.Request) {
		list := works[strings.TrimPrefix(r.URL.Query().Get("q"), "author_key:")]
		jsonHandler(map[string]any{"numFound": len(list), "docs": list})(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// fakeReadarr has ebooks in /books and audiobooks in /audiobooks, as
// Readarr lists them without saying which is which.
func fakeReadarr(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/system/status", jsonHandler(map[string]any{"appName": "Readarr", "version": "0.4.19.2811"}))
	mux.HandleFunc("GET /api/v1/rootfolder", jsonHandler([]map[string]any{{"path": "/books"}, {"path": "/audiobooks"}}))
	mux.HandleFunc("GET /api/v1/author", jsonHandler([]map[string]any{
		{"id": 1, "authorName": "Ursula K. Le Guin", "path": "/books/Ursula K. Le Guin", "rootFolderPath": "/books", "monitored": true,
			"monitorNewItems": "all", "statistics": map[string]any{"bookFileCount": 2}},
		{"id": 2, "authorName": "Andy Weir", "path": "/audiobooks/Andy Weir", "rootFolderPath": "/audiobooks", "monitored": true,
			"monitorNewItems": "none", "statistics": map[string]any{"bookFileCount": 2}},
		{"id": 3, "authorName": "Nobody Known", "path": "/books/Nobody Known", "rootFolderPath": "/books"},
	}))
	mux.HandleFunc("GET /api/v1/book", jsonHandler([]map[string]any{
		{"id": 10, "authorId": 1, "title": "A Wizard of Earthsea", "monitored": true},
		{"id": 11, "authorId": 1, "title": "The Left Hand of Darkness", "monitored": true},
		{"id": 12, "authorId": 1, "title": "A Readarr-Only Anthology", "monitored": true},
		{"id": 20, "authorId": 2, "title": "Project Hail Mary", "monitored": true},
		{"id": 21, "authorId": 2, "title": "The Martian", "monitored": false},
	}))
	mux.HandleFunc("GET /api/v1/bookfile", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("authorId") {
		case "1":
			jsonHandler([]map[string]any{
				{"bookId": 10, "path": "/books/Ursula K. Le Guin/A Wizard of Earthsea (1968)/A Wizard of Earthsea.mobi", "size": 10},
				{"bookId": 10, "path": "/books/Ursula K. Le Guin/A Wizard of Earthsea (1968)/A Wizard of Earthsea.epub", "size": 20},
			})(w, r)
		case "2":
			jsonHandler([]map[string]any{
				{"bookId": 20, "path": "/audiobooks/Andy Weir/Project Hail Mary (2021)/Part 1.m4b", "size": 100},
				{"bookId": 20, "path": "/audiobooks/Andy Weir/Project Hail Mary (2021)/Part 2.m4b", "size": 200},
			})(w, r)
		default:
			jsonHandler([]any{})(w, r)
		}
	})
	mux.HandleFunc("GET /api/v1/indexer", jsonHandler([]map[string]any{}))
	mux.HandleFunc("GET /api/v1/downloadclient", jsonHandler([]map[string]any{}))
	mux.HandleFunc("GET /api/v1/blocklist", jsonHandler(map[string]any{"totalRecords": 1, "records": []map[string]any{
		{"bookIds": []int{20}, "sourceTitle": "Andy.Weir.Project.Hail.Mary.MP3", "quality": map[string]any{"quality": map[string]any{"name": "MP3"}},
			"date": "2024-01-02T03:04:05Z", "protocol": "usenet", "indexer": "NZBgeek", "message": "bad"}}}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != legacyKey {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			t.Errorf("the migration changed something in Readarr: %s %s", r.Method, r.URL.Path)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestMigrateReadarr(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ol := fakeOpenLibrary(t)
	w.m.Books = &books.Service{Store: w.st, OpenLibrary: &openlibrary.Client{Base: ol.URL, HTTP: http.DefaultClient}}
	ebooks, audio := filepath.Join(w.media, "books"), filepath.Join(w.media, "audiobooks")
	files := []string{
		filepath.Join(ebooks, "Ursula K. Le Guin/A Wizard of Earthsea (1968)/A Wizard of Earthsea.mobi"),
		filepath.Join(ebooks, "Ursula K. Le Guin/A Wizard of Earthsea (1968)/A Wizard of Earthsea.epub"),
		filepath.Join(audio, "Andy Weir/Project Hail Mary (2021)/Part 1.m4b"),
		filepath.Join(audio, "Andy Weir/Project Hail Mary (2021)/Part 2.m4b"),
	}
	for _, f := range files {
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("book"), 0o644)
	}
	ts := fakeReadarr(t)
	req := Request{App: "readarr", URL: ts.URL, APIKey: legacyKey, PathMap: []PathMap{{From: "/books", To: ebooks}, {From: "/audiobooks", To: audio}}}

	if _, err := w.m.Preview(ctx, req); err == nil || !strings.Contains(err.Error(), "Ebooks or Audiobooks") {
		t.Fatalf("with books off: %v", err)
	}
	mediatypes.SetEnabled(ctx, w.st, store.Ebook, true)
	plan, err := w.m.Preview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// Audiobooks are off, so Andy Weir waits for them.
	if plan.Titles.Total != 3 || plan.Titles.New != 2 || plan.Titles.Skipped != 1 || !slicesContainsSub(plan.Warnings, "audiobooks are off") {
		t.Fatalf("plan with audiobooks off: %+v %v", plan.Titles, plan.Warnings)
	}
	mediatypes.SetEnabled(ctx, w.st, store.Audiobook, true)
	plan, err = w.m.Preview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Titles.New != 3 || len(plan.Profiles) != 0 || len(plan.RootFolders) != 2 ||
		plan.RootFolders[0].Kind != store.Ebook || plan.RootFolders[1].Kind != store.Audiobook {
		t.Fatalf("plan: %+v %+v", plan.Titles, plan.RootFolders)
	}

	id, err := w.m.Run(ctx, plan.Token, Options{})
	if err != nil {
		t.Fatal(err)
	}
	pr := waitDone(t, w.m, id)
	if pr.Status != "done" {
		t.Fatalf("migration: %+v", pr)
	}
	if pr.Imported.New != 2 || pr.Imported.Skipped != 1 || pr.Files != 2 || !slicesContainsSub(pr.Warnings, "doesn't list 1 book") {
		t.Fatalf("report: %+v", pr)
	}
	if f, _ := w.st.RootFolders(ctx, store.Audiobook); len(f) != 1 || f[0].Path != audio {
		t.Fatalf("audiobook root folders: %+v", f)
	}
	le, err := w.st.AuthorByKey(ctx, "OL31353A")
	if err != nil || !le.NewEbooks || le.NewAudiobooks {
		t.Fatalf("Le Guin: %+v %v", le, err)
	}
	got := map[string]store.Book{}
	for _, k := range []string{"OL31353A", "OL7234434A"} {
		a, _ := w.st.AuthorByKey(ctx, k)
		list, _ := w.st.Books(ctx, a.ID)
		for _, b := range list {
			got[b.Title] = b
		}
	}
	if b := got["A Wizard of Earthsea"]; b.EbookFile == nil || !strings.HasSuffix(b.EbookFile.Path, ".epub") || !b.WantEbook || b.WantAudiobook {
		t.Fatalf("Earthsea: %+v %+v", b, b.EbookFile)
	}
	if b := got["The Left Hand of Darkness"]; b.EbookFile != nil || !b.WantEbook {
		t.Fatalf("Left Hand, wanted: %+v", b)
	}
	if b := got["Project Hail Mary"]; b.AudiobookFile == nil || b.AudiobookFile.Path != filepath.Join(audio, "Andy Weir/Project Hail Mary (2021)") || b.WantEbook {
		t.Fatalf("Hail Mary: %+v %+v", b, b.AudiobookFile)
	}
	if b := got["The Martian"]; b.WantAudiobook || b.WantEbook {
		t.Fatalf("The Martian isn't wanted in Readarr: %+v", b)
	}
	if bl, _ := w.st.BlockedFor(ctx, store.Audiobook, got["Project Hail Mary"].ID); len(bl) != 1 {
		t.Fatalf("Hail Mary's blocklist: %+v", bl)
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
