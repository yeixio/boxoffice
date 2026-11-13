package games

import (
	"path/filepath"
	"testing"
)

// An arcade set whose chips are named like a console's games is still
// MAME's; a console game's zip of the one game is the console's.
func TestIdentifyArcadeSetWithConsoleNames(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		files map[string]uint32
		want  string
	}{
		{"esprade.zip", map[string]uint32{"u42.int": 1, "u41.int": 2, "u63.bin": 3}, "mame"},
		{"videopkr.zip", map[string]uint32{"vpoker.c5": 1, "vpoker.col": 2, "readme.txt": 3}, "mame"},
		{"mysticm.zip", map[string]uint32{"mm.a26": 1, "mm.b26": 2}, "mame"},
		{"Lone (Europe).zip", map[string]uint32{"Lone (Europe).nes": 1}, "nes"},
		{"lone.zip", map[string]uint32{"lone.nes": 1, "lone.txt": 2}, "nes"},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		zipOf(t, p, c.files)
		sys, ok := Identify(p)
		if !ok || sys.Key != c.want {
			t.Errorf("%s: got %q %v, want %s", c.name, sys.Key, ok, c.want)
		}
	}
}
