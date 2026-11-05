package unpack

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeRAR4 writes a stored (uncompressed) RAR 3 multi-volume set holding
// one file, the old format no current RAR can make, with volumes named
// by names.
// noVolNumber leaves the volume number out of end blocks, as RAR 2 did.
var noVolNumber bool

func writeRAR4(t *testing.T, dir, file string, data []byte, names []string) {
	writeRAR4Named(t, dir, file, data, names, true)
}

// writeRAR4Named writes the set, with the new-naming flag or without it
// (old-style .rar, .r00, … naming, as most scene releases use).
func writeRAR4Named(t *testing.T, dir, file string, data []byte, names []string, newNaming bool) {
	t.Helper()
	block := func(typ byte, flags uint16, body []byte) []byte {
		h := make([]byte, 7, 7+len(body))
		h[2] = typ
		binary.LittleEndian.PutUint16(h[3:], flags)
		binary.LittleEndian.PutUint16(h[5:], uint16(7+len(body)))
		h = append(h, body...)
		binary.LittleEndian.PutUint16(h, uint16(crc32.ChecksumIEEE(h[2:])))
		return h
	}
	n := len(names)
	chunk := (len(data) + n - 1) / n
	for i, name := range names {
		part := data[i*chunk : min(len(data), (i+1)*chunk)]
		var b bytes.Buffer
		b.Write(sigRAR4)
		arc := uint16(0x0001) // volume
		if newNaming {
			arc |= 0x0010
		}
		if i == 0 {
			arc |= 0x0100
		}
		b.Write(block(0x73, arc, make([]byte, 6)))
		var fh bytes.Buffer
		le := func(v any) { binary.Write(&fh, binary.LittleEndian, v) }
		le(uint32(len(part)))
		le(uint32(len(data)))
		fh.WriteByte(0) // MS-DOS
		crc := crc32.ChecksumIEEE(part)
		if i == n-1 {
			crc = crc32.ChecksumIEEE(data)
		}
		le(crc)
		le(uint32(0x5a210000)) // a DOS time
		fh.WriteByte(20)
		fh.WriteByte(0x30) // stored
		le(uint16(len(file)))
		le(uint32(0x20))
		fh.WriteString(file)
		flags := uint16(0x8000)
		if i > 0 {
			flags |= 0x01
		}
		if i < n-1 {
			flags |= 0x02
		}
		b.Write(block(0x74, flags, fh.Bytes()))
		b.Write(part)
		end := uint16(0)
		if !noVolNumber {
			end |= 0x0008 // EARC_VOLNUMBER, as unrar writes it
		}
		if i < n-1 {
			end |= 0x0001
		}
		var num []byte
		if !noVolNumber {
			num = binary.LittleEndian.AppendUint16(nil, uint16(i))
		}
		b.Write(block(0x7b, end, num))
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Mike's case: a set posted under hashes, where the first volume is a
// .rar and the rest aren't .r00, .r01…
func TestObfuscatedRAR5(t *testing.T) {
	dir := t.TempDir()
	names := map[string]string{
		"multi.part1.rar": "39f5d2cb5d2e2f132f5f3091c5087f1c.rar",
		"multi.part2.rar": "a76b5b9d1ee1b2a88b2ef31390d20f72",
		"multi.part3.rar": "0d8f3e.rar",
	}
	for from, to := range names {
		b, err := os.ReadFile(filepath.Join("testdata", from))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, to), b, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "notes.nfo"), []byte("hello"), 0o644)
	files := []string{"0d8f3e.rar", "39f5d2cb5d2e2f132f5f3091c5087f1c.rar", "a76b5b9d1ee1b2a88b2ef31390d20f72", "notes.nfo"}
	arcs := Find(dir, files)
	if len(arcs) != 1 {
		t.Fatalf("found %+v", arcs)
	}
	want := []string{names["multi.part1.rar"], names["multi.part2.rar"], names["multi.part3.rar"]}
	var got []string
	for _, v := range arcs[0].Volumes {
		got = append(got, filepath.Base(v))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("volumes %v, want %v", got, want)
	}
	res, err := Extract(context.Background(), arcs[0], dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum(t, filepath.Join(dir, "movie.mkv")) != movieMD5 {
		t.Fatal("movie differs")
	}
	if len(res.Volumes) != 3 || !strings.HasPrefix(res.Volumes[0], dir) {
		t.Fatalf("volumes to clean up: %v", res.Volumes)
	}
	// The volumes keep their names.
	for _, n := range want {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObfuscatedRAR4(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("Box Office "), 3000)
	names := []string{"ff01.rar", "zz.bin", "aa", "c3c3.rar"}
	writeRAR4(t, dir, "movie.mkv", data, names)
	for i, n := range names {
		v, ok := probeRAR(filepath.Join(dir, n))
		if !ok || v.ver != 4 || !v.multi || v.num != i {
			t.Fatalf("%s: %+v %v", n, v, ok)
		}
	}
	arcs := Find(dir, []string{"aa", "c3c3.rar", "ff01.rar", "zz.bin"})
	if len(arcs) != 1 || len(arcs[0].Volumes) != 4 {
		t.Fatalf("found %+v", arcs)
	}
	if _, err := Extract(context.Background(), arcs[0], dir, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "movie.mkv")); !bytes.Equal(b, data) {
		t.Fatal("movie differs")
	}
}

func TestObfuscatedMissingVolume(t *testing.T) {
	dir := t.TempDir()
	writeRAR4(t, dir, "movie.mkv", bytes.Repeat([]byte("x"), 9000), []string{"a.rar", "b", "c"})
	os.Remove(filepath.Join(dir, "b"))
	arcs := Find(dir, []string{"a.rar", "c"})
	if len(arcs) != 1 {
		t.Fatalf("found %+v", arcs)
	}
	_, err := Extract(context.Background(), arcs[0], dir, "")
	if err == nil || !strings.Contains(err.Error(), "volume 2") {
		t.Fatalf("err %v", err)
	}
}

func TestTwoSets(t *testing.T) {
	dir := copyTestdata(t, "multi.part1.rar", "multi.part2.rar", "multi.part3.rar")
	writeRAR4(t, dir, "subs.srt", bytes.Repeat([]byte("s"), 4000), []string{"subs.rar", "subs.r00"})
	arcs := Find(dir, []string{"multi.part1.rar", "multi.part2.rar", "multi.part3.rar", "subs.r00", "subs.rar"})
	if len(arcs) != 2 || len(arcs[0].Volumes) != 3 || len(arcs[1].Volumes) != 2 {
		t.Fatalf("found %+v", arcs)
	}
	for _, a := range arcs {
		if _, err := Extract(context.Background(), a, dir, ""); err != nil {
			t.Fatal(err)
		}
	}
}

// Mike's second case: an old-style set (rardecode asks for .r00 after the
// first volume) posted under hashes.
func TestObfuscatedOldNaming(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("Amerikatsi "), 5000)
	var names []string
	for i := range 12 {
		names = append(names, fmt.Sprintf("%08x", i*7919+1))
	}
	names[0] = "a76b5b9d1ee1b2a88b2ef31390d20f72.rar"
	writeRAR4Named(t, dir, "amerikatsi.mkv", data, names, false)
	arcs := Find(dir, names)
	if len(arcs) != 1 || len(arcs[0].Volumes) != 12 {
		t.Fatalf("found %+v", arcs)
	}
	res, err := Extract(context.Background(), arcs[0], dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "amerikatsi.mkv")); !bytes.Equal(b, data) || len(res.Volumes) != 12 {
		t.Fatal("movie differs")
	}
}

