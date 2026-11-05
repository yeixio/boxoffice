package downloader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func bookFixture(t *testing.T) (*store.Store, store.Author, store.Book, string, string) {
	st := storetest.Open(t)
	ctx := context.Background()
	ebooks, audio := t.TempDir(), t.TempDir()
	st.AddRootFolder(ctx, store.Ebook, ebooks)
	st.AddRootFolder(ctx, store.Audiobook, audio)
	a, _ := st.AddAuthor(ctx, store.Author{OLKey: "OL1A", Name: "Frank Herbert"})
	st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: "/works/OL1W", Title: "Dune", Year: 1965, WantEbook: true})
	bs, _ := st.Books(ctx, a.ID)
	return st, a, bs[0], ebooks, audio
}

func TestImportEbook(t *testing.T) {
	st, a, b, ebooks, _ := bookFixture(t)
	ctx := context.Background()
	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "dune.pdf"), make([]byte, 900), 0o644)
	os.WriteFile(filepath.Join(dl, "dune.epub"), make([]byte, 300), 0o644)
	os.WriteFile(filepath.Join(dl, "dune.nfo"), []byte("x"), 0o644)
	li := &LibraryImporter{Store: st}
	to, err := li.Import(ctx, store.Download{Kind: store.Ebook, TitleID: b.ID, Name: "Frank Herbert - Dune EPUB"}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(ebooks, "Frank Herbert", "Dune (1965)", "Dune (1965).epub"); to != want {
		t.Fatalf("to %s", to)
	}
	b, _ = st.Book(ctx, b.ID)
	if b.EbookFile == nil || b.EbookFile.Path != to || b.AudiobookFile != nil {
		t.Fatalf("book %+v", b)
	}

	// Do not touch: nothing moves in.
	a.Locked = true
	st.UpdateAuthor(ctx, a)
	if _, err := li.Import(ctx, store.Download{Kind: store.Ebook, TitleID: b.ID, Name: "x"}, dl, listFiles(dl)); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("imported for a locked author: %v", err)
	}
}

func TestImportAudiobook(t *testing.T) {
	st, _, b, _, audio := bookFixture(t)
	ctx := context.Background()
	li := &LibraryImporter{Store: st}
	folder := filepath.Join(audio, "Frank Herbert", "Dune (1965)")
	// A copy already there goes aside whole.
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "old.mp3"), make([]byte, 10), 0o644)

	dl := t.TempDir()
	os.MkdirAll(filepath.Join(dl, "CD1"), 0o755)
	os.MkdirAll(filepath.Join(dl, "CD2"), 0o755)
	os.WriteFile(filepath.Join(dl, "CD1", "01 - Dune.mp3"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(dl, "CD2", "01 - Dune.mp3"), make([]byte, 200), 0o644)
	os.WriteFile(filepath.Join(dl, "cover.jpg"), make([]byte, 5), 0o644)
	to, err := li.Import(ctx, store.Download{Kind: store.Audiobook, TitleID: b.ID, Name: "Frank Herbert - Dune - Unabridged MP3"}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if to != folder {
		t.Fatalf("to %s", to)
	}
	for _, f := range []string{"CD1/01 - Dune.mp3", "CD2/01 - Dune.mp3", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Errorf("%s didn't come along", f)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "old.mp3")); err == nil {
		t.Error("the old copy's tracks are mixed in")
	}
	recycled, _ := filepath.Glob(filepath.Join(audio, ".recycle", "*", "Dune (1965)", "old.mp3"))
	if len(recycled) != 1 {
		t.Error("the old copy wasn't set aside")
	}
	b, _ = st.Book(ctx, b.ID)
	if b.AudiobookFile == nil || b.AudiobookFile.Path != folder || b.AudiobookFile.Size != 300 {
		t.Fatalf("book %+v", b.AudiobookFile)
	}
}

func TestBookReleaseWithNothingInIt(t *testing.T) {
	if wants(store.Ebook) == wants("movie") || wants(store.Audiobook) == wants(store.Ebook) || wants("") != "" {
		t.Fatal("kinds")
	}
}
