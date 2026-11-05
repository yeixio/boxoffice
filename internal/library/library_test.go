package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func addMovie(t *testing.T, st *store.Store, root, folder string, tmdb int64) store.Movie {
	t.Helper()
	ctx := context.Background()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: tmdb, Title: filepath.Base(folder), ProfileID: profiles[0].ID, RootFolder: root, Path: folder})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Removing a movie with its files deletes its whole folder, as shown, and
// nothing else.
func TestFolderPlanDeletesOnlyTheTitlesFolder(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	folder := filepath.Join(root, "Heat (1995)")
	write(t, filepath.Join(folder, "Heat (1995).mkv"), 3000)
	write(t, filepath.Join(folder, "Heat (1995).en.srt"), 20)
	write(t, filepath.Join(folder, "extras", "trailer.mp4"), 100)
	write(t, filepath.Join(root, "Ronin (1998)", "Ronin (1998).mkv"), 50)
	m := addMovie(t, st, root, folder, 949)
	addMovie(t, st, root, filepath.Join(root, "Ronin (1998)"), 8195)

	p, err := FolderPlan(ctx, st, "movie", m.ID, root, folder)
	if err != nil || p.Problem != "" {
		t.Fatal(p.Problem, err)
	}
	if p.Count != 3 || p.Size != 3120 || p.Folder != folder || p.Files[0].Size != 3000 {
		t.Fatalf("plan %+v", p)
	}
	if !p.Matches(3, 3120) || p.Matches(3, 3000) {
		t.Fatal("Matches")
	}
	if err := Delete(p); err != nil {
		t.Fatal(err)
	}
	if exists(folder) || !exists(filepath.Join(root, "Ronin (1998)", "Ronin (1998).mkv")) || !exists(root) {
		t.Fatal("deleted the wrong things")
	}
}

// A folder that isn't safely the title's own is never deleted.
func TestFolderPlanRefusesSharedOrOutsideFolders(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "a.mkv"), 10)
	write(t, filepath.Join(root, "Collection", "Inner (2001)", "Inner.mkv"), 10)

	tmdb := int64(1000)
	for name, folder := range map[string]string{
		"the root itself":       root,
		"outside the root":      t.TempDir(),
		"in the recycle folder": filepath.Join(root, ".recycle", "2026-01-01"),
		"holding another title": filepath.Join(root, "Collection"),
	} {
		t.Run(name, func(t *testing.T) {
			os.MkdirAll(folder, 0o755)
			tmdb += 2
			outer := addMovie(t, st, root, folder, tmdb)
			inner := addMovie(t, st, root, filepath.Join(root, "Collection", "Inner (2001)"), tmdb+1)
			defer st.DeleteMovie(ctx, inner.ID)
			p, err := FolderPlan(ctx, st, "movie", outer.ID, root, folder)
			if err != nil {
				t.Fatal(err)
			}
			if p.Problem == "" {
				t.Fatalf("no problem with %s", folder)
			}
			if Delete(p) == nil {
				t.Fatal("deleted")
			}
			st.DeleteMovie(ctx, outer.ID)
		})
	}
	if !exists(filepath.Join(root, "Collection", "Inner (2001)", "Inner.mkv")) {
		t.Fatal("something was deleted")
	}
}

// Deleting an episode's file takes the files named after it, never
// another video, and an emptied season folder; the show's folder stays.
func TestFilesPlanTakesCompanionsButNotOtherVideos(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	s1 := filepath.Join(show, "Season 01")
	ep := filepath.Join(s1, "Show - S01E01 - Pilot.mkv")
	write(t, ep, 1000)
	write(t, filepath.Join(s1, "Show - S01E01 - Pilot.en.srt"), 10)
	write(t, filepath.Join(s1, "Show - S01E01 - Pilot-thumb.jpg"), 5)
	write(t, filepath.Join(s1, "Show - S01E01 - Pilot.part2.mkv"), 500) // a video: stays
	write(t, filepath.Join(s1, "Show - S01E01 - Pilots.nfo"), 1)        // not its name: stays
	write(t, filepath.Join(show, "poster.jpg"), 7)

	p := FilesPlan(root, show, []store.File{{Path: ep, Size: 1000}})
	if p.Problem != "" || p.Count != 3 || p.Size != 1015 || p.Folder != "" {
		t.Fatalf("plan %+v", p)
	}
	if err := Delete(p); err != nil {
		t.Fatal(err)
	}
	if exists(ep) || !exists(filepath.Join(s1, "Show - S01E01 - Pilot.part2.mkv")) || !exists(filepath.Join(s1, "Show - S01E01 - Pilots.nfo")) {
		t.Fatal("wrong files deleted")
	}

	// The last episode going takes its emptied season folder, not the show's.
	s2 := filepath.Join(show, "Season 02")
	ep2 := filepath.Join(s2, "Show - S02E01.mkv")
	write(t, ep2, 10)
	if err := Delete(FilesPlan(root, show, []store.File{{Path: ep2}})); err != nil {
		t.Fatal(err)
	}
	if exists(s2) || !exists(filepath.Join(show, "poster.jpg")) {
		t.Fatal("season folder kept, or show folder touched")
	}

	// A file outside the show's folder is refused.
	outside := filepath.Join(root, "Other", "x.mkv")
	write(t, outside, 10)
	if p := FilesPlan(root, show, []store.File{{Path: outside}}); p.Problem == "" {
		t.Fatal("outside file allowed")
	}
	// A file already gone is nothing to delete.
	if p := FilesPlan(root, show, []store.File{{Path: ep}}); p.Count != 0 || p.Problem != "" {
		t.Fatalf("gone file: %+v", p)
	}
}

func TestEmptyRecycle(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, RecycleDir, "2026-01-01", "Old.mkv")
	recent := filepath.Join(root, RecycleDir, time.Now().UTC().Format(time.DateOnly), "New.mkv")
	other := filepath.Join(root, RecycleDir, "someone else's", "keep.mkv")
	write(t, old, 100)
	write(t, recent, 50)
	write(t, other, 1)

	use := RecycleUsage([]string{root, t.TempDir()})
	if len(use) != 1 || use[0].Count != 2 || use[0].Size != 150 || use[0].Oldest != "2026-01-01" {
		t.Fatalf("usage %+v", use)
	}
	freed, err := EmptyRecycle(root, time.Now().AddDate(0, 0, -7))
	if err != nil || freed != 100 || exists(old) || !exists(recent) {
		t.Fatal("clearing old copies", freed, err)
	}
	freed, err = EmptyRecycle(root, time.Time{})
	if err != nil || freed != 50 || exists(recent) || !exists(other) {
		t.Fatal("emptying", freed, err)
	}
}
