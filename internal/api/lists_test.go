package api

import (
	"strings"
	"testing"
)

func TestListsAPI(t *testing.T) {
	_, hs, _ := monitorServer(t)
	var all listsView
	if code := call(t, hs, "GET", "/api/v1/lists", "", &all); code != 200 || len(all.Lists) != 0 || all.TraktClient || all.PerCheck == 0 {
		t.Fatalf("lists: %d %+v", code, all)
	}
	// A site Box Office can't follow says so.
	var bad struct{ Error string }
	if code := call(t, hs, "POST", "/api/v1/lists/look", `{"address":"https://www.imdb.com/list/ls000000001/"}`, &bad); code != 400 || !strings.Contains(bad.Error, "TMDB") {
		t.Fatalf("imdb: %d %+v", code, bad)
	}
	// Trakt without a client ID asks for one instead of failing.
	var pre struct {
		Source           string
		NeedsTraktClient bool
	}
	if code := call(t, hs, "POST", "/api/v1/lists/look", `{"address":"trakt.tv/users/sam/watchlist"}`, &pre); code != 200 || !pre.NeedsTraktClient || pre.Source != "trakt" {
		t.Fatalf("trakt: %d %+v", code, pre)
	}
	if code := call(t, hs, "PUT", "/api/v1/lists/trakt", `{"clientId":"not a client id"}`, nil); code != 400 {
		t.Fatalf("bad client id: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/lists/trakt", `{"clientId":"`+strings.Repeat("ab12", 16)+`"}`, nil); code != 204 {
		t.Fatalf("client id: %d", code)
	}
	call(t, hs, "GET", "/api/v1/lists", "", &all)
	if !all.TraktClient {
		t.Fatal("the client ID wasn't kept")
	}
	if code := call(t, hs, "DELETE", "/api/v1/lists/7", "", nil); code != 404 {
		t.Fatalf("no such list: %d", code)
	}
}
