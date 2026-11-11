package unpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// unrarPath finds RARLAB's unrar, the reference for RAR archives. When
// rardecode can't read an archive (an old or unusual one), unrar is
// tried before giving up. It's in the Box Office image; without it, only
// rardecode is used. Tests swap it.
var unrarPath = func() string {
	p, err := exec.LookPath("unrar")
	if err != nil {
		return ""
	}
	return p
}

// HaveUnrar says whether unrar is there to fall back on.
func HaveUnrar() bool { return unrarPath() != "" }

// extractUnrar extracts a RAR archive with unrar. The volumes are shown to
// it under names in order (vol.rar, vol.r00… or vol.part0001.rar…), as
// unrar finds each next volume by name, through links in a folder of its
// own; the files themselves are never renamed. It extracts into a
// folder of its own inside dir, and only plain files are moved out of it,
// so nothing lands outside dir and no links are made there.
func extractUnrar(ctx context.Context, bin string, a Archive, dir, password string) (Result, error) {
	vols := a.Volumes
	if len(vols) == 0 {
		vols = []string{a.Path}
	}
	first := vols[0]
	if len(vols) > 1 || hashedStem(first) {
		// Where links can't be made, unrar gets the first volume as it
		// is, and finds the rest by their names if they say.
		if links, err := linkVolumes(vols); err == nil {
			defer os.RemoveAll(links)
			first = filepath.Join(links, unrarNames(vols)[0])
		}
	}
	out, err := os.MkdirTemp(dir, ".boxoffice-unrar-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(out)
	pw := "-p-"
	if password != "" {
		pw = "-p" + password
	}
	// x: with paths; -o+: replace; -y: yes to all; -idcdp: quiet.
	cmd := exec.CommandContext(ctx, bin, "x", "-o+", "-y", "-idcdp", pw, "--", first, out+string(filepath.Separator))
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		msg := lastLine(stderr.String())
		var ee *exec.ExitError
		// 11: wrong password (RAR 5); older ones say so.
		if errors.As(err, &ee) && ee.ExitCode() == 11 || strings.Contains(strings.ToLower(msg), "password") {
			return Result{}, ErrPassword
		}
		if msg == "" {
			msg = err.Error()
		}
		return Result{}, errors.New(msg)
	}
	var res Result
	err = filepath.WalkDir(out, func(p string, de fs.DirEntry, err error) error {
		if err != nil || !de.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		to := target(dir, rel)
		if to == "" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(p, to); err != nil {
			return err
		}
		res.Files = append(res.Files, rel)
		return nil
	})
	if err != nil {
		return res, err
	}
	res.Volumes = vols
	return res, nil
}

// linkVolumes makes a folder of links to a set's volumes, under the names
// unrar expects, and returns it.
func linkVolumes(vols []string) (string, error) {
	links, err := os.MkdirTemp("", "boxoffice-unrar-")
	if err != nil {
		return "", err
	}
	for i, name := range unrarNames(vols) {
		abs, err := filepath.Abs(vols[i])
		if err == nil {
			err = os.Symlink(abs, filepath.Join(links, name))
		}
		if err != nil {
			os.RemoveAll(links)
			return "", err
		}
	}
	return links, nil
}

// unrarNames are the names unrar is shown a set's volumes by: old-style
// (.rar, .r00, .r01…) when the first volume's header says so, else
// .part0001.rar and on.
func unrarNames(vols []string) []string {
	old := false
	if v, ok := probeRAR(vols[0]); ok && v.ver == 4 {
		old = !v.newNames
	}
	width := max(4, len(fmt.Sprint(len(vols))))
	names := make([]string, len(vols))
	name := "vol.rar"
	for i := range vols {
		if old {
			if i > 0 {
				name = nextOldName(name)
			}
			names[i] = name
		} else {
			names[i] = fmt.Sprintf("vol.part%0*d.rar", width, i+1)
		}
	}
	return names
}

// hashedStem says whether a name, without its extension, is a hash, as
// unrar may then look for a next volume by it.
func hashedStem(p string) bool {
	b := filepath.Base(p)
	b = strings.TrimSuffix(b, filepath.Ext(b))
	if len(b) < 16 {
		return false
	}
	for _, c := range strings.ToLower(b) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "Total errors") {
			return l
		}
	}
	return ""
}
