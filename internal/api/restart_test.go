package api

import (
	"testing"
	"time"
)

// The Restart button answers first and restarts after, and only an admin
// can press it.
func TestRestart(t *testing.T) {
	s, hs := portalServer(t)

	stranger := newVisitor(t, hs)
	if code := stranger.do("POST", "/api/v1/admin/restart", `{}`, nil); code != 401 {
		t.Fatalf("restart without signing in: %d", code)
	}
	var info restartInfo
	if code := call(t, hs, "GET", "/api/v1/admin/restart", "", &info); code != 200 || info.Available || info.Downloads != 0 {
		t.Fatalf("before a restart is given: %d %+v", code, info)
	}
	if code := call(t, hs, "POST", "/api/v1/admin/restart", `{}`, nil); code != 409 {
		t.Fatalf("restart with nothing to restart with: %d", code)
	}

	restarted := make(chan struct{}, 1)
	s.Restart = func() { restarted <- struct{}{} }
	if code := call(t, hs, "GET", "/api/v1/admin/restart", "", &info); code != 200 || !info.Available || info.How != "self" {
		t.Fatalf("restart info: %d %+v", code, info)
	}
	var out struct {
		StartedAt time.Time `json:"startedAt"`
	}
	if code := call(t, hs, "POST", "/api/v1/admin/restart", `{}`, &out); code != 202 || !out.StartedAt.Equal(s.started) {
		t.Fatalf("restart: %d %+v", code, out)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("never restarted")
	}
}
