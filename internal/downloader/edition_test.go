package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// Another edition goes in next to the movie's copy, or takes its place
// (the old copy to the recycle folder), named with its edition.
func TestImportEditions(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, _ := st.AddMovie(ctx, store.Movie{TMDBID: 78, Title: "Blade Runner", Year: 1982, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Blade Runner (1982)"), Monitored: true})
	os.MkdirAll(m.Path, 0o755)
	theatrical := filepath.Join(m.Path, "Blade Runner (1982).mkv")
	os.WriteFile(theatrical, make([]byte, 3000), 0o644)
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: theatrical, Quality: 10})
	li := &LibraryImporter{Store: st}

	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "blade.runner.dc.mkv"), make([]byte, 5000), 0o644)
	to, err := li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Blade.Runner.1982.Directors.Cut.1080p.BluRay", Edition: "Director's Cut", Mode: store.ModeAlongside}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Path, "Blade Runner (1982) {edition-Director's Cut}.mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(theatrical); err != nil {
		t.Fatal("the theatrical copy went")
	}
	m, _ = st.Movie(ctx, m.ID)
	if m.File == nil || m.File.Path != theatrical {
		t.Fatalf("the movie's copy changed: %+v", m.File)
	}
	if eds, _ := st.EditionFiles(ctx, m.ID); len(eds) != 1 || eds[0].Path != to {
		t.Fatalf("editions %+v", eds)
	}

	// The Final Cut replaces the theatrical copy, and is the edition
	// wanted from now on.
	dl2 := t.TempDir()
	os.WriteFile(filepath.Join(dl2, "br.fc.mkv"), make([]byte, 6000), 0o644)
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Want: store.WantBetter})
	to, err = li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Blade.Runner.1982.Final.Cut.2160p", Edition: "Final Cut", Mode: store.ModeReplace}, dl2, listFiles(dl2))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Path, "Blade Runner (1982) {edition-Final Cut}.mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(theatrical); err == nil {
		t.Fatal("the theatrical copy wasn't set aside")
	}
	m, _ = st.Movie(ctx, m.ID)
	if m.File == nil || m.File.Path != to {
		t.Fatalf("file %+v", m.File)
	}
	tw, _ := st.TitleWant(ctx, "movie", m.ID)
	if tw.Edition != "Final Cut" || tw.Want != "" {
		t.Fatalf("want %+v", tw)
	}
	if eds, _ := st.EditionFiles(ctx, m.ID); len(eds) != 1 {
		t.Fatalf("the Director's Cut was touched: %+v", eds)
	}
}
