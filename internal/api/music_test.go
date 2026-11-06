package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/search"
)

const artistID = "a74b1b7f-71a5-4011-9441-d0b5e4122711"

func fakeMusicBrainz(t *testing.T) *musicbrainz.Client {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/artist":
			fmt.Fprint(w, `{"artists":[{"id":"`+artistID+`","name":"Radiohead","sort-name":"Radiohead","type":"Group"}]}`)
		case r.URL.Path == "/artist/"+artistID:
			fmt.Fprint(w, `{"id":"`+artistID+`","name":"Radiohead","sort-name":"Radiohead","type":"Group","country":"GB"}`)
		case r.URL.Path == "/release-group":
			fmt.Fprint(w, `{"release-group-count":4,"release-groups":[
				{"id":"b1b7f2a0-0000-4000-8000-000000000001","title":"OK Computer","primary-type":"Album","first-release-date":"1997-05-21"},
				{"id":"b1b7f2a0-0000-4000-8000-000000000002","title":"I Might Be Wrong","primary-type":"Album","secondary-types":["Live"],"first-release-date":"2001-11-12"},
				{"id":"b1b7f2a0-0000-4000-8000-000000000003","title":"My Iron Lung","primary-type":"EP","first-release-date":"1994-09-26"},
				{"id":"b1b7f2a0-0000-4000-8000-000000000004","title":"Creep","primary-type":"Single","first-release-date":"1992-09-21"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	c := musicbrainz.New("test")
	c.Base, c.Spacing = hs.URL, time.Millisecond
	return c
}

type artistOut struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	AlbumTypes   []string `json:"albumTypes"`
	Albums       int      `json:"albums"`
	WantedAlbums int      `json:"wantedAlbums"`
	AlbumList    []struct {
		ID        int64  `json:"id"`
		Title     string `json:"title"`
		Monitored bool   `json:"monitored"`
		Cover     string `json:"cover"`
	} `json:"albumList"`
}

func (a artistOut) wanted() []string {
	var out []string
	for _, b := range a.AlbumList {
		if b.Monitored {
			out = append(out, b.Title)
		}
	}
	return out
}

// Adding an artist needs music turned on and a music root folder; it
// watches for studio albums only, until the kinds wanted change.
func TestAddArtist(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Catalog = &catalog.Service{Store: s.Store}
	s.Music = &music.Service{Store: s.Store, MusicBrainz: fakeMusicBrainz(t)}
	body := `{"mbid":"` + artistID + `","monitored":true}`
	if code := call(t, hs, "GET", "/api/v1/lookup/artists?q=radiohead", "", nil); code != 409 {
		t.Fatalf("lookup with music off: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/artists", body, nil); code != 409 {
		t.Fatalf("add with music off: %d", code)
	}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":true}}`, nil)
	if code := call(t, hs, "POST", "/api/v1/artists", body, nil); code != 400 {
		t.Fatalf("add with no music folder: %d", code)
	}
	root := t.TempDir()
	if code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"music","path":"`+root+`"}`, nil); code != 201 {
		t.Fatal(code)
	}
	var found []struct {
		MBID string `json:"mbid"`
		ID   int64  `json:"id"`
	}
	if code := call(t, hs, "GET", "/api/v1/lookup/artists?q=radiohead", "", &found); code != 200 || len(found) != 1 || found[0].ID != 0 {
		t.Fatalf("lookup: %d %+v", code, found)
	}
	var a artistOut
	if code := call(t, hs, "POST", "/api/v1/artists", body, &a); code != 201 {
		t.Fatal(code)
	}
	if a.Name != "Radiohead" || a.Path != root+"/Radiohead" || a.Albums != 4 || a.WantedAlbums != 1 {
		t.Fatalf("added %+v", a)
	}
	if code := call(t, hs, "POST", "/api/v1/artists", body, nil); code != 409 {
		t.Fatalf("added twice: %d", code)
	}
	call(t, hs, "GET", "/api/v1/lookup/artists?q=radiohead", "", &found)
	if found[0].ID != a.ID {
		t.Fatalf("lookup doesn't see it's on the list: %+v", found)
	}

	call(t, hs, "GET", fmt.Sprintf("/api/v1/artists/%d", a.ID), "", &a)
	if w := a.wanted(); len(w) != 1 || w[0] != "OK Computer" || !strings.HasPrefix(a.AlbumList[0].Cover, "https://coverartarchive.org/") {
		t.Fatalf("wanted %v", w)
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/artists/%d", a.ID), `{"albumTypes":["Album","EP","Live"]}`, &a); code != 200 {
		t.Fatal(code)
	}
	if w := a.wanted(); len(w) != 3 {
		t.Fatalf("after adding EPs and live: %v", w)
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/artists/%d", a.ID), `{"albumTypes":["Bootleg"]}`, nil); code != 400 {
		t.Fatalf("an unknown album kind: %d", code)
	}
	var creep int64
	for _, b := range a.AlbumList {
		if b.Title == "Creep" {
			creep = b.ID
		}
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/albums/%d", creep), `{"monitored":true}`, nil); code != 200 {
		t.Fatal(code)
	}
	// A refresh keeps the choices made.
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/artists/%d/refresh", a.ID), "", &a); code != 200 || len(a.wanted()) != 4 {
		t.Fatalf("refresh: %d %v", code, a.wanted())
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/artists/%d", a.ID), "", nil); code != 204 {
		t.Fatal(code)
	}
	var list []artistOut
	call(t, hs, "GET", "/api/v1/artists", "", &list)
	if len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
}

// An artist added with albums in their folder already counts those as in
// the library; the album quality can be picked; and searching needs an
// indexer.
func TestArtistAlbumsOnDisk(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Catalog = &catalog.Service{Store: s.Store}
	s.Music = &music.Service{Store: s.Store, MusicBrainz: fakeMusicBrainz(t)}
	s.Search = &search.Service{Store: s.Store}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":true}}`, nil)
	root := t.TempDir()
	call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"music","path":"`+root+`"}`, nil)
	ok := filepath.Join(root, "Radiohead", "OK Computer (1997)")
	os.MkdirAll(ok, 0o755)
	os.WriteFile(filepath.Join(ok, "01 - Airbag.flac"), make([]byte, 50), 0o644)

	var a struct {
		artistOut
		HaveAlbums int `json:"haveAlbums"`
	}
	if code := call(t, hs, "POST", "/api/v1/artists", `{"mbid":"`+artistID+`","monitored":true}`, &a); code != 201 || a.HaveAlbums != 1 {
		t.Fatalf("added %d %+v", code, a)
	}
	var detail struct {
		AlbumList []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			File  *struct {
				Path string `json:"path"`
				Size int64  `json:"size"`
			} `json:"file"`
		} `json:"albumList"`
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/artists/%d", a.ID), "", &detail)
	var okID int64
	for _, b := range detail.AlbumList {
		if (b.File != nil) != (b.Title == "OK Computer") {
			t.Errorf("%s: %+v", b.Title, b.File)
		}
		if b.Title == "OK Computer" {
			okID = b.ID
			if b.File.Path != ok || b.File.Size != 50 {
				t.Errorf("file %+v", b.File)
			}
		}
	}
	var one struct {
		ArtistID int64 `json:"artistId"`
	}
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/albums/%d", okID), "", &one); code != 200 || one.ArtistID != a.ID {
		t.Fatalf("album: %d %+v", code, one)
	}

	var set struct {
		Quality   string   `json:"quality"`
		Qualities []string `json:"qualities"`
	}
	if code := call(t, hs, "GET", "/api/v1/music/settings", "", &set); code != 200 || set.Quality != "flac-first" || len(set.Qualities) != 3 {
		t.Fatalf("settings %d %+v", code, set)
	}
	if code := call(t, hs, "PUT", "/api/v1/music/settings", `{"quality":"mp3"}`, &set); code != 200 || set.Quality != "mp3" {
		t.Fatalf("saved %d %+v", code, set)
	}
	if code := call(t, hs, "PUT", "/api/v1/music/settings", `{"quality":"wav"}`, nil); code != 400 {
		t.Fatalf("an unknown quality: %d", code)
	}
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/albums/%d/releases", okID), "", nil); code != 400 {
		t.Fatalf("releases with no indexers: %d", code)
	}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":false}}`, nil)
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/albums/%d/releases", okID), "", nil); code != 409 {
		t.Fatalf("releases with music off: %d", code)
	}
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/albums/%d/search", okID), "", nil); code != 409 {
		t.Fatalf("search with music off: %d", code)
	}
}
