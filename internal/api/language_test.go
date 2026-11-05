package api

import (
	"testing"
)

// An admin's and a member's language is kept with their account, so it
// follows them to every browser; only languages with a catalog are taken.
func TestLanguagePick(t *testing.T) {
	_, hs := portalServer(t)

	admin := newVisitor(t, hs)
	if code := admin.do("POST", "/api/v1/setup", `{"username":"mike","password":"password1"}`, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/me/language", `{"language":"xx-Nope"}`, nil); code != 400 {
		t.Fatalf("unknown language: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/me/language", `{"language":"de"}`, nil); code != 200 {
		t.Fatalf("pick de: %d", code)
	}
	var status struct {
		User struct{ Language string } `json:"user"`
	}
	admin.do("GET", "/api/v1/status", "", &status)
	if status.User.Language != "de" {
		t.Fatalf("status after picking de: %+v", status)
	}
	// The API key is no one in particular, so it has no language to keep.
	if code := call(t, hs, "PUT", "/api/v1/me/language", `{"language":"fr"}`, nil); code != 403 {
		t.Fatalf("with the API key: %d", code)
	}
	// Back to following the browser.
	if code := admin.do("PUT", "/api/v1/me/language", `{"language":""}`, nil); code != 200 {
		t.Fatalf("automatic: %d", code)
	}

	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true}`, nil); code != 200 {
		t.Fatalf("turn on the request site: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/portal/members", `{"username":"alice","password":"password1"}`, nil); code != 201 {
		t.Fatalf("add member: %d", code)
	}
	alice := newVisitor(t, hs)
	if code := alice.do("PUT", "/api/portal/me/language", `{"language":"ja"}`, nil); code != 401 {
		t.Fatalf("before signing in: %d", code)
	}
	alice.do("POST", "/api/portal/login", `{"username":"alice","password":"password1"}`, nil)
	if code := alice.do("PUT", "/api/portal/me/language", `{"language":"ja"}`, nil); code != 200 {
		t.Fatalf("member picks ja: %d", code)
	}
	var site struct {
		Member struct{ Language string } `json:"member"`
	}
	alice.do("GET", "/api/portal/site", "", &site)
	if site.Member.Language != "ja" {
		t.Fatalf("site after picking ja: %+v", site)
	}
}
