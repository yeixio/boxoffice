package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const radiohead = "a74b1b7f-71a5-4011-9441-d0b5e4122711"

func TestAlbumsPagesAndRetries(t *testing.T) {
	var calls, busy atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "BoxOffice/") || r.URL.Query().Get("fmt") != "json" {
			t.Errorf("request %s %q", r.URL, r.Header.Get("User-Agent"))
		}
		if r.URL.Query().Get("offset") == "100" && busy.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var groups []string
		start := 0
		fmt.Sscan(r.URL.Query().Get("offset"), &start)
		for i := start; i < min(start+100, 130); i++ {
			sec := `[]`
			if i == 1 {
				sec = `["Live"]`
			}
			groups = append(groups, fmt.Sprintf(`{"id":"g%d","title":"Album %d","primary-type":"Album","secondary-types":%s,"first-release-date":"1997-05-21"}`, i, i, sec))
		}
		fmt.Fprintf(w, `{"release-group-count":130,"release-groups":[%s]}`, strings.Join(groups, ","))
	}))
	defer hs.Close()
	c := New("test")
	c.Base, c.Spacing = hs.URL, time.Millisecond
	albums, err := c.Albums(context.Background(), radiohead)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 130 || calls.Load() != 3 {
		t.Fatalf("%d albums in %d calls", len(albums), calls.Load())
	}
	if !albums[0].Studio() || albums[1].Studio() || albums[1].SecondaryTypes[0] != "Live" {
		t.Fatalf("types %+v %+v", albums[0], albums[1])
	}
}

func TestRequestsAreSpaced(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"artists":[{"id":"`+radiohead+`","name":"Radiohead","sort-name":"Radiohead","type":"Group","country":"GB","life-span":{"begin":"1991"}}]}`)
	}))
	defer hs.Close()
	c := New("test")
	c.Base, c.Spacing = hs.URL, 50*time.Millisecond
	start := time.Now()
	for range 3 {
		got, err := c.SearchArtists(context.Background(), "radiohead")
		if err != nil || len(got) != 1 || got[0].Name != "Radiohead" || got[0].Began != "1991" {
			t.Fatal(got, err)
		}
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("three requests in %v", d)
	}
}

func TestIsMBID(t *testing.T) {
	if !IsMBID(radiohead) || IsMBID("radiohead") || IsMBID(strings.Replace(radiohead, "-", "x", 1)) {
		t.Fatal("IsMBID")
	}
}
