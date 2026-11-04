package unpack

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The testdata archives hold movie.mkv (25000 bytes) and readme.txt, made
// with RAR 7.00, p7zip 16.02 and Info-ZIP.
const movieMD5 = "b13af46c10781f6515b9a22230be2e7b"

func copyTestdata(t *testing.T, names ...string) string {
	dir := t.TempDir()
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, n), b, 0o644)
	}
	return dir
}

func sum(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func TestFind(t *testing.T) {
	got := Find("/d", []string{"a.part1.rar", "a.part2.rar", "a.part10.rar", "b.7z.001", "b.7z.002", "c.zip", "d.mkv", "e.rar"})
	want := []Archive{{"/d/a.part1.rar", "rar"}, {"/d/b.7z.001", "7z"}, {"/d/c.zip", "zip"}, {"/d/e.rar", "rar"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, v := range []string{"x.r00", "x.part2.rar", "x.7z.003", "x.z01"} {
		if !IsVolume(v) {
			t.Errorf("%s isn't a volume?", v)
		}
	}
	if IsVolume("x.mkv") {
		t.Error("mkv is a volume?")
	}
}

func TestRARVolumes(t *testing.T) {
	dir := copyTestdata(t, "multi.part1.rar", "multi.part2.rar", "multi.part3.rar")
	arcs := Find(dir, []string{"multi.part1.rar", "multi.part2.rar", "multi.part3.rar"})
	if len(arcs) != 1 {
		t.Fatalf("found %v", arcs)
	}
	res, err := Extract(context.Background(), arcs[0], dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Files, "movie.mkv") || len(res.Volumes) != 3 {
		t.Fatalf("result %+v", res)
	}
	if sum(t, filepath.Join(dir, "movie.mkv")) != movieMD5 {
		t.Fatal("movie differs")
	}
}

func TestRARMissingVolume(t *testing.T) {
	dir := copyTestdata(t, "multi.part1.rar", "multi.part3.rar")
	if _, err := Extract(context.Background(), Archive{filepath.Join(dir, "multi.part1.rar"), "rar"}, dir, ""); err == nil {
		t.Fatal("extracted with a volume missing")
	}
}

func TestRARPassword(t *testing.T) {
	dir := copyTestdata(t, "locked.rar")
	a := Archive{filepath.Join(dir, "locked.rar"), "rar"}
	if _, err := Extract(context.Background(), a, dir, ""); !errors.Is(err, ErrPassword) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := Extract(context.Background(), a, dir, "wrong"); !errors.Is(err, ErrPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := Extract(context.Background(), a, dir, "secret"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "readme.txt")); string(b) != "hello from box office\n" {
		t.Fatalf("got %q", b)
	}
}

func Test7zAndZip(t *testing.T) {
	dir := copyTestdata(t, "plain.7z", "plain.zip")
	res, err := Extract(context.Background(), Archive{filepath.Join(dir, "plain.7z"), "7z"}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum(t, filepath.Join(dir, "movie.mkv")) != movieMD5 || !slices.Contains(res.Files, filepath.Join("sub", "inner.txt")) {
		t.Fatalf("7z: %+v", res)
	}
	if _, err := Extract(context.Background(), Archive{filepath.Join(dir, "plain.zip"), "zip"}, dir, ""); err != nil {
		t.Fatal(err)
	}
}

func TestTarget(t *testing.T) {
	for in, want := range map[string]string{"a/b.mkv": "/d/a/b.mkv", "../../etc/passwd": "/d/etc/passwd", "/abs": "/d/abs", `..\x`: "/d/x", "..": ""} {
		if got := target("/d", in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
