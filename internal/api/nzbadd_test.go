package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/importer"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

const testNZB = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` +
	`<file subject="&quot;grp-x.part01.rar&quot; yEnc (1/1)"><segments><segment bytes="1000" number="1">a@b</segment></segments></file>` +
	`<file subject="&quot;grp-x.par2&quot; yEnc (1/1)"><segments><segment bytes="10" number="1">c@d</segment></segments></file></nzb>`

// An NZB is checked, from a file or a link, then added: to be filed once
// it's downloaded, or for a title.
func TestAddNZBByHand(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: t.TempDir(), Log: s.Log}
	fake := tmdbtest.New(t)
	cat := &catalog.Service{Store: s.Store, Log: s.Log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	s.Imports = &importer.Service{Store: s.Store, Downloads: s.Downloads, Catalog: cat, Scan: &libscan.Service{Store: s.Store, Catalog: cat, Log: s.Log}, Log: s.Log}

	send := func(path, body, contentType string, out any) int {
		req, _ := http.NewRequest("POST", hs.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("X-Api-Key", "key")
		req.Header.Set("Content-Type", contentType)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			json.NewDecoder(res.Body).Decode(out)
		}
		return res.StatusCode
	}
	var c NZBCheck
	if code := send("/api/v1/downloads/nzb/check?name="+url.QueryEscape("Some.Show.S02.1080p.WEB-DL.nzb"), testNZB, "application/x-nzb", &c); code != 200 {
		t.Fatalf("check %d", code)
	}
	// Par2 files aren't counted as what's in it.
	if c.Name != "Some.Show.S02.1080p.WEB-DL" || c.Files != 2 || c.Size == 0 || c.Guess.Kind != importer.Series || c.Token == "" || len(c.Kinds) < 2 {
		t.Fatalf("%+v", c)
	}
	if code := send("/api/v1/downloads/nzb/check", "not an nzb", "application/x-nzb", nil); code != 400 {
		t.Fatalf("took junk: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", `{"token":"`+c.Token+`","fileAs":"song"}`, nil); code != 400 {
		t.Fatalf("filed as a song: %d", code)
	}
	var d store.Download
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", `{"token":"`+c.Token+`","fileAs":"auto"}`, &d); code != 201 {
		t.Fatalf("add %d", code)
	}
	if d.FileAs != "auto" || d.Kind != "" || d.Name != "Some.Show.S02.1080p.WEB-DL" || d.Source != "upload" {
		t.Fatalf("%+v", d)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", `{"token":"`+c.Token+`"}`, nil); code != 400 {
		t.Fatalf("added twice: %d", code)
	}

	// A link to an indexer Box Office has is given its API key; a gzipped
	// NZB is unpacked, and named from what the indexer calls it.
	var asked url.Values
	ix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		if r.URL.Path == "/gone" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="The.Film.2001.1080p.nzb.gz"`)
		zw := gzip.NewWriter(w)
		zw.Write([]byte(testNZB))
		zw.Close()
	}))
	defer ix.Close()
	if _, err := s.Store.CreateIndexer(ctx, store.Indexer{Name: "ix", Protocol: "usenet", Kind: "newznab", URL: ix.URL, APIKey: "sekrit", EnableSearch: true}); err != nil {
		t.Fatal(err)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/check", `{"url":"`+ix.URL+`/getnzb/abc"}`, &c); code != 200 {
		t.Fatalf("link %d", code)
	}
	if asked.Get("apikey") != "sekrit" || c.Name != "The.Film.2001.1080p" || c.Guess.Kind != importer.Movie {
		t.Fatalf("%v %+v", asked, c)
	}
	var e struct{ Error string }
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/check", `{"url":"`+ix.URL+`/gone?apikey=x"}`, &e); code != 400 || asked.Get("apikey") != "x" || e.Error == "" {
		t.Fatalf("gone %d %+v", code, e)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/check", `{"url":"file:///etc/passwd"}`, nil); code != 400 {
		t.Fatalf("file link %d", code)
	}

	// For a title: a movie, or a season's episodes.
	root := t.TempDir()
	ps, _ := s.Store.Profiles(ctx)
	m, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "The Film", Year: 2001, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "The Film (2001)"), Monitored: true})
	d = store.Download{}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", fmt.Sprintf(`{"token":%q,"for":"/movies/%d/releases"}`, c.Token, m.ID), &d); code != 201 {
		t.Fatalf("for a movie %d", code)
	}
	if d.Kind != "movie" || d.TitleID != m.ID || d.FileAs != "" {
		t.Fatalf("%+v", d)
	}
	send("/api/v1/downloads/nzb/check?name=x.nzb", testNZB, "application/x-nzb", &c)
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", fmt.Sprintf(`{"token":%q,"for":"/etc/passwd"}`, c.Token), nil); code != 400 {
		t.Fatalf("for nothing %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/nzb/add", fmt.Sprintf(`{"token":%q,"for":"/movies/999/releases"}`, c.Token), nil); code != 404 {
		t.Fatalf("for a movie that isn't there %d", code)
	}
}

