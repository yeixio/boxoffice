package downloader

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

// Ebook formats, best first: the one an ebook download brings in when it
// holds several.
var ebookOrder = []string{".epub", ".kepub", ".azw3", ".kfx", ".mobi", ".azw", ".pdf", ".fb2", ".djvu", ".lit", ".pdb"}

// EbookRank says how good an ebook format is, higher being better; 0 is
// not an ebook.
func EbookRank(ext string) int {
	i := slices.Index(ebookOrder, strings.ToLower(ext))
	if i < 0 {
		return 0
	}
	return len(ebookOrder) - i
}

// BookFolder is where a book goes in a root folder:
// "<root>/<Author>/<Title> (<Year>)", the layout Readarr, Calibre-Web's
// imports and Audiobookshelf all read.
func BookFolder(root string, a store.Author, b store.Book) string {
	return filepath.Join(root, cleanFileName(a.Name), fileName(b.Title, b.Year))
}

// bookRoot is the root folder a book goes in: the one that has its
// author's folder already, or else the first.
func (li *LibraryImporter) bookRoot(ctx context.Context, format string, a store.Author) (string, error) {
	roots, err := li.Store.RootFolders(ctx, format)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("there's no %s root folder; add one in Settings", map[string]string{store.Ebook: "ebook", store.Audiobook: "audiobook"}[format])
	}
	for _, r := range roots {
		if st, err := os.Stat(filepath.Join(r.Path, cleanFileName(a.Name))); err == nil && st.IsDir() {
			return r.Path, nil
		}
	}
	return roots[0].Path, nil
}

// importBook moves an ebook or an audiobook into its author's folder. An
// ebook brings its best file; an audiobook brings all its audio, keeping
// the files' names so the chapters stay in order. A copy already there is
// set aside in the root folder's .recycle folder, never deleted.
func (li *LibraryImporter) importBook(ctx context.Context, d store.Download, dir string, files []string) (string, error) {
	b, err := li.Store.Book(ctx, d.TitleID)
	if err != nil {
		return "", fmt.Errorf("the book it was for is gone: %w", err)
	}
	a, err := li.Store.Author(ctx, b.AuthorID)
	if err != nil {
		return "", fmt.Errorf("the book's author is gone: %w", err)
	}
	root, err := li.bookRoot(ctx, d.Kind, a)
	if err != nil {
		return "", err
	}
	folder := BookFolder(root, a, b)
	if err := checkLibrary(root, folder); err != nil {
		return "", err
	}
	if d.Kind == store.Ebook {
		return li.importEbook(ctx, b, root, folder, dir, files)
	}
	return li.importAudiobook(ctx, b, root, folder, dir, files)
}

func (li *LibraryImporter) importEbook(ctx context.Context, b store.Book, root, folder, dir string, files []string) (string, error) {
	type found struct {
		name string
		rank int
		size int64
	}
	var list []found
	for _, f := range files {
		r := EbookRank(filepath.Ext(f))
		if r == 0 {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		list = append(list, found{f, r, st.Size()})
	}
	if len(list) == 0 {
		return "", errors.New("there's no ebook file in it")
	}
	slices.SortFunc(list, func(x, y found) int {
		if c := cmp.Compare(y.rank, x.rank); c != 0 {
			return c
		}
		return cmp.Compare(y.size, x.size)
	})
	best := list[0].name
	dest := filepath.Join(folder, fileName(b.Title, b.Year)+strings.ToLower(filepath.Ext(best)))
	if err := li.place(ctx, dir, best, dest, root, folder, store.Ebook, b.ID, 0, b.EbookFile); err != nil {
		return "", err
	}
	return dest, nil
}

func (li *LibraryImporter) importAudiobook(ctx context.Context, b store.Book, root, folder, dir string, files []string) (string, error) {
	var tracks []string
	for _, f := range files {
		if screen.IsAudio(f) {
			tracks = append(tracks, f)
		}
	}
	if len(tracks) == 0 {
		return "", errors.New("there's no audio in it")
	}
	slices.Sort(tracks)
	if err := os.MkdirAll(filepath.Dir(folder), 0o755); err != nil {
		return "", err
	}
	// The copy there now goes aside whole, so tracks of two copies never
	// mix; and it comes back if the new one can't be moved in.
	aside, err := arrapi.RecycleTo(root, folder)
	if err != nil {
		return "", fmt.Errorf("couldn't set aside the copy already at %s: %w", folder, err)
	}
	undo := func() {
		os.RemoveAll(folder)
		if aside != "" {
			if _, serr := os.Lstat(folder); errors.Is(serr, fs.ErrNotExist) {
				rename(aside, folder)
			}
		}
	}
	// One file is named for the book; several keep their names (and
	// their CD1, CD2 folders), which put the chapters in order.
	var total int64
	for _, t := range tracks {
		dest := filepath.Join(folder, filepath.FromSlash(t))
		if len(tracks) == 1 {
			dest = filepath.Join(folder, fileName(b.Title, b.Year)+strings.ToLower(filepath.Ext(t)))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			undo()
			return "", err
		}
		if err := move(filepath.Join(dir, t), dest); err != nil {
			var left *sourceLeftError
			if !errors.As(err, &left) {
				undo()
				return "", err
			}
		}
		if st, err := os.Stat(dest); err == nil {
			total += st.Size()
		}
	}
	// A cover that came with it goes along.
	for _, f := range files {
		low := strings.ToLower(filepath.Base(f))
		if low == "cover.jpg" || low == "cover.png" || low == "folder.jpg" {
			dest := filepath.Join(folder, low)
			if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
				move(filepath.Join(dir, f), dest)
			}
			break
		}
	}
	if _, err := li.Store.AddFile(ctx, store.File{Kind: store.Audiobook, OwnerID: b.ID, Path: folder, Size: total}); err != nil {
		return "", err
	}
	// The copy it replaces, if Box Office had it somewhere else in the
	// root folder, is set aside too.
	if old := b.AudiobookFile; old != nil && filepath.Clean(old.Path) != filepath.Clean(folder) {
		if rel, err := filepath.Rel(filepath.Clean(root), old.Path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			if err := arrapi.Recycle(root, old.Path); err != nil {
				return "", fmt.Errorf("moved in, but couldn't set the old copy aside: %w", err)
			}
		}
	}
	return folder, nil
}
