package screen

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	pe := make([]byte, 300)
	pe[0], pe[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(pe[0x3c:], 0x80)
	copy(pe[0x80:], "PE\x00\x00")
	files := map[string][]byte{
		"movie.mkv":     {0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3},
		"movie.nfo":     []byte("a release"),
		"Subs/en.srt":   []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"),
		"setup.exe":     pe,
		"movie.mkv.lnk": []byte("L"),
		"codec.dat":     pe,
		"extra.mp4":     []byte("Rar!\x1a\x07\x01\x00"),
		"MZ-start.mkv":  append([]byte("MZ"), make([]byte, 100)...), // MZ but no PE header: a video can start that way
		"run.sh.txt":    []byte("#!/bin/sh"),
	}
	var names []string
	for n, b := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
		os.WriteFile(filepath.Join(dir, n), b, 0o644)
		names = append(names, n)
	}
	got := map[string]string{}
	for _, f := range Check(dir, names, true) {
		got[f.File] = f.Why
	}
	want := map[string]string{
		"setup.exe":     "programs or scripts",
		"movie.mkv.lnk": "pretending to be a video",
		"codec.dat":     "a Windows program",
		"extra.mp4":     "RAR archive",
		"run.sh.txt":    "a script",
	}
	for f, w := range want {
		if !strings.Contains(got[f], w) {
			t.Errorf("%s: %q, want %q", f, got[f], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("found %v", got)
	}
}

func TestNoVideo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.nfo"), []byte("x"), 0o644)
	if f := Check(dir, []string{"a.nfo"}, true); len(f) != 1 || f[0].File != "" {
		t.Fatalf("%v", f)
	}
	if f := Check(dir, []string{"a.nfo"}, false); len(f) != 0 {
		t.Fatalf("a hand-added download needn't hold a video: %v", f)
	}
}

func TestCheckBooks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "book.epub"), append([]byte("PK\x03\x04"), make([]byte, 100)...), 0o644)
	os.WriteFile(filepath.Join(dir, "fake.mobi"), append([]byte("Rar!\x1a\x07\x00"), make([]byte, 100)...), 0o644)
	os.WriteFile(filepath.Join(dir, "book.epub.exe"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(dir, "01.mp3"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), make([]byte, 10), 0o644)
	if f := CheckFor(dir, []string{"book.epub"}, WantEbook); len(f) != 0 {
		t.Fatalf("an EPUB (a ZIP inside) was flagged: %v", f)
	}
	if f := CheckFor(dir, []string{"fake.mobi"}, WantEbook); len(f) != 1 {
		t.Fatalf("a RAR named .mobi: %v", f)
	}
	if f := CheckFor(dir, []string{"book.epub.exe"}, WantEbook); len(f) != 1 || f[0].Why != "it's a .exe file pretending to be an ebook" {
		t.Fatalf("exe: %v", f)
	}
	if f := CheckFor(dir, []string{"readme.txt", "01.mp3"}, WantEbook); len(f) != 1 || f[0].File != "" {
		t.Fatalf("no ebook: %v", f)
	}
	if f := CheckFor(dir, []string{"01.mp3"}, WantAudio); len(f) != 0 {
		t.Fatalf("audiobook: %v", f)
	}
	if f := CheckFor(dir, []string{"book.epub"}, WantAudio); len(f) != 1 {
		t.Fatalf("an ebook passed as an audiobook: %v", f)
	}
}
