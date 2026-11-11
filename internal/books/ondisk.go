package books

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/namefix"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

var (
	reNonWord = regexp.MustCompile(`[^a-z0-9]+`)
	reYearTag = regexp.MustCompile(`\s*[\(\[]\d{4}[\)\]]\s*$`)
)

// norm is a name for comparing: without accents (so "García" on disk,
// whichever way its accent is written, is "Garcia" in a release), lower
// case, "and" for "&" and spaces for punctuation.
func norm(s string) string {
	s = namefix.Fold(strings.ReplaceAll(s, "&", "and"))
	return strings.TrimSpace(reNonWord.ReplaceAllString(s, " "))
}

// authorNames are the ways an author's folder can be named: "Brandon
// Sanderson" or "Sanderson, Brandon".
func authorNames(name string) []string {
	out := []string{norm(name)}
	if f := strings.Fields(name); len(f) > 1 {
		out = append(out, norm(f[len(f)-1]+" "+strings.Join(f[:len(f)-1], " ")))
	}
	return out
}

// TitleKey is what a book's title is matched on elsewhere, as by Move in.
func TitleKey(title string) string { return bookKey(title) }

// SameAuthor says whether two names are the same author's, "Ursula K. Le
// Guin" and "Le Guin, Ursula K." alike.
func SameAuthor(a, b string) bool {
	return slices.ContainsFunc(authorNames(a), func(n string) bool { return slices.Contains(authorNames(b), n) || n == norm(b) })
}

// bookKey is what a book's file or folder name is matched on: its title
// without a subtitle, a year or an extension.
func bookKey(name string) string {
	name = reYearTag.ReplaceAllString(name, "")
	main, _, _ := strings.Cut(name, ":")
	return norm(main)
}

// FindOnDisk looks in the root folders for an author's books that are
// there already, in the "<Author>/<Title> (<Year>)" layout Readarr,
// Calibre-Web and Audiobookshelf use, or as "<Author> - <Title>" right in
// the root folder, and counts each one found as the
// book's copy in that format, and as wanted. It only looks: nothing is
// moved, renamed or deleted. It says how many it found.
func (s *Service) FindOnDisk(ctx context.Context, authorID int64) (int, error) {
	a, err := s.Store.Author(ctx, authorID)
	if err != nil {
		return 0, err
	}
	list, err := s.Store.Books(ctx, authorID)
	if err != nil {
		return 0, err
	}
	byKey := map[string][]store.Book{}
	for _, b := range list {
		k := bookKey(b.Title)
		byKey[k] = append(byKey[k], b)
	}
	names := authorNames(a.Name)
	found := 0
	for _, format := range []string{store.Ebook, store.Audiobook} {
		if on, err := mediatypes.Enabled(ctx, s.Store, format); err != nil || !on {
			continue
		}
		roots, err := s.Store.RootFolders(ctx, format)
		if err != nil {
			return found, err
		}
		for _, r := range roots {
			entries, err := os.ReadDir(r.Path)
			if err != nil {
				continue
			}
			for _, e := range entries {
				var n int
				var err error
				if e.IsDir() && slices.Contains(names, norm(e.Name())) {
					n, err = s.findIn(ctx, filepath.Join(r.Path, e.Name()), format, byKey)
				} else if who, title, ok := strings.Cut(e.Name(), " - "); ok && slices.Contains(names, norm(who)) {
					n, err = s.record(ctx, filepath.Join(r.Path, e.Name()), title, e.IsDir(), format, byKey)
				}
				found += n
				if err != nil {
					return found, err
				}
			}
		}
	}
	return found, nil
}

// findIn records the books in one author folder.
func (s *Service) findIn(ctx context.Context, dir, format string, byKey map[string][]store.Book) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	found := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n, err := s.record(ctx, filepath.Join(dir, e.Name()), e.Name(), e.IsDir(), format, byKey)
		found += n
		if err != nil {
			return found, err
		}
	}
	return found, nil
}

// record counts the file or folder at p, named for a book's title, as
// that book's copy in a format, if it's one of the author's books with no
// copy yet. It says whether it did (1 or 0).
func (s *Service) record(ctx context.Context, p, name string, dir bool, format string, byKey map[string][]store.Book) (int, error) {
	if !dir {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	books := byKey[bookKey(name)]
	if len(books) == 0 {
		return 0, nil
	}
	b := books[0]
	if b.File(format) != nil {
		return 0, nil
	}
	path, size := copyAt(p, dir, format)
	if path == "" {
		return 0, nil
	}
	if _, err := s.Store.AddFile(ctx, store.File{Kind: format, OwnerID: b.ID, Path: path, Size: size}); err != nil {
		if errors.Is(err, store.ErrExists) {
			return 0, nil
		}
		return 0, err
	}
	if !b.Wants(format) {
		if err := s.Store.SetBookWanted(ctx, b.ID, format, true); err != nil {
			return 0, err
		}
	}
	// byKey holds copies, so a second folder for the same book is skipped.
	books[0] = b
	if format == store.Ebook {
		books[0].EbookFile = &store.File{Path: path}
	} else {
		books[0].AudiobookFile = &store.File{Path: path}
	}
	return 1, nil
}

// copyAt says what at p is a book's copy in a format: an ebook's best file,
// or an audiobook's folder (or its one file), with its size; "" if none.
func copyAt(p string, dir bool, format string) (string, int64) {
	if !dir {
		st, err := os.Stat(p)
		if err != nil {
			return "", 0
		}
		if format == store.Ebook && screen.IsEbook(p) || format == store.Audiobook && screen.IsAudio(p) {
			return p, st.Size()
		}
		return "", 0
	}
	best, rank, total := "", 0, int64(0)
	var bestSize int64
	filepath.WalkDir(p, func(f string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return nil
		}
		if format == store.Audiobook {
			if screen.IsAudio(f) {
				total += info.Size()
			}
			return nil
		}
		if r := downloader.EbookRank(filepath.Ext(f)); r > rank || r == rank && r > 0 && info.Size() > bestSize {
			best, rank, bestSize = f, r, info.Size()
		}
		return nil
	})
	if format == store.Audiobook {
		if total == 0 {
			return "", 0
		}
		return p, total
	}
	return best, bestSize
}
