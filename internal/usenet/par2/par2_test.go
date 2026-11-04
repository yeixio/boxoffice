package par2

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// gen makes the same bytes the fixtures were made from (see testdata).
func gen(n, size int) []byte {
	x := uint32(uint64(n) * 2654435761)
	out := make([]byte, size)
	for i := range out {
		x = x*1103515245 + 12345
		out[i] = byte(x >> 16)
	}
	return out
}

var fixtures = []struct {
	name string
	size int
}{{"movie.mkv", 50000}, {"sample.mkv", 9000}, {"info.nfo", 300}}

func setup(t *testing.T) (string, map[string][]byte) {
	dir := t.TempDir()
	want := map[string][]byte{}
	for i, f := range fixtures {
		b := gen(i+1, f.size)
		want[f.name] = b
		if err := os.WriteFile(filepath.Join(dir, f.name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"set.par2", "set.vol0+4.par2", "set.vol4+5.par2"} {
		b, err := os.ReadFile(filepath.Join("testdata", p))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, p), b, 0o644)
	}
	return dir, want
}

func load(t *testing.T, dir string, names ...string) *Set {
	var paths []string
	for _, n := range names {
		paths = append(paths, filepath.Join(dir, n))
	}
	s, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyWhole(t *testing.T) {
	dir, _ := setup(t)
	s := load(t, dir, "set.par2", "set.vol0+4.par2", "set.vol4+5.par2")
	if s.SliceSize != 2048 || len(s.Files) != 3 || s.RecoveryBlocks() != 9 {
		t.Fatalf("set: slice %d, %d files, %d blocks", s.SliceSize, len(s.Files), s.RecoveryBlocks())
	}
	rep, err := s.Verify(context.Background(), dir)
	if err != nil || !rep.OK() {
		t.Fatalf("verify: %v %+v", err, rep)
	}
}

func TestRepair(t *testing.T) {
	dir, want := setup(t)
	ctx := context.Background()
	// Damage three slices of the movie, lose the nfo (one slice), and cut
	// the sample short (its last slice).
	movie := filepath.Join(dir, "movie.mkv")
	b, _ := os.ReadFile(movie)
	b[10] ^= 0xff
	b[2048*7+5] ^= 0x01
	b[49999] ^= 0x10
	os.WriteFile(movie, b, 0o644)
	os.Remove(filepath.Join(dir, "info.nfo"))
	os.Truncate(filepath.Join(dir, "sample.mkv"), 8500)

	s := load(t, dir, "set.par2", "set.vol0+4.par2", "set.vol4+5.par2")
	rep, err := s.Verify(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.NeedBlocks != 5 || !rep.Repairable() {
		t.Fatalf("report %+v", rep)
	}
	if err := s.Repair(ctx, dir, rep, 0); err != nil {
		t.Fatal(err)
	}
	for name, data := range want {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if !bytes.Equal(got, data) {
			t.Fatalf("%s wasn't repaired", name)
		}
	}
	if rep, _ := s.Verify(ctx, dir); !rep.OK() {
		t.Fatalf("after repair: %+v", rep)
	}
}

func TestRepairInSmallPieces(t *testing.T) {
	dir, want := setup(t)
	ctx := context.Background()
	os.Remove(filepath.Join(dir, "movie.mkv")) // 25 slices: more than there are blocks
	s := load(t, dir, "set.par2", "set.vol0+4.par2", "set.vol4+5.par2")
	rep, _ := s.Verify(ctx, dir)
	if rep.Repairable() {
		t.Fatalf("25 slices from 9 blocks? %+v", rep)
	}
	if err := s.Repair(ctx, dir, rep, 0); err != ErrNotEnough {
		t.Fatalf("got %v", err)
	}
	// The sample (5 slices) can be rebuilt, here 4 KiB of memory at a time.
	dir, want = setup(t)
	os.Remove(filepath.Join(dir, "sample.mkv"))
	s = load(t, dir, "set.par2", "set.vol4+5.par2")
	rep, _ = s.Verify(ctx, dir)
	if err := s.Repair(ctx, dir, rep, 4096*5*2); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sample.mkv"))
	if !bytes.Equal(got, want["sample.mkv"]) {
		t.Fatal("sample wasn't rebuilt")
	}
}

func TestMatchObfuscated(t *testing.T) {
	dir, want := setup(t)
	os.Rename(filepath.Join(dir, "movie.mkv"), filepath.Join(dir, "a8f3e2b1c9d0"))
	s := load(t, dir, "set.par2")
	renamed, err := s.Match(dir, []string{"a8f3e2b1c9d0", "sample.mkv"})
	if err != nil || renamed["a8f3e2b1c9d0"] != "movie.mkv" {
		t.Fatalf("renamed %v, %v", renamed, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "movie.mkv"))
	if !bytes.Equal(got, want["movie.mkv"]) {
		t.Fatal("wrong file")
	}
}

func TestDamagedPar2(t *testing.T) {
	dir, _ := setup(t)
	p := filepath.Join(dir, "set.vol4+5.par2")
	b, _ := os.ReadFile(p)
	b[3000] ^= 0xff // inside a recovery block
	os.WriteFile(p, b, 0o644)
	os.Remove(filepath.Join(dir, "info.nfo"))
	s := load(t, dir, "set.vol4+5.par2") // no index file: the volume carries the set too
	rep, _ := s.Verify(context.Background(), dir)
	if err := s.Repair(context.Background(), dir, rep, 0); err != nil {
		t.Fatal(err)
	}
	if rep, _ := s.Verify(context.Background(), dir); !rep.OK() {
		t.Fatal("not repaired")
	}
}

func TestInputConstants(t *testing.T) {
	c := inputConstants(4)
	if c[0] != 2 || c[1] != 4 || c[2] != 16 || c[3] != 128 {
		t.Fatalf("got %v", c)
	}
}
