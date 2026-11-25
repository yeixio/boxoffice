package api

import "testing"

// Unwanted tracks are off until turned on, and can't be turned on
// without the languages to keep.
func TestTracksSettings(t *testing.T) {
	_, hs, _ := monitorServer(t)
	var v tracksView
	if code := call(t, hs, "GET", "/api/v1/tracks", "", &v); code != 200 || v.Config.On || len(v.Languages) == 0 || v.Saved.Files != 0 {
		t.Fatalf("at first: %d %+v", code, v.Config)
	}
	if code := call(t, hs, "PUT", "/api/v1/tracks", `{"on":true,"languages":[]}`, nil); code != 400 {
		t.Fatalf("on with no languages: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/tracks", `{"on":true,"languages":["eng","English","es"]}`, &v); code != 200 || !v.Config.On {
		t.Fatalf("turned on: %d %+v", code, v.Config)
	}
	if len(v.Config.Languages) != 2 || v.Config.Languages[0] != "en" || v.Config.Languages[1] != "es" {
		t.Fatalf("languages %v", v.Config.Languages)
	}
}
