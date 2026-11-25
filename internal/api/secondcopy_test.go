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

// A show's second copies are asked for on the show, the same way. They're
// deleted all at once, planned and shown, and the first copies stay.
func TestEditShowSecondCopies(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Catalog = &catalog.Service{Store: s.Store, Jobs: jobs.New(s.Store, s.Log), Log: s.Log}
	root, other := t.TempDir(), t.TempDir()
	s.Store.AddRootFolder(ctx, "series", root)
	s.Store.AddRootFolder(ctx, "series", other)
	ps, _ := s.Store.Profiles(ctx)
	sh, err := s.Store.AddSeries(ctx, store.Series{TMDBID: 2, Title: "Show", ProfileID: ps[2].ID, RootFolder: root, Path: filepath.Join(root, "Show"),
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		s.Store.UpsertEpisode(ctx, store.Episode{SeriesID: sh.ID, Season: 1, Number: i, Monitored: true})
	}
	eps, _ := s.Store.Episodes(ctx, sh.ID)
	path := fmt.Sprintf("/api/v1/series/%d", sh.ID)
	for _, bad := range []string{
		fmt.Sprintf(`{"secondProfileId":%d}`, ps[2].ID), // the first copies'
		fmt.Sprintf(`{"secondProfileId":%d,"secondRoot":"/nowhere"}`, ps[1].ID),
	} {
		if code := call(t, hs, "PATCH", path, bad, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	var got struct {
		Series   store.Series
		Episodes []store.Episode
	}
	if code := call(t, hs, "PATCH", path, fmt.Sprintf(`{"secondProfileId":%d,"secondRoot":%q}`, ps[1].ID, other), &got); code != 200 || got.Series.SecondProfileID != ps[1].ID || got.Series.SecondRoot != other {
		t.Fatalf("on: %d %+v", code, got.Series)
	}
	// They're looked for at once.
	if list, _ := s.Store.Jobs(ctx, 10); len(list) != 1 || list[0].Kind != catalog.JobSearchSeries {
		t.Fatalf("jobs %+v", list)
	}

	// The first episode has both copies, the second only a second copy.
	first := filepath.Join(sh.Path, "Season 01", "Show - S01E01.mkv")
	seconds := []string{
		filepath.Join(other, "Show", "Season 01", "Show - S01E01.mkv"),
		filepath.Join(other, "Show", "Season 01", "Show - S01E02.mkv"),
	}
	for p, n := range map[string]int{first: 900, seconds[0]: 500, seconds[1]: 400} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, n), 0o644)
	}
	os.WriteFile(filepath.Join(other, "Show", "Season 01", "Show - S01E01.en.srt"), make([]byte, 10), 0o644)
	s.Store.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: first, Size: 900, Quality: 15})
	s.Store.AddFile(ctx, store.File{Kind: store.SecondEpisodeKind, OwnerID: eps[0].ID, Path: seconds[0], Size: 500, Quality: 10})
	s.Store.AddFile(ctx, store.File{Kind: store.SecondEpisodeKind, OwnerID: eps[1].ID, Path: seconds[1], Size: 400, Quality: 10})
	if code := call(t, hs, "GET", path, "", &got); code != 200 || len(got.Episodes) != 2 || got.Episodes[0].File == nil || got.Episodes[0].Second == nil ||
		got.Episodes[0].Second.Path != seconds[0] || got.Episodes[1].File != nil || got.Episodes[1].Second == nil {
		t.Fatalf("show: %d %+v", code, got.Episodes)
	}
	// They can't be kept somewhere else while they're on disk, on or off.
	for _, body := range []string{`{"secondRoot":""}`, `{"secondProfileId":0,"secondRoot":""}`} {
		if code := call(t, hs, "PATCH", path, body, nil); code != 409 {
			t.Fatalf("moving them with %s: %d", body, code)
		}
	}
	// Removing the show with its folder says the second copies stay.
	var tr struct {
		Count      int
		SecondKept string
	}
	if code := call(t, hs, "GET", path+"/removal", "", &tr); code != 200 || tr.Count != 1 || tr.SecondKept != filepath.Join(other, "Show") {
		t.Fatalf("removal: %d %+v", code, tr)
	}
	// Deleting the second copies: planned, shown, and only they go.
	var plan struct {
		Count   int
		Size    int64
		Watched bool
		Problem string
	}
	if code := call(t, hs, "GET", path+"/second/removal", "", &plan); code != 200 || plan.Count != 3 || plan.Size != 910 || !plan.Watched || plan.Problem != "" {
		t.Fatalf("plan: %d %+v", code, plan)
	}
	if code := call(t, hs, "DELETE", path+"/second?expectFiles=3&expectSize=1", "", nil); code != 409 {
		t.Fatalf("not what was shown: %d", code)
	}
	if _, err := os.Stat(seconds[0]); err != nil {
		t.Fatal("deleted though the plan didn't match")
	}
	got.Series, got.Episodes = store.Series{}, nil
	if code := call(t, hs, "DELETE", path+"/second?expectFiles=3&expectSize=910&unmonitor=true", "", &got); code != 200 || got.Series.SecondProfileID != 0 ||
		len(got.Episodes) != 2 || got.Episodes[0].Second != nil || got.Episodes[1].Second != nil || got.Episodes[0].File == nil {
		t.Fatalf("delete: %d %+v %+v", code, got.Series, got.Episodes)
	}
	for _, p := range seconds {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s is still there", p)
		}
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("a first copy went")
	}
}
