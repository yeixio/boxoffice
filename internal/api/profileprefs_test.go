package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A profile's release preferences are saved with it, tidied, kept by a
// save that leaves them out, and cleared by an empty set. The ready-made
// ones are listed with their words.
func TestProfilePrefs(t *testing.T) {
	_, hs, _ := monitorServer(t)
	var presets []quality.Preset
	if code := call(t, hs, "GET", "/api/v1/profiles/presets", "", &presets); code != 200 || len(presets) != len(quality.Presets) || presets[0].ID != "best" || len(presets[0].Prefer) == 0 {
		t.Fatalf("presets: %d %+v", code, presets)
	}
	var p quality.Profile
	body := `{"name":"Picky","allowed":[10],"cutoff":10,"upgrade":true,"prefs":{"presets":["small","small"],"prefer":[" NTb ","ntb",""],"avoid":["upscaled"],"never":["KORSUB"]}}`
	if code := call(t, hs, "POST", "/api/v1/profiles", body, &p); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if strings.Join(p.Prefs.Presets, ",") != "small" || strings.Join(p.Prefs.Prefer, ",") != "NTb" || strings.Join(p.Prefs.Avoid, ",") != "upscaled" || strings.Join(p.Prefs.Never, ",") != "KORSUB" {
		t.Fatalf("created %+v", p.Prefs)
	}
	path := fmt.Sprintf("/api/v1/profiles/%d", p.ID)
	var got quality.Profile
	if code := call(t, hs, "PUT", path, `{"name":"Picky","allowed":[10],"cutoff":10}`, &got); code != 200 || strings.Join(got.Prefs.Prefer, ",") != "NTb" || len(got.Prefs.Presets) != 1 {
		t.Fatalf("a save without prefs: %d %+v", code, got.Prefs)
	}
	for _, bad := range []string{`{"presets":["nope"]}`, `{"avoid":["--"]}`} {
		if code := call(t, hs, "PUT", path, `{"name":"Picky","allowed":[10],"cutoff":10,"prefs":`+bad+`}`, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	got = quality.Profile{}
	if code := call(t, hs, "PUT", path, `{"name":"Picky","allowed":[10],"cutoff":10,"prefs":{}}`, &got); code != 200 || got.Prefs.Any() {
		t.Fatalf("cleared: %d %+v", code, got.Prefs)
	}
	var all []quality.Profile
	call(t, hs, "GET", "/api/v1/profiles", "", &all)
	for _, x := range all {
		if x.ID == p.ID && x.Prefs.Any() {
			t.Fatalf("listed with %+v", x.Prefs)
		}
	}
}
