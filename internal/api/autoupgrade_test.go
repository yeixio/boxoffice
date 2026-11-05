package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// A movie or a show can follow Settings on automatic upgrades, or turn
// them on or off for itself.
func TestEditAutoUpgrade(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	root := t.TempDir()
	ps, _ := s.Store.Profiles(ctx)
	m, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Keep", Year: 2000, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Keep (2000)"), Monitored: true})
	sh, _ := s.Store.AddSeries(ctx, store.Series{TMDBID: 2, Title: "Show", ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Show"), Monitored: true})

	var got struct{ Movie store.Movie }
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/movies/%d", m.ID), `{"autoUpgrade":"on"}`, &got); code != 200 {
		t.Fatalf("movie on: %d", code)
	}
	if m, _ = s.Store.Movie(ctx, m.ID); m.AutoUpgrade != "on" || !m.Monitored {
		t.Fatalf("movie %+v", m)
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/series/%d", sh.ID), `{"autoUpgrade":"off"}`, nil); code != 200 {
		t.Fatalf("show off: %d", code)
	}
	if sh, _ = s.Store.Series(ctx, sh.ID); sh.AutoUpgrade != "off" {
		t.Fatalf("show %+v", sh)
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/series/%d", sh.ID), `{"autoUpgrade":"sometimes"}`, nil); code != 400 {
		t.Fatalf("bad value: %d", code)
	}
	// Other edits keep it.
	call(t, hs, "PATCH", fmt.Sprintf("/api/v1/series/%d", sh.ID), `{"monitored":false}`, nil)
	if sh, _ = s.Store.Series(ctx, sh.ID); sh.AutoUpgrade != "off" || sh.Monitored {
		t.Fatalf("after another edit %+v", sh)
	}
	call(t, hs, "PATCH", fmt.Sprintf("/api/v1/movies/%d", m.ID), `{"autoUpgrade":""}`, nil)
	if m, _ = s.Store.Movie(ctx, m.ID); m.AutoUpgrade != "" {
		t.Fatalf("back to Settings %+v", m)
	}
}
