package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
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
