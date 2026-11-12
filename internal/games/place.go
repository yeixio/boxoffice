package games

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
)

// ErrHave says a game being added is in its system's folder already.
var ErrHave = errors.New("it's in the games folder already")

// Identify says which system a file is a game for: a console's file by
// its extension, an archive by what's in it, and a CHD is MAME's. An
// archive holding no console's games, and no video, music or books, is
// taken to be a MAME set.
func Identify(p string) (gamedat.System, bool) {
	name := filepath.Base(p)
	if sys, ok := gamedat.SystemByExt(name); ok {
		return sys, true
	}
	if strings.EqualFold(filepath.Ext(name), ".chd") {
		return gamedat.LookupSystem("mame")
	}
	if !gamedat.IsArchive(name) {
		return gamedat.System{}, false
	}
	files, err := gamedat.ListArchive(p)
	if err != nil || len(files) == 0 {
		return gamedat.System{}, false
	}
	for _, f := range files {
		if sys, ok := gamedat.SystemByExt(f.Name); ok {
			return sys, true
		}
	}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".mkv", ".mp4", ".avi", ".m4v", ".mov", ".mp3", ".flac", ".m4a", ".m4b", ".ogg", ".epub", ".mobi", ".azw3", ".pdf", ".cbz", ".cbr",
			".rar", ".exe", ".iso", ".bin", ".cue", ".txt", ".nfo", ".jpg", ".png":
			// .bin and .cue are discs, which Box Office doesn't know yet.
			return gamedat.System{}, false
		}
	}
	return gamedat.LookupSystem("mame")
}

// Folder is the folder a system's games go in: the one Box Office already
// knows for it, or else a folder named for it in the first games root
// folder.
func (s *Service) Folder(ctx context.Context, key string) (string, error) {
	sys, ok := gamedat.LookupSystem(key)
	if !ok {
		return "", fmt.Errorf("Box Office doesn't know a system called %q", key)
	}
	known, err := s.Store.GameSystems(ctx)
	if err != nil {
		return "", err
	}
	for _, k := range known {
		if k.System == key && !k.Locked {
			return k.Path, nil
		}
	}
	for _, k := range known {
		if k.System == key {
			return "", fmt.Errorf("%s is set to Do not touch; turn that off to add games to it", sys.Name)
		}
	}
	roots, err := s.Store.RootFolders(ctx, mediatypes.Game)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", errors.New("add a games root folder in Settings first")
	}
	root := roots[0].Path
	// A root folder that's one system's folder can't take another's.
	if found, _, err := Detect(root); err == nil && len(found) == 1 && found[0].path == root {
		if found[0].sys.Key == key {
			return root, nil
		}
		return "", fmt.Errorf("the games root folder %s holds only %s games; add a games root folder with a folder for each system", root, found[0].sys.Name)
	}
	return filepath.Join(root, key), nil
}

// Add copies a game into its system's folder, and says where it went. A
// MAME CHD goes in the folder named for its set. The file it's copied
// from is left as it is.
func (s *Service) Add(ctx context.Context, key, src string) (string, error) {
	dir, err := s.Folder(ctx, key)
	if err != nil {
		return "", err
	}
	name := filepath.Base(src)
	if key == "mame" && strings.EqualFold(filepath.Ext(name), ".chd") {
		dir = filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	dst := filepath.Join(dir, name)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	if have, err := os.Stat(dst); err == nil {
		if have.Size() == st.Size() {
			return dst, ErrHave
		}
		return dst, fmt.Errorf("another file called %s is in %s already", name, dir)
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return "", writeErr(err, dir)
	}
	tmp := filepath.Join(dir, "."+name+".part")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return "", writeErr(err, dir)
	}
	_, err = io.Copy(out, readCtx{ctx, in})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return "", writeErr(err, dir)
	}
	return dst, nil
}

func writeErr(err error, dir string) error {
	if errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("Box Office can't write to %s: the games folder is read-only for it", dir)
	}
	return err
}

// readCtx stops a copy when its context ends.
type readCtx struct {
	ctx context.Context
	r   io.Reader
}

func (r readCtx) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
