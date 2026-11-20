package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// A movie's second copy is asked for on the movie: another profile than
// the first copy's, next to it or in another movie root folder. Deleting
// it is planned and shown like any other delete, and leaves the first
// copy alone.
func TestEditSecondCopy(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Catalog = &catalog.Service{Store: s.Store, Jobs: jobs.New(s.Store, s.Log), Log: s.Log}
	root, other := t.TempDir(), t.TempDir()
	s.Store.AddRootFolder(ctx, "movie", root)
	s.Store.AddRootFolder(ctx, "movie", other)
	ps, _ := s.Store.Profiles(ctx)
	var locked quality.Profile
	for _, p := range ps {
		if p.Locked {
			locked = p
		}
	}
	if locked.ID == 0 {
		t.Fatal("no Do not touch profile")
	}
	m, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Keep", Year: 2000, ProfileID: ps[2].ID, RootFolder: root, Path: filepath.Join(root, "Keep (2000)"), Monitored: true})
	sh, _ := s.Store.AddSeries(ctx, store.Series{TMDBID: 2, Title: "Show", ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Show"), Monitored: true})
	path := fmt.Sprintf("/api/v1/movies/%d", m.ID)
	for _, bad := range []string{
		fmt.Sprintf(`{"secondProfileId":%d}`, ps[2].ID), // the first copy's
		`{"secondProfileId":9999}`,
		fmt.Sprintf(`{"secondProfileId":%d}`, locked.ID),
		fmt.Sprintf(`{"secondProfileId":%d,"secondRoot":"/nowhere"}`, ps[1].ID),
	} {
		if code := call(t, hs, "PATCH", path, bad, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/series/%d", sh.ID), fmt.Sprintf(`{"secondProfileId":%d}`, ps[1].ID), nil); code != 400 {
		t.Fatalf("a show: %d", code)
	}
	var got store.Movie
	if code := call(t, hs, "PATCH", path, fmt.Sprintf(`{"secondProfileId":%d,"secondRoot":%q}`, ps[1].ID, other), &got); code != 200 || got.SecondProfileID != ps[1].ID || got.SecondRoot != other {
		t.Fatalf("on: %d %+v", code, got)
	}
	// It's looked for at once.
	if list, _ := s.Store.Jobs(ctx, 10); len(list) != 1 || list[0].Kind != catalog.JobSearchMovie {
		t.Fatalf("jobs %+v", list)
	}
	// The movie's own root folder means next to the first copy.
	got = store.Movie{}
	if code := call(t, hs, "PATCH", path, fmt.Sprintf(`{"secondRoot":%q}`, root), &got); code != 200 || got.SecondRoot != "" {
		t.Fatalf("same root: %d %+v", code, got)
	}
	call(t, hs, "PATCH", path, fmt.Sprintf(`{"secondRoot":%q}`, other), &got)

	// Both copies on disk; the second in the other root folder.
	first := filepath.Join(m.Path, "Keep (2000).mkv")
	second := filepath.Join(other, "Keep (2000)", "Keep (2000).mkv")
	for p, n := range map[string]int{first: 900, second: 500} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, n), 0o644)
	}
	os.WriteFile(filepath.Join(other, "Keep (2000)", "Keep (2000).en.srt"), make([]byte, 10), 0o644)
	s.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: first, Size: 900, Quality: 16})
	s.Store.AddFile(ctx, store.File{Kind: store.SecondKind, OwnerID: m.ID, Path: second, Size: 500, Quality: 11})
	if code := call(t, hs, "GET", path, "", &got); code != 200 || got.File == nil || got.Second == nil || got.Second.Path != second {
		t.Fatalf("movie: %d %+v", code, got)
	}
	// It can't be kept somewhere else while it's on disk.
	if code := call(t, hs, "PATCH", path, `{"secondRoot":""}`, nil); code != 409 {
		t.Fatalf("moving it: %d", code)
	}
	// Removing the movie with its folder says the second copy stays.
	var tr struct {
		Count      int
		SecondKept string
	}
	if code := call(t, hs, "GET", path+"/removal", "", &tr); code != 200 || tr.Count != 1 || tr.SecondKept != second {
		t.Fatalf("removal: %d %+v", code, tr)
	}
	// Deleting the second copy: planned, shown, and only it goes.
	var plan struct {
		Count   int
		Size    int64
		Watched bool
		Problem string
	}
	if code := call(t, hs, "GET", path+"/second/removal", "", &plan); code != 200 || plan.Count != 2 || plan.Size != 510 || !plan.Watched || plan.Problem != "" {
		t.Fatalf("plan: %d %+v", code, plan)
	}
	if code := call(t, hs, "DELETE", path+"/second?expectFiles=2&expectSize=1", "", nil); code != 409 {
		t.Fatalf("not what was shown: %d", code)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal("deleted though the plan didn't match")
	}
	got = store.Movie{}
	if code := call(t, hs, "DELETE", path+"/second?expectFiles=2&expectSize=510&unmonitor=true", "", &got); code != 200 || got.Second != nil || got.SecondProfileID != 0 || got.File == nil {
		t.Fatalf("delete: %d %+v", code, got)
	}
	if _, err := os.Stat(second); err == nil {
		t.Fatal("the second copy is still there")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("the first copy went")
	}
}
