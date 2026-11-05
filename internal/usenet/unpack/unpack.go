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
	"time"

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
)

// Archive is an archive to extract, by its first volume.
type Archive struct {
	Path string
	Kind string // rar, 7z or zip
	// Volumes are a RAR set's files in order, found by their headers, as
	// obfuscated posts name them anything.
	Volumes []string
}

// Find lists the archives among files (names in dir), by first volume.
// RAR archives are found by content, whatever they're called; 7-Zip and
// ZIP by name.
func Find(dir string, files []string) []Archive {
	var out []Archive
	rars := map[string]bool{}
	for _, s := range findRARSets(dir, files) {
		a := Archive{Path: s[0].path, Kind: "rar"}
		for _, v := range s {
			a.Volumes = append(a.Volumes, v.path)
			rars[v.path] = true
		}
		out = append(out, a)
	}
	for _, f := range files {
		low := strings.ToLower(f)
		p := filepath.Join(dir, f)
		switch {
		case strings.HasSuffix(low, ".rar"):
			// Named .rar but its headers didn't read as one: extracting it
			// says what's wrong.
			if !rars[p] && (rePart.FindStringSubmatch(f) == nil || rePart.FindStringSubmatch(f)[1] == "1") {
				out = append(out, Archive{Path: p, Kind: "rar"})
			}
		case strings.HasSuffix(low, ".7z"):
			out = append(out, Archive{Path: p, Kind: "7z"})
		case re7zSplit.MatchString(f):
			if re7zSplit.FindStringSubmatch(f)[1] == "1" {
				out = append(out, Archive{Path: p, Kind: "7z"})
			}
		case strings.HasSuffix(low, ".zip"):
			out = append(out, Archive{Path: p, Kind: "zip"})
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

// Result is what extracting an archive did.
type Result struct {
	Files   []string // extracted, relative to the folder
	Volumes []string // the archive's own files, to delete after
}

// Extract extracts an archive into dir. Names inside that would land
// outside dir are refused, links are skipped, and existing files are
// replaced.
func Extract(ctx context.Context, a Archive, dir, password string) (Result, error) {
	return ExtractWithProgress(ctx, a, dir, password, nil)
}

// ExtractWithProgress is Extract, telling progress of how many bytes of
// how many have been written out.
func ExtractWithProgress(ctx context.Context, a Archive, dir, password string, progress func(done, total int64)) (Result, error) {
	if progress != nil {
		ctx = context.WithValue(ctx, meterKey{}, &meter{tell: progress})
	}
	switch a.Kind {
	case "rar":
		return extractRAR(ctx, a, dir, password)
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
	n, err := c.r.Read(p)
	if m := meterOf(c.ctx); m != nil {
		m.add(int64(n))
	}
	return n, err
}

type meterKey struct{}

// meter counts the bytes written out of an archive.
type meter struct {
	done, total int64
	tell        func(done, total int64)
	last        time.Time
}

func meterOf(ctx context.Context) *meter {
	m, _ := ctx.Value(meterKey{}).(*meter)
	return m
}

func setTotal(ctx context.Context, n int64) {
	if m := meterOf(ctx); m != nil {
		m.total = n
		m.tell(0, n)
	}
}

func (m *meter) add(n int64) {
	m.done += n
	if now := time.Now(); now.Sub(m.last) > 250*time.Millisecond || m.done >= m.total {
		m.last = now
		m.tell(min(m.done, max(m.total, m.done)), max(m.total, m.done))
	}
}

func passwordErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "password") || strings.Contains(s, "encrypt") || errors.Is(err, rardecode.ErrBadPassword)
}

func extractRAR(ctx context.Context, a Archive, dir, password string) (Result, error) {
	var opts []rardecode.Option
	if password != "" {
		opts = append(opts, rardecode.Password(password))
	}
	path := a.Path
	var vfs *volumeFS
	if len(a.Volumes) > 0 {
		if err := checkSet(a.Volumes); err != nil {
			return Result{}, err
		}
		vfs = &volumeFS{paths: a.Volumes, width: max(4, len(fmt.Sprint(len(a.Volumes))))}
		opts = append(opts, rardecode.FileSystem(vfs))
		path = vfs.name(0)
	}
	if m := meterOf(ctx); m != nil {
		if list, err := rardecode.List(path, opts...); err == nil {
			var total int64
			for _, f := range list {
				if !f.IsDir {
					total += f.UnPackedSize
				}
			}
			setTotal(ctx, total)
		}
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
	for _, v := range rc.Volumes() {
		res.Volumes = append(res.Volumes, filepath.Join(filepath.Dir(a.Path), v))
	}
	if vfs != nil {
		// Every volume of the set, not just the ones read.
		res.Volumes = slices.Clone(a.Volumes)
	}
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
	if meterOf(ctx) != nil {
		var total int64
		for _, f := range rc.File {
			total += int64(f.UncompressedSize) //nolint:gosec
		}
		setTotal(ctx, total)
	}
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
	if meterOf(ctx) != nil {
		var total int64
		for _, f := range zr.File {
			total += int64(f.UncompressedSize64) //nolint:gosec
		}
		setTotal(ctx, total)
	}
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
