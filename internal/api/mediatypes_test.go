package api

import (
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
)

// New media types start off; a root folder for one is refused until it's
// turned on in Settings, and turning it off again keeps its folders.
func TestMediaTypesOffUntilTurnedOn(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Catalog = &catalog.Service{Store: s.Store}
	type settings struct {
		MediaTypes []struct {
			Key       string   `json:"key"`
			Enabled   bool     `json:"enabled"`
			Suggested []string `json:"suggested"`
		} `json:"mediaTypes"`
	}
	enabled := func(s settings) map[string]bool {
		out := map[string]bool{}
		for _, t := range s.MediaTypes {
			out[t.Key] = t.Enabled
		}
		return out
	}
	var got settings
	if code := call(t, hs, "GET", "/api/v1/settings", "", &got); code != 200 {
		t.Fatal(code)
	}
	if len(got.MediaTypes) != 4 {
		t.Fatalf("types %+v", got.MediaTypes)
	}
	for k, on := range enabled(got) {
		if on {
			t.Fatalf("%s is on by default", k)
		}
	}

	dir := t.TempDir()
	if code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"music","path":"`+dir+`"}`, nil); code != 400 {
		t.Fatalf("a music folder while music is off: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"podcast":true}}`, nil); code != 400 {
		t.Fatalf("an unknown type: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":true}}`, &got); code != 200 {
		t.Fatal(code)
	}
	if on := enabled(got); !on["music"] || on["ebook"] || on["audiobook"] || on["comic"] {
		t.Fatalf("after turning music on: %v", on)
	}
	if code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"music","path":"`+dir+`"}`, nil); code != 201 {
		t.Fatalf("a music folder while music is on: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":false}}`, &got); code != 200 || enabled(got)["music"] {
		t.Fatalf("turning music off: %d %v", code, enabled(got))
	}
	var roots []struct{ Path string }
	call(t, hs, "GET", "/api/v1/rootfolders?kind=music", "", &roots)
	if len(roots) != 1 || roots[0].Path != dir {
		t.Fatalf("turning music off lost its folder: %+v", roots)
	}
}
