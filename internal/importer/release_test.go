package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKindFromName(t *testing.T) {
	for _, c := range []struct{ name, category, want string }{
		{"The.Matrix.1999.1080p.BluRay.x264-GRP", "", Movie},
		{"Some.Show.S02.1080p.WEB-DL", "", Series},
		{"Some.Show.S01E05.720p.HDTV", "", Series},
		{"Some Show Season 3 Complete", "", Series},
		{"obfuscated", "TV > HD", Series},
		{"obfuscated", "Movies > UHD", Movie},
		{"Artist - Album (2001) FLAC", "", Album},
		{"obfuscated", "Audio > MP3", Album},
		{"Author - Title Unabridged", "", Audiobook},
		{"obfuscated", "Audio > Audiobook", Audiobook},
		{"obfuscated", "Books > EBook", Ebook},
		{"obfuscated", "Console > NDS", Game},
		{"a1b2c3d4e5", "", ""},
	} {
		if got := kindFromName(c.name, c.category); got != c.want {
			t.Errorf("%s (%s): %q, want %q", c.name, c.category, got, c.want)
		}
	}
}

// Before it's downloaded, a release is guessed from its name, or from its
// files when they aren't packed.
func TestGuessRelease(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	rars := []ReleaseFile{{"grp-matrix.part01.rar", 100 << 20}, {"grp-matrix.part02.rar", 50 << 20}}
	it := s.GuessRelease(ctx, "The.Matrix.1999.1080p.BluRay.x264-GRP", rars, "")
	if it.Kind != Movie || it.Match == nil || it.Match.TMDBID != 603 || !it.Sure || it.Files != 2 || it.Size != 150<<20 {
		t.Fatalf("matrix %+v", it)
	}
	it = s.GuessRelease(ctx, "a1b2c3d4e5", []ReleaseFile{{"x.rar", 1}}, "")
	if it.Kind != "" || it.Problem == "" {
		t.Fatalf("unknown %+v", it)
	}
	// Unpacked files say what it is, whatever the name.
	it = s.GuessRelease(ctx, "a1b2c3d4e5", []ReleaseFile{{"Author - Book.epub", 1 << 20}}, "")
	if it.Kind != Ebook {
		t.Fatalf("ebook %+v", it)
	}
}

// A finished download handed over is filed when Box Office is sure what
// it is, and waits on the Import files page when it isn't.
func TestAdopt(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	lib := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, "movie", lib); err != nil {
		t.Fatal(err)
	}
	dl := s.Downloads.Dir
	write(t, filepath.Join(dl, "The.Matrix.1999.1080p", "grp-matrix.mkv"), 5000)
	write(t, filepath.Join(dl, "The.Matrix.1999.1080p", "grp-matrix.nfo"), 10)
	to, waiting, err := s.Adopt(ctx, filepath.Join(dl, "The.Matrix.1999.1080p"), "The.Matrix.1999.1080p", Auto)
	if err != nil || waiting != "" || !strings.HasPrefix(to, lib) {
		t.Fatalf("matrix: %q %q %v", to, waiting, err)
	}
	if m, err := s.Store.MovieByTMDB(ctx, 603); err != nil || m.File == nil {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(dl, "The.Matrix.1999.1080p")); err == nil {
		t.Fatal("the download's folder is still there")
	}

	write(t, filepath.Join(dl, "a1b2c3", "x.txt"), 10)
	to, waiting, err = s.Adopt(ctx, filepath.Join(dl, "a1b2c3"), "a1b2c3", Auto)
	if err != nil || to != "" || waiting == "" {
		t.Fatalf("unknown: %q %q %v", to, waiting, err)
	}
	st := settled(t, s)
	if it := item(t, st, "a1b2c3"); it.Status != Ready || it.Problem == "" {
		t.Fatalf("%+v", it)
	}
	// Said to be a movie, it's looked up as one; a name taken already gets
	// another.
	write(t, filepath.Join(dl, "a1b2c3", "x.mkv"), 10)
	if _, waiting, err = s.Adopt(ctx, filepath.Join(dl, "a1b2c3"), "a1b2c3", Movie); err != nil || waiting == "" {
		t.Fatalf("%q %v", waiting, err)
	}
	st = settled(t, s)
	if it := item(t, st, "a1b2c3 (2)"); it.Kind != Movie {
		t.Fatalf("%+v", it)
	}
}
