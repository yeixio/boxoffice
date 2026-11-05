// Package unpack extracts the archives a release is posted in: RAR (one
// file or many volumes, old .r00 style or .partNN), 7-Zip and ZIP.
package unpack

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// ErrPassword means the archive needs a password, and the one given (if
// any) didn't open it.
var ErrPassword = errors.New("the archive needs a password")

var (
	rePart    = regexp.MustCompile(`(?i)\.part0*(\d+)\.rar$`)
	reOldVol  = regexp.MustCompile(`(?i)\.(r\d\d|s\d\d)$`)
	re7zSplit = regexp.MustCompile(`(?i)\.7z\.0*(\d+)$`)
	reZipVol  = regexp.MustCompile(`(?i)\.z\d\d$`)
	reNumVol  = regexp.MustCompile(`\.0*(\d+)$`)
)

// Archive is an archive to extract, by its first volume.
type Archive struct {
	Path string
	Kind string // rar, 7z or zip
}

// Find lists the archives among files (names in dir), by first volume.
func Find(dir string, files []string) []Archive {
	var out []Archive
	for _, f := range files {
		low := strings.ToLower(f)
		p := filepath.Join(dir, f)
		switch {
		case strings.HasSuffix(low, ".rar"):
			if m := rePart.FindStringSubmatch(f); m != nil && m[1] != "1" {
				continue
			}
			out = append(out, Archive{p, "rar"})
		case strings.HasSuffix(low, ".7z"):
			out = append(out, Archive{p, "7z"})
		case re7zSplit.MatchString(f):
			if re7zSplit.FindStringSubmatch(f)[1] == "1" {
				out = append(out, Archive{p, "7z"})
			}
		case strings.HasSuffix(low, ".zip"):
			out = append(out, Archive{p, "zip"})
		}
	}
	// Old-style sets with a .r00 but no .rar, as some posts are renamed:
	// any file that starts a RAR is the first volume.
	if !slices.ContainsFunc(out, func(a Archive) bool { return a.Kind == "rar" }) {
		for _, f := range files {
			if strings.HasSuffix(strings.ToLower(f), ".r00") || reNumVol.MatchString(f) && reNumVol.FindStringSubmatch(f)[1] == "1" {
				if isRAR(filepath.Join(dir, f)) {
					out = append(out, Archive{filepath.Join(dir, f), "rar"})
					break
				}
			}
		}
	}
	return out
}

// IsVolume says whether a file is part of an archive (any volume), so it
// can be cleaned up after extracting.
func IsVolume(name string) bool {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".rar") || reOldVol.MatchString(name) || strings.HasSuffix(low, ".7z") ||
		re7zSplit.MatchString(name) || strings.HasSuffix(low, ".zip") || reZipVol.MatchString(name)
}

func isRAR(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 7)
	io.ReadFull(f, b)
	return string(b[:6]) == "Rar!\x1a\x07"
}

// Result is what extracting an archive did.
type Result struct {
	Files   []string // extracted, relative to the folder
	Volumes []string // the archive's own files, to delete after
}

// Extract extracts an archive into dir. Names inside that would land
// outside dir are refused, links are skipped, and existing files are
// replaced.
func Extract(ctx context.Context, a Archive, dir, password string) (Result, error) {
	switch a.Kind {
	case "rar":
		return extractRAR(ctx, a.Path, dir, password)
	case "7z":
		return extract7z(ctx, a.Path, dir, password)
	case "zip":
		return extractZip(ctx, a.Path, dir)
	}
	return Result{}, fmt.Errorf("can't extract %s archives", a.Kind)
}

// target is where an archive's entry goes, or "" when it would go outside
// dir.
func target(dir, name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	clean := filepath.Clean("/" + name)
	if clean == "/" {
		return ""
	}
	return filepath.Join(dir, clean)
}

func write(ctx context.Context, dir, name string, mode fs.FileMode, r io.Reader) (string, error) {
	p := target(dir, name)
	if p == "" {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".boxoffice-part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, ctxReader{ctx, r})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(dir, p)
	return rel, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func passwordErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "password") || strings.Contains(s, "encrypt") || errors.Is(err, rardecode.ErrBadPassword)
}

func extractRAR(ctx context.Context, path, dir, password string) (Result, error) {
	var opts []rardecode.Option
	if password != "" {
		opts = append(opts, rardecode.Password(password))
	}
	rc, err := rardecode.OpenReader(path, opts...)
	if err != nil {
		if passwordErr(err) {
			return Result{}, ErrPassword
		}
		return Result{}, err
	}
	defer rc.Close()
	var res Result
	for {
		h, err := rc.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if passwordErr(err) {
				return res, ErrPassword
			}
			return res, err
		}
		if h.IsDir || h.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		rel, err := write(ctx, dir, h.Name, h.Mode(), rc)
		if err != nil {
			if passwordErr(err) {
				return res, ErrPassword
			}
			return res, fmt.Errorf("extract %s: %w", h.Name, err)
		}
		if rel != "" {
			res.Files = append(res.Files, rel)
		}
	}
	res.Volumes = rc.Volumes()
	return res, nil
}

func extract7z(ctx context.Context, path, dir, password string) (Result, error) {
	var rc *sevenzip.ReadCloser
	var err error
	if password != "" {
		rc, err = sevenzip.OpenReaderWithPassword(path, password)
	} else {
		rc, err = sevenzip.OpenReader(path)
	}
	if err != nil {
		if passwordErr(err) {
			return Result{}, ErrPassword
		}
		return Result{}, err
	}
	defer rc.Close()
	var res Result
	for _, f := range rc.File {
		if f.FileInfo().IsDir() || f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		r, err := f.Open()
		if err != nil {
			if passwordErr(err) {
				return res, ErrPassword
			}
			return res, err
		}
		rel, err := write(ctx, dir, f.Name, f.Mode(), r)
		r.Close()
		if err != nil {
			if passwordErr(err) {
				return res, ErrPassword
			}
			return res, fmt.Errorf("extract %s: %w", f.Name, err)
		}
		if rel != "" {
			res.Files = append(res.Files, rel)
		}
	}
	res.Volumes = rc.Volumes()
	return res, nil
}

func extractZip(ctx context.Context, path, dir string) (Result, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Result{}, err
	}
	defer zr.Close()
	var res Result
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if f.Flags&0x1 != 0 {
			return res, ErrPassword
		}
		r, err := f.Open()
		if err != nil {
			return res, err
		}
		rel, err := write(ctx, dir, f.Name, f.Mode(), r)
		r.Close()
		if err != nil {
			return res, fmt.Errorf("extract %s: %w", f.Name, err)
		}
		if rel != "" {
			res.Files = append(res.Files, rel)
		}
	}
	res.Volumes = []string{path}
	return res, nil
}
