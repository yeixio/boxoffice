package api

import (
	"context"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/playon"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestPlayOnAPI(t *testing.T) {
	srv, hs, _ := monitorServer(t)
	var v playOnView
	if code := call(t, hs, "GET", "/api/v1/playon", "", &v); code != 200 || v.Config.On || len(v.Recordings) != 0 {
		t.Fatalf("get: %d %+v", code, v)
	}
	var bad struct{ Error string }
	if code := call(t, hs, "PUT", "/api/v1/playon", `{"on":true,"email":"me@example.com"}`, &bad); code != 400 || !strings.Contains(bad.Error, "password") {
		t.Fatalf("no password: %d %+v", code, bad)
	}
	if code := call(t, hs, "PUT", "/api/v1/playon", `{"on":true,"email":"me@example.com","password":"secret"}`, &v); code != 200 || !v.Config.On || !v.Config.HasPassword {
		t.Fatalf("save: %d %+v", code, v)
	}
	// The password is kept when the page sends none back, and never sent.
	if code := call(t, hs, "PUT", "/api/v1/playon", `{"on":false,"email":"me@example.com"}`, &v); code != 200 || !v.Config.HasPassword {
		t.Fatalf("keep: %d %+v", code, v)
	}
	if c, _ := playon.LoadConfig(context.Background(), srv.Store); c.Password != "secret" {
		t.Fatalf("password = %q", c.Password)
	}
	ctx := context.Background()
	srv.Store.SeePlayOnRecording(ctx, store.PlayOnRecording{ID: 9, Name: "Arrival", Year: 2016})
	srv.Store.SetPlayOnState(ctx, 9, store.PlayOnFailed, "broke", "", 0, true)
	if code := call(t, hs, "POST", "/api/v1/playon/recordings/9/retry", "", &v); code != 200 || v.Recordings[0].State != store.PlayOnNew {
		t.Fatalf("retry: %d %+v", code, v)
	}
	if code := call(t, hs, "POST", "/api/v1/playon/recordings/9/retry", "", nil); code != 404 {
		t.Fatalf("retry again: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/playon/recordings/9/skip", "", &v); code != 200 || v.Recordings[0].State != store.PlayOnSkipped || v.Counts[store.PlayOnSkipped] != 1 {
		t.Fatalf("skip: %d %+v", code, v)
	}
	if code := call(t, hs, "POST", "/api/v1/playon/check", "", nil); code != 400 {
		t.Fatalf("check while off: %d", code)
	}
	// Another account forgets this one's recordings.
	if code := call(t, hs, "PUT", "/api/v1/playon", `{"on":false,"email":"you@example.com"}`, &v); code != 200 || v.Config.HasPassword || len(v.Recordings) != 0 {
		t.Fatalf("other account: %d %+v", code, v)
	}
}
