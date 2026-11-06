package migrate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

var errBooksOff = errors.New("turn on Ebooks or Audiobooks under Settings first: Readarr's authors come across into them")

// noProfiles are the apps whose quality profiles stay behind: Box
// Office's music and books choose their copies by settings of their own.
func noProfiles(app string) bool { return app == "lidarr" || app == "readarr" }

// typesOn checks the media types an app's titles go into are on.
func (m *Migrator) typesOn(ctx context.Context, app string) error {
	st := m.Catalog.Store
	switch app {
	case "lidarr":
		if on, err := mediatypes.Enabled(ctx, st, mediatypes.Music); err != nil || !on {
			return cmpErr(err, errMusicOff)
		}
	case "readarr":
		if m.Books == nil {
			return errors.New("Box Office's books aren't set up")
		}
		eb, err := mediatypes.Enabled(ctx, st, store.Ebook)
		if err != nil {
			return err
		}
		ab, err := mediatypes.Enabled(ctx, st, store.Audiobook)
		if err != nil {
			return err
		}
		if !eb && !ab {
			return errBooksOff
		}
	}
	return nil
}

func cmpErr(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}

// Readarr's authors, books and book files, as its API lists them.
type lAuthor struct {
	ID              int    `json:"id"`
	AuthorName      string `json:"authorName"`
	Path            string `json:"path"`
	RootFolderPath  string `json:"rootFolderPath"`
	Monitored       bool   `json:"monitored"`
	MonitorNewItems string `json:"monitorNewItems"` // all, none or new
	Statistics      struct {
		BookFileCount int `json:"bookFileCount"`
	} `json:"statistics"`
}

type lBook struct {
	ID        int    `json:"id"`
	AuthorID  int    `json:"authorId"`
	Title     string `json:"title"`
	Monitored bool   `json:"monitored"`
}

