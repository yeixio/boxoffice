package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// A title on Do not touch: its files can't be deleted or replaced, it
// isn't searched for, and the profile itself can't be changed or removed.
// Titles go onto it one at a time or many at once.
func TestDoNotTouch(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	root := t.TempDir()
	var locked quality.Profile
	ps, _ := s.Store.Profiles(ctx)
	for _, p := range ps {
		if p.Locked {
			locked = p
		}
	}
	if locked.Name != quality.DoNotTouchName {
		t.Fatalf("no Do not touch profile in %+v", ps)
	}
	m, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Keep", Year: 2000, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Keep (2000)"), Monitored: true})
	other, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 2, Title: "Other", Year: 2001, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Other (2001)"), Monitored: true})
	file := filepath.Join(m.Path, "Keep (2000).mkv")
	touch(t, file, 1000)
	s.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: file, Size: 1000, Quality: 4})

	body := fmt.Sprintf(`{"ids":[%d,%d,999],"profileId":%d}`, m.ID, other.ID, locked.ID)
	var res struct{ Changed int }
	if code := call(t, hs, "POST", "/api/v1/movies/bulk", body, &res); code != 200 || res.Changed != 2 {
		t.Fatalf("bulk: %d %+v", code, res)
	}

	var plan fileRemoval
	call(t, hs, "GET", fmt.Sprintf("/api/v1/movies/%d/file/removal", m.ID), "", &plan)
	if plan.Problem == "" {
		t.Fatalf("plan %+v", plan)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/movies/%d/file?expectFiles=1&expectSize=1000", m.ID), "", nil); code != 409 || !there(file) {
		t.Fatalf("delete file: %d", code)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/movies/%d?deleteFiles=true&expectFiles=1&expectSize=1000", m.ID), "", nil); code != 409 || !there(file) {
		t.Fatalf("delete with files: %d", code)
	}
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/movies/%d/search", m.ID), "{}", nil); code != 409 {
		t.Fatalf("search: %d", code)
	}
	li := &downloader.LibraryImporter{Store: s.Store}
	if _, err := li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Keep.2000.1080p.BluRay"}, t.TempDir(), nil); !errors.Is(err, store.ErrLocked) {
		t.Fatal("import into a Do not touch title")
	}
	if code := call(t, hs, "PUT", fmt.Sprintf("/api/v1/profiles/%d", locked.ID), `{"name":"x","allowed":[1],"cutoff":1}`, nil); code != 400 {
		t.Fatalf("edit Do not touch: %d", code)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/profiles/%d", locked.ID), "", nil); code != 400 {
		t.Fatalf("remove Do not touch: %d", code)
	}

	// Moved back to another profile, it can be searched for again.
	body = fmt.Sprintf(`{"ids":[%d],"profileId":%d}`, m.ID, ps[0].ID)
	call(t, hs, "POST", "/api/v1/movies/bulk", body, nil)
	if locked, _ := s.Store.TitleLocked(ctx, "movie", m.ID); locked {
		t.Fatal("still locked")
	}
}
