package downloader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

// AlbumFolder is where an album goes: "<artist folder>/<Title> (<Year>)",
// the layout Lidarr uses, and Matinee, Plex and Jellyfin read.
func AlbumFolder(a store.Artist, b store.Album) string {
	y := 0
	if len(b.Released) >= 4 {
		fmt.Sscanf(b.Released[:4], "%d", &y)
	}
	return filepath.Join(a.Path, fileName(b.Title, y))
}

// What comes along with an album's tracks: covers, cue sheets and rip logs.
var albumExtras = []string{".jpg", ".jpeg", ".png", ".cue", ".log"}

// importAlbum moves an album's tracks into its folder in the artist's
// folder, keeping their names (and CD1, CD2 folders) so they stay in
// order, with its covers, cue sheets and logs. An album there already is
// set aside whole in the root folder's .recycle folder, never deleted, and
// comes back if the new one can't be moved in.
func (li *LibraryImporter) importAlbum(ctx context.Context, d store.Download, dir string, files []string) (string, error) {
	b, err := li.Store.Album(ctx, d.TitleID)
	if err != nil {
		return "", fmt.Errorf("the album it was for is gone: %w", err)
	}
	a, err := li.Store.Artist(ctx, b.ArtistID)
	if err != nil {
		return "", fmt.Errorf("the album's artist is gone: %w", err)
	}
	folder := AlbumFolder(a, b)
	if err := checkLibrary(a.RootFolder, folder); err != nil {
		return "", err
	}
	var tracks, extras []string
	for _, f := range files {
		switch {
		case screen.IsAudio(f):
			tracks = append(tracks, f)
		case slices.Contains(albumExtras, strings.ToLower(filepath.Ext(f))):
			extras = append(extras, f)
		}
	}
	if len(tracks) == 0 {
		return "", errors.New("there's no audio in it")
	}
	slices.Sort(tracks)
	// The release's own folder is left out: the album's folder takes its
	// place.
	prefix := commonDir(tracks)
	if err := os.MkdirAll(filepath.Dir(folder), 0o755); err != nil {
		return "", err
	}
	aside, err := arrapi.RecycleTo(a.RootFolder, folder)
	if err != nil {
		return "", fmt.Errorf("couldn't set aside the album already at %s: %w", folder, err)
	}
	undo := func() {
		os.RemoveAll(folder)
		if aside != "" {
			if _, serr := os.Lstat(folder); errors.Is(serr, fs.ErrNotExist) {
				rename(aside, folder)
			}
		}
	}
	var total int64
	for _, t := range tracks {
		dest := filepath.Join(folder, filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(t), prefix)))
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
	for _, f := range extras {
		rel := filepath.ToSlash(f)
		if !strings.HasPrefix(rel, prefix) {
			continue
		}
		dest := filepath.Join(folder, filepath.FromSlash(strings.TrimPrefix(rel, prefix)))
		if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
			if os.MkdirAll(filepath.Dir(dest), 0o755) == nil {
				move(filepath.Join(dir, f), dest)
			}
		}
	}
	if _, err := li.Store.AddFile(ctx, store.File{Kind: store.AlbumKind, OwnerID: b.ID, Path: folder, Size: total}); err != nil {
		return "", err
	}
	// The copy it replaces, if Box Office had it in another folder of the
	// artist's, is set aside too.
	if old := b.File; old != nil && filepath.Clean(old.Path) != filepath.Clean(folder) {
		if rel, err := filepath.Rel(filepath.Clean(a.Path), old.Path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			if err := arrapi.Recycle(a.RootFolder, old.Path); err != nil {
				return "", fmt.Errorf("moved in, but couldn't set the old copy aside: %w", err)
			}
		}
	}
	return folder, nil
}

// commonDir is the folder every one of files (slash-separated) is in, with
// a trailing slash, or "" when they're not all in one. CD1 and CD2 folders
// of a two-disc album aren't left out: only a folder all of them share.
func commonDir(files []string) string {
	if len(files) == 0 {
		return ""
	}
	dir := path.Dir(filepath.ToSlash(files[0]))
	for dir != "." && dir != "/" {
		p := dir + "/"
		if !slices.ContainsFunc(files, func(f string) bool { return !strings.HasPrefix(filepath.ToSlash(f), p) }) {
			if isDisc(path.Base(dir)) {
				dir = path.Dir(dir)
				continue
			}
			return p
		}
		dir = path.Dir(dir)
	}
	return ""
}

var reDisc = regexp.MustCompile(`(?i)^(cd|disc|disk)\s*\d+$`)

func isDisc(name string) bool { return reDisc.MatchString(strings.TrimSpace(name)) }

// ---- albums on disk ----

var (
	reTag      = regexp.MustCompile(`\s*[\(\[\{][^\)\]\}]*[\)\]\}]\s*$`)
	reLeadYear = regexp.MustCompile(`^\s*(19|20)\d{2}\s*[-.–]\s*`)
	reNonWord  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

func norm(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", "and"))
	return strings.TrimSpace(reNonWord.ReplaceAllString(s, " "))
}

// albumKey is what an album's folder name is matched on: its title without
// a year, a format or an edition in brackets, an "Artist - " in front, or
// a year in front, as in "1997 - OK Computer".
func albumKey(name, artist string) string {
	for {
		t := reTag.ReplaceAllString(name, "")
		if t == name || t == "" {
			break
		}
		name = t
	}
	if who, title, ok := strings.Cut(name, " - "); ok && norm(who) == norm(artist) {
		name = title
	}
	name = reLeadYear.ReplaceAllString(name, "")
	return norm(name)
}

// FindAlbums looks in an artist's folder for albums there already, such as
// the ones Lidarr got, and counts each one found as the album's copy, so
// Box Office doesn't download it again. It only looks: nothing is moved,
// renamed or deleted. It says how many it found.
func FindAlbums(ctx context.Context, st *store.Store, a store.Artist) (int, error) {
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return 0, nil // no folder yet: nothing's there
	}
	albums, err := st.Albums(ctx, a.ID)
	if err != nil {
		return 0, err
	}
	byKey := map[string][]int{}
	for i, b := range albums {
		k := albumKey(b.Title, a.Name)
		byKey[k] = append(byKey[k], i)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		at := byKey[albumKey(e.Name(), a.Name)]
		// Of albums of the same name, the first without a copy takes it:
		// a studio album before a single of the same name.
		slices.SortStableFunc(at, func(x, y int) int { return typeOrder(albums[x]) - typeOrder(albums[y]) })
		i := slices.IndexFunc(at, func(i int) bool { return albums[i].File == nil })
		if i < 0 {
			continue
		}
		b := &albums[at[i]]
		p := filepath.Join(a.Path, e.Name())
		size := audioSize(p)
		if size == 0 {
			continue
		}
		f, err := st.AddFile(ctx, store.File{Kind: store.AlbumKind, OwnerID: b.ID, Path: p, Size: size})
		if errors.Is(err, store.ErrExists) {
			continue // the folder is another album's
		}
		if err != nil {
			return found, err
		}
		b.File = &f
		found++
	}
	return found, nil
}

func typeOrder(b store.Album) int {
	if len(b.SecondaryTypes) > 0 {
		return 3
	}
	switch b.Type {
	case "Album":
		return 0
	case "EP":
		return 1
	case "Single":
		return 2
	}
	return 4
}

// audioSize is the size of the audio in a folder, 0 if there's none.
func audioSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(f string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() || !screen.IsAudio(f) {
			return nil
		}
		if info, err := de.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
