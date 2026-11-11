package unpack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLocked(t *testing.T) {
	dir := copyTestdata(t, "locked.rar", "multi.part1.rar", "multi.part2.rar", "plain.7z", "plain.zip")
	for name, want := range map[string]bool{
		"locked.rar":      true, // RAR 7, encrypted headers
		"multi.part1.rar": false,
		"multi.part2.rar": false,
		"plain.7z":        false,
		"plain.zip":       false,
	} {
		if got := Locked(filepath.Join(dir, name)); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	if Locked(filepath.Join(dir, "gone.rar")) {
		t.Error("a missing file is locked")
	}
}

func TestLockedZip(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "plain.zip"))
	if err != nil {
		t.Fatal(err)
	}
	b[6] |= 0x1
	p := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(p, b, 0o644)
	if !Locked(p) {
		t.Fatal("an encrypted zip isn't locked")
	}
}

func TestLockedRAR4(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("box office "), 3000)
	writeRAR4(t, dir, "movie.mkv", data, []string{"a.part1.rar", "a.part2.rar"})
	if Locked(filepath.Join(dir, "a.part2.rar")) {
		t.Fatal("a plain RAR 4 volume is locked")
	}
	encryptFiles = true
	defer func() { encryptFiles = false }()
	writeRAR4(t, dir, "movie.mkv", data, []string{"b.part1.rar", "b.part2.rar"})
	for _, n := range []string{"b.part1.rar", "b.part2.rar"} {
		if !Locked(filepath.Join(dir, n)) {
			t.Errorf("%s: an encrypted RAR 4 volume isn't locked", n)
		}
	}
}

// rar5 writes a RAR 5 archive of one stored file, its data encrypted or
// not, after a service header, as RAR writes for a comment.
func rar5(encrypted bool) []byte {
	vint := func(b *bytes.Buffer, n uint64) {
		for n >= 0x80 {
			b.WriteByte(byte(n) | 0x80)
			n >>= 7
		}
		b.WriteByte(byte(n))
	}
	var out bytes.Buffer
	out.Write(sigRAR5)
	block := func(body []byte) {
		out.Write([]byte{0, 0, 0, 0}) // CRC, which Locked doesn't check
		vint(&out, uint64(len(body)))
		out.Write(body)
	}
	block([]byte{1, 0, 0}) // main header
	payload := []byte("hello")
	header := func(typ uint64, name string, extra []byte) {
		var h bytes.Buffer
		vint(&h, typ)
		flags := uint64(0x2)
		if len(extra) > 0 {
			flags |= 0x1
		}
		vint(&h, flags)
		if len(extra) > 0 {
			vint(&h, uint64(len(extra)))
		}
		vint(&h, uint64(len(payload)))
		vint(&h, 0)                    // file flags
		vint(&h, uint64(len(payload))) // unpacked size
		vint(&h, 0x20)                 // attributes
		vint(&h, 0)                    // compression: stored
		vint(&h, 0)                    // Windows
		vint(&h, uint64(len(name)))
		h.WriteString(name)
		h.Write(extra)
		block(h.Bytes())
		out.Write(payload)
	}
	header(3, "CMT", nil)
	var extra bytes.Buffer
	// A file time record, then (when encrypted) an encryption record.
	extra.Write([]byte{6, 3, 0x2, 1, 2, 3, 4})
	if encrypted {
		rec := append([]byte{0x01, 0, 0, 15}, make([]byte, 16+16)...)
		vint(&extra, uint64(len(rec)))
		extra.Write(rec)
	}
	header(2, "movie.mkv", extra.Bytes())
	block([]byte{5, 0, 0}) // end
	return out.Bytes()
}

func TestLockedRAR5Files(t *testing.T) {
	dir := t.TempDir()
	for _, enc := range []bool{false, true} {
		p := filepath.Join(dir, "x.rar")
		os.WriteFile(p, rar5(enc), 0o644)
		if got := Locked(p); got != enc {
			t.Errorf("encrypted %v: got %v", enc, got)
		}
	}
}