func TestNextOldName(t *testing.T) {
	for in, want := range map[string]string{"v.part0001.rar": "v.part0001.r00", "v.r00": "v.r01", "v.r99": "v.s00"} {
		if got := nextOldName(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

// Volumes go in the order their headers say, not their names'.
func TestObfuscatedOrder(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("0123456789"), 6000)
	names := []string{"m.rar", "z9", "y8", "x7", "w6", "v5"}
	writeRAR4Named(t, dir, "movie.mkv", data, names, false)
	arcs := Find(dir, names)
	if len(arcs) != 1 || !slices.Equal(arcs[0].Volumes, func() (out []string) {
		for _, n := range names {
			out = append(out, filepath.Join(dir, n))
		}
		return
	}()) {
		t.Fatalf("found %+v", arcs)
	}
	if _, err := Extract(context.Background(), arcs[0], dir, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "movie.mkv")); !bytes.Equal(b, data) {
		t.Fatal("movie differs")
	}
}

func TestMissingLastVolumes(t *testing.T) {
	dir := t.TempDir()
	writeRAR4Named(t, dir, "movie.mkv", bytes.Repeat([]byte("x"), 9000), []string{"a.rar", "b", "c"}, false)
	os.Remove(filepath.Join(dir, "c"))
	arcs := Find(dir, []string{"a.rar", "b"})
	_, err := Extract(context.Background(), arcs[0], dir, "")
	if err == nil || !strings.Contains(err.Error(), "missing volumes at its end") {
		t.Fatalf("err %v", err)
	}
}

func TestScrambledWithoutNumbers(t *testing.T) {
	noVolNumber = true
	defer func() { noVolNumber = false }()
	dir := t.TempDir()
	writeRAR4Named(t, dir, "movie.mkv", bytes.Repeat([]byte("x"), 9000), []string{"a.rar", "q", "b"}, false)
	arcs := Find(dir, []string{"a.rar", "b", "q"})
	_, err := Extract(context.Background(), arcs[0], dir, "")
	if err == nil || !strings.Contains(err.Error(), "can't be put in order") {
		t.Fatalf("err %v", err)
	}
	// With regular names, the names give the order.
	dir = t.TempDir()
	writeRAR4Named(t, dir, "movie.mkv", bytes.Repeat([]byte("y"), 9000), []string{"s.rar", "s.r00", "s.r01"}, false)
	arcs = Find(dir, []string{"s.r00", "s.r01", "s.rar"})
	if _, err := Extract(context.Background(), arcs[0], dir, ""); err != nil {
		t.Fatal(err)
	}
}
