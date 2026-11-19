package api

import (
	"fmt"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A profile's size limits are saved and given back, kept by a save that
// doesn't mention them, cleared by one that sends none, and checked.
func TestProfileSizeLimits(t *testing.T) {
	_, hs, _ := monitorServer(t)
	var p quality.Profile
	if code := call(t, hs, "POST", "/api/v1/profiles", `{"name":"Small","allowed":[10],"cutoff":10,"upgrade":true,"sizes":{"h264":{"min":5,"max":100},"av1":{"max":25}}}`, &p); code != 201 {
		t.Fatalf("create: %d", code)
	}
	want := quality.Sizes{H264: quality.SizeLimit{Min: 5, Max: 100}, AV1: quality.SizeLimit{Max: 25}}
	path := fmt.Sprintf("/api/v1/profiles/%d", p.ID)
	read := func() quality.Sizes {
		var all []quality.Profile
		call(t, hs, "GET", "/api/v1/profiles", "", &all)
		for _, x := range all {
			if x.ID == p.ID {
				return x.Sizes
			}
		}
		t.Fatal("the profile is gone")
		return quality.Sizes{}
	}
	if got := read(); got != want {
		t.Fatalf("saved %+v", got)
	}
	if code := call(t, hs, "PUT", path, `{"name":"Small","allowed":[10,11],"cutoff":10,"upgrade":true}`, nil); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if got := read(); got != want {
		t.Fatalf("a save without sizes left %+v", got)
	}
	if code := call(t, hs, "PUT", path, `{"name":"Small","allowed":[10],"cutoff":10,"sizes":{"hevc":{"min":80,"max":20}}}`, nil); code != 400 {
		t.Fatalf("smallest over biggest: %d", code)
	}
	if code := call(t, hs, "PUT", path, `{"name":"Small","allowed":[10],"cutoff":10,"sizes":{}}`, nil); code != 200 {
		t.Fatalf("clear: %d", code)
	}
	if got := read(); got.Any() {
		t.Fatalf("cleared, and left %+v", got)
	}
}
