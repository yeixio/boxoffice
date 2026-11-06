package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func albumFixture(t *testing.T) (*store.Store, store.Artist, []store.Album) {
	st := storetest.Open(t)
	ctx := context.Background()
	root := t.TempDir()
	st.AddRootFolder(ctx, mediatypes.Music, root)
	a, err := st.AddArtist(ctx, store.Artist{MBID: "a74b1b7f-71a5-4011-9441-d0b5e4122711", Name: "Radiohead", RootFolder: root, Path: filepath.Join(root, "Radiohead")})
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range []store.Album{
		{Title: "OK Computer", Type: "Album", Released: "1997-05-21"},
		{Title: "Kid A", Type: "Album", Released: "2000-10-02"},
		{Title: "Creep", Type: "Single", Released: "1992-09-21"},
		{Title: "Creep", Type: "Album", SecondaryTypes: []string{"Compilation"}, Released: "2008"},
		{Title: "In Rainbows", Type: "Album", Released: "2007-10-10"},
	} {
		b.ArtistID, b.MBID, b.Monitored = a.ID, string(rune('a'+i))+"1392450-e666-3926-a536-22c65f834433", true
		if _, err := st.UpsertAlbum(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	bs, _ := st.Albums(ctx, a.ID)
	return st, a, bs
}

func albumNamed(t *testing.T, list []store.Album, title, kind string) store.Album {
	for _, b := range list {
		if b.Title == title && (kind == "" || b.Type == kind && len(b.SecondaryTypes) == 0) {
			return b
		}
	}
	t.Fatalf("no %s", title)
	return store.Album{}
}

func TestImportAlbum(t *testing.T) {
	st, a, albums := albumFixture(t)
	ctx := context.Background()
	b := albumNamed(t, albums, "OK Computer", "")
	li := &LibraryImporter{Store: st}
	folder := filepath.Join(a.Path, "OK Computer (1997)")
	// An album already there goes aside whole.
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "old.mp3"), make([]byte, 10), 0o644)

	dl := t.TempDir()
	rel := filepath.Join(dl, "Radiohead - OK Computer (1997) [FLAC]")
	os.MkdirAll(filepath.Join(rel, "CD1"), 0o755)
	os.MkdirAll(filepath.Join(rel, "CD2"), 0o755)
	os.WriteFile(filepath.Join(rel, "CD1", "01 - Airbag.flac"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(rel, "CD2", "01 - Lull.flac"), make([]byte, 200), 0o644)
	os.WriteFile(filepath.Join(rel, "folder.jpg"), make([]byte, 5), 0o644)
	os.WriteFile(filepath.Join(rel, "rip.log"), make([]byte, 5), 0o644)
	os.WriteFile(filepath.Join(rel, "release.nfo"), make([]byte, 5), 0o644)
	to, err := li.Import(ctx, store.Download{Kind: store.AlbumKind, TitleID: b.ID, Name: "Radiohead - OK Computer (1997) [FLAC]"}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if to != folder {
		t.Fatalf("to %s", to)
	}
	for _, f := range []string{"CD1/01 - Airbag.flac", "CD2/01 - Lull.flac", "folder.jpg", "rip.log"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Errorf("%s didn't come along", f)
		}
	}
	for _, f := range []string{"old.mp3", "release.nfo"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err == nil {
			t.Errorf("%s is in the album's folder", f)
		}
	}
	if aside, _ := filepath.Glob(filepath.Join(a.RootFolder, ".recycle", "*", "OK Computer (1997)", "old.mp3")); len(aside) != 1 {
		matches, _ := filepath.Glob(filepath.Join(a.RootFolder, ".recycle", "*"))
		t.Errorf("the old copy wasn't set aside: %v", matches)
	}
	b, _ = st.Album(ctx, b.ID)
	if b.File == nil || b.File.Path != folder || b.File.Size != 300 {
		t.Fatalf("album %+v", b.File)
	}
	a, _ = st.Artist(ctx, a.ID)
	if a.HaveAlbums != 1 {
		t.Fatalf("have %d", a.HaveAlbums)
	}
}

func TestFindAlbums(t *testing.T) {
	st, a, albums := albumFixture(t)
	ctx := context.Background()
	mk := func(dir string, files ...string) {
		os.MkdirAll(filepath.Join(a.Path, dir), 0o755)
		for _, f := range files {
			os.WriteFile(filepath.Join(a.Path, dir, f), make([]byte, 10), 0o644)
		}
	}
	mk("OK Computer (1997) [FLAC]", "01.flac", "02.flac")
	mk("2000 - Kid A", "01.mp3")
	mk("Radiohead - Creep", "01.mp3")
	mk("In Rainbows (2007)", "cover.jpg") // no audio: not the album
	mk(".recycle", "x.mp3")
	if n, err := FindAlbums(ctx, st, a); err != nil || n != 3 {
		t.Fatalf("found %d %v", n, err)
	}
	albums, _ = st.Albums(ctx, a.ID)
	for _, c := range []struct {
		title, kind, dir string
	}{
		{"OK Computer", "", "OK Computer (1997) [FLAC]"},
		{"Kid A", "", "2000 - Kid A"},
		{"Creep", "Single", "Radiohead - Creep"},
	} {
		b := albumNamed(t, albums, c.title, c.kind)
		if b.File == nil || b.File.Path != filepath.Join(a.Path, c.dir) {
			t.Errorf("%s: %+v", c.title, b.File)
		}
	}
	if b := albumNamed(t, albums, "In Rainbows", ""); b.File != nil {
		t.Errorf("In Rainbows found in a folder with no audio")
	}
	// Looking again finds nothing new and changes nothing.
	if n, _ := FindAlbums(ctx, st, a); n != 0 {
		t.Fatalf("found %d again", n)
	}
	entries, _ := os.ReadDir(a.Path)
	if len(entries) != 5 {
		t.Fatalf("the folder changed: %d", len(entries))
	}
}

func TestCommonDir(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  string
	}{
		{[]string{"01.flac", "02.flac"}, ""},
		{[]string{"A/01.flac", "A/02.flac"}, "A/"},
		{[]string{"A/CD1/01.flac", "A/CD2/01.flac"}, "A/"},
		{[]string{"CD1/01.flac", "CD1/02.flac"}, ""},
		{[]string{"A/CD1/01.flac", "A/CD1/02.flac"}, "A/"},
	} {
		if got := commonDir(c.files); got != c.want {
			t.Errorf("%v: %q", c.files, got)
		}
	}
}
