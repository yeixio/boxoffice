package downloader

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// crossDevice makes move copy, as it does between two shares, and makes
// the first fails removals of the original fail.
func crossDevice(t *testing.T, fails int) *int {
	t.Helper()
	tries := 0
	rename = func(src, dest string) error {
		if filepath.Dir(src) != filepath.Dir(dest) {
			return &os.LinkError{Op: "rename", Old: src, New: dest, Err: syscall.EXDEV}
		}
		return os.Rename(src, dest)
	}
	remove = func(p string) error {
		tries++
		if tries <= fails {
			return &os.PathError{Op: "remove", Path: p, Err: syscall.EIO}
		}
		return os.Remove(p)
	}
	removeWait = time.Millisecond
	t.Cleanup(func() { rename, remove, removeWait = os.Rename, os.Remove, 250*time.Millisecond })
	return &tries
}

func TestMoveRetriesRemove(t *testing.T) {
	tries := crossDevice(t, 2)
	src := filepath.Join(t.TempDir(), "a.mkv")
	dest := filepath.Join(t.TempDir(), "b.mkv")
	os.WriteFile(src, []byte("movie"), 0o644)
	if err := move(src, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "movie" {
		t.Fatalf("dest holds %q", b)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the original is still there")
	}
	if *tries != 3 {
		t.Fatalf("%d tries", *tries)
	}
}

func TestMoveSourceLeft(t *testing.T) {
	crossDevice(t, 100)
	src := filepath.Join(t.TempDir(), "a.mkv")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "b.mkv")
	os.WriteFile(src, []byte("movie"), 0o644)
	err := move(src, dest)
	var left *sourceLeftError
	if !errors.As(err, &left) || !errors.Is(err, syscall.EIO) {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "movie" {
		t.Fatalf("dest holds %q", b)
	}
	entries, _ := os.ReadDir(destDir)
	if len(entries) != 1 {
		t.Fatalf("left %d files in the library folder", len(entries))
	}
}
