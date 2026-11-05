package books

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func write(t *testing.T, p string, n int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Books already in the root folders count as there, and nothing is moved.
func TestFindOnDisk(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	ebooks, audio := t.TempDir(), t.TempDir()
	mediatypes.SetEnabled(ctx, st, store.Ebook, true)
	mediatypes.SetEnabled(ctx, st, store.Audiobook, true)
	st.AddRootFolder(ctx, store.Ebook, ebooks)
	st.AddRootFolder(ctx, store.Audiobook, audio)
	a, _ := st.AddAuthor(ctx, store.Author{OLKey: "OL1A", Name: "Brandon Sanderson"})
	for i, title := range []string{"Mistborn: The Final Empire", "Elantris", "Warbreaker"} {
		st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: "/works/OL" + string(rune('1'+i)) + "W", Title: title, Year: 2006})
	}
	write(t, filepath.Join(ebooks, "Brandon Sanderson", "Mistborn (2006)", "Mistborn.pdf"), 10)
	write(t, filepath.Join(ebooks, "Brandon Sanderson", "Mistborn (2006)", "Mistborn.epub"), 5)
	write(t, filepath.Join(ebooks, "Brandon Sanderson", "Elantris.mobi"), 5)
	write(t, filepath.Join(audio, "Sanderson, Brandon", "Warbreaker", "01.mp3"), 7)
	write(t, filepath.Join(audio, "Sanderson, Brandon", "Warbreaker", "02.mp3"), 8)
	write(t, filepath.Join(audio, "Sanderson, Brandon", "Unknown Book", "01.mp3"), 8)
	// "Author - Title" right in the root folder, as the Scan button reads it.
	write(t, filepath.Join(audio, "Brandon Sanderson - Elantris (2005)", "Elantris.m4b"), 9)
	write(t, filepath.Join(ebooks, "Brandon Sanderson - Warbreaker.epub"), 4)
	write(t, filepath.Join(ebooks, "Someone Else - Warbreaker.epub"), 4)

	s := &Service{Store: st}
	n, err := s.FindOnDisk(ctx, a.ID)
	if err != nil || n != 5 {
		t.Fatalf("found %d: %v", n, err)
	}
	bs, _ := st.Books(ctx, a.ID)
	got := map[string]store.Book{}
	for _, b := range bs {
		got[b.Title] = b
	}
	if f := got["Mistborn: The Final Empire"].EbookFile; f == nil || filepath.Base(f.Path) != "Mistborn.epub" || !got["Mistborn: The Final Empire"].WantEbook {
		t.Fatalf("mistborn %+v", got["Mistborn: The Final Empire"])
	}
	if got["Elantris"].EbookFile == nil {
		t.Fatal("a loose ebook file wasn't found")
	}
	if f := got["Warbreaker"].AudiobookFile; f == nil || f.Size != 15 || !got["Warbreaker"].WantAudiobook {
		t.Fatalf("warbreaker %+v", got["Warbreaker"])
	}
	au, _ := st.Author(ctx, a.ID)
	if got["Elantris"].AudiobookFile == nil || got["Warbreaker"].EbookFile == nil || filepath.Base(got["Warbreaker"].EbookFile.Path) != "Brandon Sanderson - Warbreaker.epub" {
		t.Fatalf("top-level Author - Title copies weren't found: %+v %+v", got["Elantris"], got["Warbreaker"])
	}
	if au.HaveEbooks != 3 || au.HaveAudiobooks != 2 {
		t.Fatalf("author %+v", au)
	}
	if n, _ := s.FindOnDisk(ctx, a.ID); n != 0 {
		t.Fatalf("found %d again", n)
	}
	if _, err := os.Stat(filepath.Join(ebooks, "Brandon Sanderson", "Mistborn (2006)", "Mistborn.pdf")); err != nil {
		t.Fatal("a file was touched")
	}
}