// An indexer's API key goes only to the indexer's own address: the same
// scheme, host and port. An http link to an https indexer is fetched over
// https, and a link that already carries a key is left alone.
func TestIndexerKeyOnlyToItsAddress(t *testing.T) {
	for _, c := range []struct{ link, indexer, want string }{
		{"https://nzb.example/getnzb/1.nzb", "https://nzb.example/api", "https://nzb.example/getnzb/1.nzb?apikey=k"},
		{"https://NZB.example:443/getnzb/1.nzb", "https://nzb.example", "https://NZB.example:443/getnzb/1.nzb?apikey=k"},
		{"http://nzb.example/getnzb/1.nzb", "https://nzb.example", "https://nzb.example/getnzb/1.nzb?apikey=k"},
		{"http://nzb.example:8080/getnzb/1.nzb", "https://nzb.example", "http://nzb.example:8080/getnzb/1.nzb"},
		{"http://nzb.example/getnzb/1.nzb", "https://nzb.example:8443", "http://nzb.example/getnzb/1.nzb"},
		{"https://nzb.example:9999/getnzb/1.nzb", "https://nzb.example", "https://nzb.example:9999/getnzb/1.nzb"},
		{"https://nzb.example/getnzb/1.nzb", "http://nzb.example", "https://nzb.example/getnzb/1.nzb"},
		{"http://nzb.example:5075/getnzb/1.nzb", "http://nzb.example:5075/", "http://nzb.example:5075/getnzb/1.nzb?apikey=k"},
		{"https://other.example/getnzb/1.nzb", "https://nzb.example", "https://other.example/getnzb/1.nzb"},
		{"https://nzb.example/getnzb/1.nzb?apikey=mine", "https://nzb.example", "https://nzb.example/getnzb/1.nzb?apikey=mine"},
	} {
		u, err := url.Parse(c.link)
		if err != nil {
			t.Fatal(err)
		}
		withIndexerKey(u, c.indexer, "k")
		if u.String() != c.want {
			t.Errorf("%s for %s: %s, want %s", c.link, c.indexer, u, c.want)
		}
	}
}

// Checked NZBs wait packed, and the oldest go once there are too many or
// they take too much room.
func TestNZBHoldIsCapped(t *testing.T) {
	var h nzbHold
	body := bytes.Repeat([]byte(`<segment bytes="716800" number="1">abc@example</segment>`), 2000)
	first := h.put(body, "first")
	got, ok := h.get(first)
	if !ok || !bytes.Equal(got.data, body) || got.name != "first" {
		t.Fatalf("held %v %d bytes", ok, len(got.data))
	}
	if len(got.packed) >= len(body)/5 {
		t.Fatalf("kept %d bytes of %d", len(got.packed), len(body))
	}
	var last string
	for i := 0; i < maxHeld; i++ {
		last = h.put(body, "more")
	}
	if len(h.held) != maxHeld {
		t.Fatalf("%d held", len(h.held))
	}
	if _, ok := h.get(first); ok {
		t.Fatal("the oldest wasn't dropped")
	}
	if _, ok := h.get(last); !ok {
		t.Fatal("the newest was dropped")
	}
	// Too much room: random bytes don't pack, so two of these are over.
	big := make([]byte, maxHeldBytes/2+1)
	rand.Read(big)
	a := h.put(big, "a")
	b := h.put(big, "b")
	if _, ok := h.get(a); ok {
		t.Fatal("over the size cap, the older stayed")
	}
	if _, ok := h.get(b); !ok || len(h.held) != 1 {
		t.Fatalf("after the big ones: %v, %d held", ok, len(h.held))
	}
	h.drop(b)
	if len(h.held) != 0 {
		t.Fatal("not dropped")
	}
}
