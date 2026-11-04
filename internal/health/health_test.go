package health

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func ids(issues []Issue) []string {
	var out []string
	for _, is := range issues {
		out = append(out, is.ID)
	}
	return out
}

func find(issues []Issue, id string) *Issue {
	for i := range issues {
		if issues[i].ID == id {
			return &issues[i]
		}
	}
	return nil
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	key := ""
	bk := &backup.Manager{Store: st, Dir: t.TempDir()}
	c := &Checker{Store: st, Backups: bk, TMDBKey: func(context.Context) string { return key }}

	// A fresh Box Office: no key, no root folders, backups only here.
	got := c.Check(ctx)
	for _, id := range []string{"tmdb-key", "root-none-movie", "root-none-series", "backups-local"} {
		if find(got, id) == nil {
			t.Errorf("fresh: no %s in %v", id, ids(got))
		}
	}
	for _, is := range got {
		if len(is.Steps) == 0 && is.ID != "check-failed" {
			t.Errorf("%s says nothing of how to fix it", is.ID)
		}
	}

	// Set up properly, nothing's wrong but the backups' one place.
	key = "k"
	movies, tv := t.TempDir(), t.TempDir()
	if _, err := st.AddRootFolder(ctx, "movie", movies); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRootFolder(ctx, "series", tv); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(movies, "Heat (1995)", "Heat.mkv")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("x"), 0o644)
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: 1, Path: file}); err != nil {
		t.Fatal(err)
	}
	// (A test machine's disk may be nearly full; that's not under test.)
	got = slices.DeleteFunc(c.Check(ctx), func(is Issue) bool { return strings.HasSuffix(is.ID, "-space") })
	if !slices.Equal(ids(got), []string{"backups-local"}) {
		t.Errorf("set up: %v", ids(got))
	}

	// Most files gone, a root folder gone, a key missing, a task failing,
	// backups off.
	for i := range 3 {
		st.AddFile(ctx, store.File{Kind: "movie", OwnerID: int64(10 + i), Path: filepath.Join("/nowhere", strings.Repeat("x", i+1)+".mkv")})
	}
	os.RemoveAll(tv)
	st.UpsertIndexer(ctx, store.Indexer{Name: "NZBgeek", Protocol: "usenet", Kind: "newznab", URL: "https://api.nzbgeek.info", Source: "Radarr", MissingSecret: true})
	j, _ := st.QueueJob(ctx, "refresh-movie", "Heat", "{}")
	st.FinishJob(ctx, j.ID, "failed", "TMDB didn't answer")
	s, _ := bk.Settings(ctx)
	s.EveryHours = 0
	bk.SaveSettings(ctx, s)

	got = c.Check(ctx)
	for _, id := range []string{"files-missing", "connections-secret", "jobs-failed", "backups-off"} {
		if find(got, id) == nil {
			t.Errorf("broken: no %s in %v", id, ids(got))
		}
	}
	if f := find(got, "files-missing"); f != nil && f.Level != "error" {
		t.Errorf("3 of 4 files missing is a %s", f.Level)
	}
	missing := false
	for _, is := range got {
		if strings.HasSuffix(is.ID, "-missing") && strings.Contains(is.Title, tv) {
			missing = is.Level == "error"
		}
	}
	if !missing {
		t.Errorf("no error for the missing TV folder in %v", ids(got))
	}
	if got[0].Level != "error" {
		t.Errorf("worst isn't first: %v", got[0])
	}
	if cs := find(got, "connections-secret"); cs != nil && !strings.Contains(cs.Detail, "NZBgeek") {
		t.Errorf("connections detail = %q", cs.Detail)
	}
}

func TestReadOnlyRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	ctx := context.Background()
	st := storetest.Open(t)
	dir := t.TempDir()
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	st.AddRootFolder(ctx, "movie", dir)
	got, err := (&Checker{Store: st}).rootFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || !strings.HasSuffix(got[0].ID, "-readonly") || got[0].Level != "notice" {
		t.Errorf("read-only root: %+v", got)
	}
}
