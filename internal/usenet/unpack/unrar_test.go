package unpack

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func needUnrar(t *testing.T) {
	t.Helper()
	if !HaveUnrar() {
		t.Skip("unrar isn't installed")
	}
}

// When rardecode can't read an archive, unrar is tried, and what it
// extracts lands in the folder like rardecode's would.
func TestUnrarFallback(t *testing.T) {
	needUnrar(t)
	decode = func(context.Context, Archive, string, string) (Result, error) {
		return Result{}, errors.New("rardecode: unexpected end of archive")
	}
	t.Cleanup(func() { decode = decodeRAR })
	for _, c := range []struct {
		name  string
		names []string
		new   bool
	}{
		{"old-style names under hashes", []string{"6f2d940f3fa058e9.rar", "ee75c6d4a1b2c3d4.r00", "0a1b2c3d4e5f6071.r01"}, false},
		{"part names", []string{"m.part1.rar", "m.part2.rar", "m.part3.rar"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			data := bytes.Repeat([]byte("Earth 2 "), 5000)
			writeRAR4Named(t, dir, "earth.2.avi", data, c.names, c.new)
			arcs := Find(dir, c.names)
			if len(arcs) != 1 {
				t.Fatalf("found %+v", arcs)
			}
			res, err := Extract(context.Background(), arcs[0], dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "earth.2.avi")); !bytes.Equal(b, data) {
				t.Fatal("episode differs")
			}
			if len(res.Files) != 1 || res.Files[0] != "earth.2.avi" || len(res.Volumes) != len(c.names) {
				t.Fatalf("result %+v", res)
			}
			// Its own folder is gone.
			if m, _ := filepath.Glob(filepath.Join(dir, ".boxoffice-unrar-*")); len(m) > 0 {
				t.Fatalf("left %v", m)
			}
		})
	}
}

// When unrar can't either, both say why.
func TestUnrarFallbackFails(t *testing.T) {
	needUnrar(t)
	decode = func(context.Context, Archive, string, string) (Result, error) {
		return Result{}, errors.New("rardecode: unexpected end of archive")
	}
	t.Cleanup(func() { decode = decodeRAR })
	dir := t.TempDir()
	names := []string{"a.rar", "a.r00"}
	writeRAR4Named(t, dir, "x.avi", bytes.Repeat([]byte("x"), 9000), names, false)
	// Damage the second volume's data.
	p := filepath.Join(dir, "a.r00")
	b, _ := os.ReadFile(p)
	copy(b[len(b)-200:], bytes.Repeat([]byte{0}, 100))
	os.WriteFile(p, b, 0o644)
	_, err := Extract(context.Background(), Find(dir, names)[0], dir, "")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("unrar couldn't either")) {
		t.Fatalf("err %v", err)
	}
}

// The real thing, with no stand-in: the testdata set (RAR 5) still opens.
func TestUnrarDirect(t *testing.T) {
	needUnrar(t)
	dir := copyTestdata(t, "multi.part1.rar", "multi.part2.rar", "multi.part3.rar")
	a := Find(dir, []string{"multi.part1.rar", "multi.part2.rar", "multi.part3.rar"})[0]
	if _, err := extractUnrar(context.Background(), unrarPath(), a, dir, ""); err != nil {
		t.Fatal(err)
	}
	if got := sum(t, filepath.Join(dir, "movie.mkv")); got != movieMD5 {
		t.Fatalf("md5 %s", got)
	}
}