type lBookFile struct {
	BookID int    `json:"bookId"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}

// rootKind is the kind of root folder a title of the app at path goes in.
func (p *pending) rootKind(path string) string {
	if p.req.App != "readarr" {
		return rootKind[p.req.App]
	}
	if k, ok := p.rootKinds[filepath.Clean(path)]; ok {
		return k
	}
	return store.Ebook
}

// authorRoot is the Readarr root folder an author is in.
func (p *pending) authorRoot(a lAuthor) string {
	if a.RootFolderPath != "" {
		return filepath.Clean(a.RootFolderPath)
	}
	return filepath.Dir(filepath.Clean(a.Path))
}

func (p *pending) bookKind(legacyID int) string {
	if k, ok := p.bookKinds[legacyID]; ok {
		return k
	}
	return store.Ebook
}

// samplesPerRoot is how many authors' files are looked at to tell a root
// folder of ebooks from one of audiobooks.
const samplesPerRoot = 5

// bookKinds works out which of Readarr's root folders hold ebooks and
// which audiobooks, from the files of a few of the authors in each.
// Readarr doesn't say; a folder with no files yet is taken by its name.
func (m *Migrator) bookKinds(ctx context.Context, p *pending) error {
	p.rootKinds, p.kindOn = map[string]string{}, map[string]bool{}
	for _, k := range []string{store.Ebook, store.Audiobook} {
		on, err := mediatypes.Enabled(ctx, m.Catalog.Store, k)
		if err != nil {
			return err
		}
		p.kindOn[k] = on
	}
	for _, r := range p.roots {
		root := filepath.Clean(r.Path)
		ebooks, audio, looked := 0, 0, 0
		for _, a := range p.authors {
			if looked == samplesPerRoot || p.authorRoot(a) != root || a.Statistics.BookFileCount == 0 {
				continue
			}
			looked++
			var files []lBookFile
			if err := p.l.get(ctx, "/bookfile", map[string][]string{"authorId": {strconv.Itoa(a.ID)}}, &files); err != nil {
				return err
			}
			for _, f := range files {
				switch {
				case screen.IsEbook(f.Path):
					ebooks++
				case screen.IsAudio(f.Path):
					audio++
				}
			}
		}
		kind := store.Ebook
		if audio > ebooks || audio == ebooks && strings.Contains(strings.ToLower(root), "audio") {
			kind = store.Audiobook
		}
		// With nothing to go by, a folder goes where books are on.
		if audio == 0 && ebooks == 0 && !p.kindOn[kind] {
			kind = map[string]string{store.Ebook: store.Audiobook, store.Audiobook: store.Ebook}[kind]
		}
		p.rootKinds[root] = kind
	}
	return nil
}

var kindNames = map[string]string{store.Ebook: "ebooks", store.Audiobook: "audiobooks"}

// haveAuthor finds an author on the list by name.
func haveAuthor(list []store.Author, name string) (store.Author, bool) {
	i := slices.IndexFunc(list, func(a store.Author) bool { return books.SameAuthor(a.Name, name) })
	if i < 0 {
		return store.Author{}, false
	}
	return list[i], true
}

func (m *Migrator) previewAuthors(ctx context.Context, p *pending, plan *Plan, sample func(Sample)) {
	have, _ := m.Catalog.Store.Authors(ctx)
	off := map[string]int{}
	for _, a := range p.authors {
		plan.Titles.Total++
		kind := p.rootKind(p.authorRoot(a))
		if !p.kindOn[kind] {
			plan.Titles.Skipped++
			off[kind]++
			continue
		}
		if _, ok := haveAuthor(have, a.AuthorName); ok {
			plan.Titles.Already++
			continue
		}
		plan.Titles.New++
		sample(Sample{Title: a.AuthorName, Path: mapPath(p.req.PathMap, a.Path), HasFile: a.Statistics.BookFileCount > 0})
	}
	for _, k := range []string{store.Ebook, store.Audiobook} {
		if off[k] > 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s in root folders of %s, but %s are off. Turn on %s under Settings and connect again to bring them across.",
				count(off[k], "author is", "authors are"), kindNames[k], kindNames[k], strings.ToUpper(kindNames[k][:1])+kindNames[k][1:]))
		}
	}
	plan.Warnings = append(plan.Warnings, "Readarr's authors are looked up on Open Library by name, one a second, so moving in takes a while. One Open Library doesn't know is left out.")
}

// importAuthors puts Readarr's authors on the list, found on Open Library
// by name, with the books Readarr wants wanted, and the copies it has
// recorded where they are. Nothing on disk is moved, renamed or deleted.
func (m *Migrator) importAuthors(ctx context.Context, p *pending, pr *Progress, progress func(int, int)) error {
	st := m.Catalog.Store
	warn := func(s string) { m.update(func() { pr.Warnings = append(pr.Warnings, s) }) }
	p.bookIDs, p.bookKinds = map[int]int64{}, map[int]string{}
	for i, la := range p.authors {
		progress(i, len(p.authors))
		if err := ctx.Err(); err != nil {
			return err
		}
		kind := p.rootKind(p.authorRoot(la))
		if !p.kindOn[kind] {
			m.update(func() { pr.Imported.Skipped++ })
			continue // the preview warned
		}
		list, err := st.Authors(ctx)
		if err != nil {
			return err
		}
		a, isOld := haveAuthor(list, la.AuthorName)
		before := 0
		if isOld {
			if before, err = m.copies(ctx, a.ID); err != nil {
				return err
			}
		} else {
			key, err := m.findAuthor(ctx, la.AuthorName)
			if err != nil {
				m.update(func() { pr.Imported.Skipped++ })
				warn(fmt.Sprintf("%s: %v", la.AuthorName, err))
				continue
			}
			a, err = m.Books.Add(ctx, books.AddOptions{Key: key, Format: kind, New: la.Monitored && la.MonitorNewItems != "none"})
			if errors.Is(err, catalog.ErrExists) {
				a, err = st.AuthorByKey(ctx, key)
				isOld = true
				if err == nil {
					before, err = m.copies(ctx, a.ID)
				}
			}
			if err != nil {
				m.update(func() { pr.Imported.Skipped++ })
				warn(fmt.Sprintf("%s: %v", la.AuthorName, err))
				continue
			}
		}
		missing, err := m.authorBooks(ctx, p, la, a, kind)
		if err != nil {
			return fmt.Errorf("%s's books: %w", la.AuthorName, err)
		}
		if missing > 0 {
			warn(fmt.Sprintf("%s: Open Library doesn't list %s Readarr has, so %s left out", la.AuthorName,
				count(missing, "book", "books"), map[bool]string{true: "it was", false: "they were"}[missing == 1]))
		}
		// Books Readarr lists no files for, but that are in the root
		// folders, are found by their names.
		if _, err := m.Books.FindOnDisk(ctx, a.ID); err != nil {
			warn(fmt.Sprintf("%s: looking in the root folders: %v", la.AuthorName, err))
		}
		after, err := m.copies(ctx, a.ID)
		if err != nil {
			return err
		}
		m.update(func() {
			if isOld {
				pr.Imported.Already++
			} else {
				pr.Imported.New++
			}
			pr.Files += after - before
		})
	}
	progress(len(p.authors), len(p.authors))
	return nil
}

// copies counts an author's books in the library, in either format.
func (m *Migrator) copies(ctx context.Context, authorID int64) (int, error) {
	list, err := m.Catalog.Store.Books(ctx, authorID)
	n := 0
	for _, b := range list {
		if b.EbookFile != nil {
			n++
		}
		if b.AudiobookFile != nil {
			n++
		}
	}
	return n, err
}

// findAuthor looks an author up on Open Library by name.
func (m *Migrator) findAuthor(ctx context.Context, name string) (string, error) {
	found, err := m.Books.OpenLibrary.SearchAuthors(ctx, name)
	if err != nil {
		return "", err
	}
	best := -1
	for i, a := range found {
		if books.SameAuthor(a.Name, name) && (best < 0 || a.Works > found[best].Works) {
			best = i
		}
	}
	if best < 0 {
		return "", errors.New("Open Library doesn't know this author, so they were left out")
	}
	return found[best].Key, nil
}

// authorBooks wants the books Readarr wants, and records the copies it
// has. It says how many of Readarr's books aren't among the author's
// books on Open Library.
func (m *Migrator) authorBooks(ctx context.Context, p *pending, la lAuthor, a store.Author, kind string) (int, error) {
	st := m.Catalog.Store
	have, err := st.Books(ctx, a.ID)
	if err != nil {
		return 0, err
	}
	byKey := map[string]store.Book{}
	for _, b := range have {
		if _, ok := byKey[books.TitleKey(b.Title)]; !ok {
			byKey[books.TitleKey(b.Title)] = b
		}
	}
	matched := map[int]store.Book{}
	missing := 0
	for _, lb := range p.books {
		if lb.AuthorID != la.ID {
			continue
		}
		b, ok := byKey[books.TitleKey(lb.Title)]
		if !ok {
			missing++
			continue
		}
		matched[lb.ID] = b
		p.bookIDs[lb.ID], p.bookKinds[lb.ID] = b.ID, kind
		if lb.Monitored && !b.Wants(kind) {
			if err := st.SetBookWanted(ctx, b.ID, kind, true); err != nil {
				return missing, err
			}
		}
	}
	if la.Statistics.BookFileCount == 0 {
		return missing, nil
	}
	var files []lBookFile
	if err := p.l.get(ctx, "/bookfile", map[string][]string{"authorId": {strconv.Itoa(la.ID)}}, &files); err != nil {
		return missing, err
	}
	byBook := map[int][]lBookFile{}
	for _, f := range files {
		f.Path = mapPath(p.req.PathMap, f.Path)
		byBook[f.BookID] = append(byBook[f.BookID], f)
	}
	authorDir := filepath.Clean(mapPath(p.req.PathMap, la.Path))
	for legacy, b := range matched {
		if b.File(kind) != nil || len(byBook[legacy]) == 0 {
			continue
		}
		path, size := copyOf(kind, byBook[legacy], authorDir)
		if path == "" {
			continue
		}
		if _, err := st.AddFile(ctx, store.File{Kind: kind, OwnerID: b.ID, Path: path, Size: size}); err != nil {
			if errors.Is(err, store.ErrExists) {
				continue
			}
			return missing, err
		}
		if !b.Wants(kind) {
			if err := st.SetBookWanted(ctx, b.ID, kind, true); err != nil {
				return missing, err
			}
		}
	}
	return missing, nil
}

// copyOf is the copy a book's files make in a kind: an ebook's file, the
// first in the order ebooks are preferred, or an audiobook's folder (its
// one file when it has none of its own).
func copyOf(kind string, files []lBookFile, authorDir string) (string, int64) {
	if kind == store.Ebook {
		for _, ext := range []string{".epub", ".kepub", ".azw3", ".mobi", ".pdf", ""} {
			for _, f := range files {
				if ext == "" && screen.IsEbook(f.Path) || ext != "" && strings.EqualFold(filepath.Ext(f.Path), ext) {
					return f.Path, f.Size
				}
			}
		}
		return "", 0
	}
	var paths []string
	var total int64
	for _, f := range files {
		paths = append(paths, f.Path)
		total += f.Size
	}
	dir := downloader.AlbumDir(paths)
	if rel, err := filepath.Rel(authorDir, dir); dir != "" && err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return dir, total
	}
	if len(files) == 1 {
		return files[0].Path, total
	}
	return "", 0
}
