package gamedat

import (
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// listXML is a small piece of MAME's -listxml: a parent and its clone, a
// BIOS and a game that needs it, a device with files and one without, and
// a game with a CHD.
const listXML = `<?xml version="1.0"?>
<!DOCTYPE mame [
<!ELEMENT mame (machine+)>
]>
<mame build="0.289 (mame0289)" debug="no" mameconfig="10">
	<machine name="neogeo" isbios="yes">
		<description>Neo-Geo</description>
		<biosset name="euro" description="Europe MVS"/>
		<biosset name="us" description="US MVS"/>
		<rom name="sp-s2.sp1" bios="euro" size="131072" crc="9036d879"/>
		<rom name="usa_2slt.bin" bios="us" size="131072" crc="e72943de"/>
		<rom name="sm1.sm1" size="131072" crc="94416d67"/>
	</machine>
	<machine name="puckman">
		<description>Puck Man (Japan set 1)</description>
		<year>1980</year>
		<manufacturer>Namco</manufacturer>
		<rom name="pm1_prg1.6e" size="2048" crc="f36e88ab"/>
		<rom name="pm1-1.7f" size="32" crc="2fc650bd"/>
		<device_ref name="namco"/>
		<input players="2" coins="2"/>
	</machine>
	<machine name="pacman" cloneof="puckman" romof="puckman">
		<description>Pac-Man (Midway)</description>
		<year>1980</year>
		<manufacturer>Namco (Midway license)</manufacturer>
		<rom name="pacman.6e" size="4096" crc="c1e6ab10"/>
		<rom name="82s123.7f" merge="pm1-1.7f" size="32" crc="2fc650bd"/>
		<device_ref name="namco"/>
		<input players="2" coins="2"/>
	</machine>
	<machine name="namco" isdevice="yes" runnable="no">
		<description>Namco 8-voice sound</description>
	</machine>
	<machine name="mslug" romof="neogeo">
		<description>Metal Slug &amp; friends</description>
		<year>1996</year>
		<manufacturer>Nazca</manufacturer>
		<rom name="sp-s2.sp1" merge="sp-s2.sp1" bios="euro" size="131072" crc="9036d879"/>
		<rom name="usa_2slt.bin" merge="usa_2slt.bin" bios="us" size="131072" crc="e72943de"/>
		<rom name="sm1.sm1" merge="sm1.sm1" size="131072" crc="94416d67"/>
		<rom name="201-p1.p1" size="2097152" crc="08d8daa5"/>
		<rom name="201-nodump.bin" size="16" status="nodump"/>
		<input players="2"/>
	</machine>
	<machine name="sfa">
		<description>Street Fighter Alpha</description>
		<rom name="sfz.03" size="524288" crc="5f99e9a5"/>
		<device_ref name="qsound"/>
	</machine>
	<machine name="qsound" isdevice="yes" runnable="no">
		<description>Q-Sound</description>
		<rom name="dl-1425.bin" size="8192" crc="d6cf5ef5"/>
	</machine>
	<machine name="kinst">
		<description>Killer Instinct</description>
		<rom name="ki-l15d.u98" size="524288" crc="7b65ca3d"/>
		<disk name="kinst" sha1="81d833236e994528d1482979261401b198d1ca53"/>
	</machine>
</mame>`

func list(t *testing.T) *List {
	t.Helper()
	l, err := ParseXML(strings.NewReader(listXML), nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseXML(t *testing.T) {
	l := list(t)
	if l.Version != "0.289" {
		t.Errorf("version %q", l.Version)
	}
	p := l.Machines["pacman"]
	if p == nil || p.Description != "Pac-Man (Midway)" || p.Year != "1980" || p.Manufacturer != "Namco (Midway license)" ||
		p.CloneOf != "puckman" || p.Players != 2 || len(p.Roms) != 2 || p.Roms[1].Merge != "pm1-1.7f" || p.Roms[0].CRC != 0xc1e6ab10 {
		t.Errorf("pacman %+v", p)
	}
	if m := l.Machines["mslug"]; m.Description != "Metal Slug & friends" || !m.Roms[4].NoDump || m.Roms[4].HasCRC {
		t.Errorf("mslug %+v", m)
	}
	if b := l.Machines["neogeo"]; !b.IsBIOS || b.DefaultBIOS != "euro" {
		t.Errorf("neogeo %+v", b)
	}
	if d := l.Machines["namco"]; !d.IsDevice || d.HasRoms {
		t.Errorf("namco %+v", d)
	}
	if k := l.Machines["kinst"]; len(k.Disks) != 1 || k.Disks[0].SHA1 != "81d833236e994528d1482979261401b198d1ca53" {
		t.Errorf("kinst %+v", k)
	}
}

// Only the sets asked for keep their files, and LoadMAME asks for those
// on disk and those they lean on.
func TestLoadMAMEKeepsOnlyWhatsNeeded(t *testing.T) {
	opens := 0
	l, err := LoadMAME(func() (io.ReadCloser, error) {
		opens++
		return io.NopCloser(strings.NewReader(listXML)), nil
	}, []string{"pacman", "mslug", "sfa"})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 2 {
		t.Errorf("read the list %d times", opens)
	}
	for _, n := range []string{"pacman", "puckman", "mslug", "neogeo", "sfa", "qsound"} {
		if len(l.Machines[n].Roms) == 0 {
			t.Errorf("%s has no files", n)
		}
	}
	if len(l.Machines["kinst"].Roms) != 0 || !l.Machines["kinst"].HasRoms {
		t.Errorf("kinst %+v", l.Machines["kinst"])
	}
	need := Needed(l, []string{"puckman"})
	if !need["pacman"] || !need["puckman"] || need["namco"] || need["mslug"] {
		t.Errorf("needed for puckman: %v", need)
	}
}

func set(name string, crcs ...uint32) *Set {
	s := &Set{Name: name, Path: "/games/mame/" + name + ".zip"}
	for i, c := range crcs {
		s.Files = append(s.Files, File{Name: name + string(rune('a'+i)), CRC: c})
	}
	return s
}

func sets(ss ...*Set) map[string]*Set {
	out := map[string]*Set{}
	for _, s := range ss {
		out[s.Name] = s
	}
	return out
}

func game(t *testing.T, c Check, name string) Game {
	t.Helper()
	i := slices.IndexFunc(c.Games, func(g Game) bool { return g.Machine.Name == name })
	if i < 0 {
		t.Fatalf("no %s in %+v", name, c.Games)
	}
	return c.Games[i]
}

func TestCheckMAMEFormats(t *testing.T) {
	l := list(t)
	t.Run("split", func(t *testing.T) {
		c := CheckMAME(l, sets(set("puckman", 0xf36e88ab, 0x2fc650bd), set("pacman", 0xc1e6ab10)), nil)
		if c.Format != Split {
			t.Errorf("format %q", c.Format)
		}
		for _, n := range []string{"puckman", "pacman"} {
			if g := game(t, c, n); g.Status != Complete || g.Parent() != "puckman" {
				t.Errorf("%s: %s %v, parent %s", n, g.Status, g.Problems, g.Parent())
			}
		}
	})
	t.Run("non-merged", func(t *testing.T) {
		c := CheckMAME(l, sets(set("puckman", 0xf36e88ab, 0x2fc650bd), set("pacman", 0xc1e6ab10, 0x2fc650bd)), nil)
		if c.Format != NonMerged {
			t.Errorf("format %q", c.Format)
		}
	})
	t.Run("merged", func(t *testing.T) {
		// The clone's file is in its parent's archive, and it has none of
		// its own.
		c := CheckMAME(l, sets(set("puckman", 0xf36e88ab, 0x2fc650bd, 0xc1e6ab10)), nil)
		if c.Format != Merged {
			t.Errorf("format %q", c.Format)
		}
		g := game(t, c, "pacman")
		if !g.InParent || g.Status != Complete || g.Set.Name != "puckman" {
			t.Errorf("pacman %+v", g)
		}
	})
	t.Run("parents only", func(t *testing.T) {
		if c := CheckMAME(l, sets(set("puckman", 0xf36e88ab, 0x2fc650bd)), nil); c.Format != "" || len(c.Games) != 1 {
			t.Errorf("%+v", c)
		}
	})
}

func TestCheckMAMEProblems(t *testing.T) {
	l := list(t)
	t.Run("clone without its parent", func(t *testing.T) {
		g := game(t, CheckMAME(l, sets(set("pacman", 0xc1e6ab10)), nil), "pacman")
		if g.Status != Missing || !slices.Equal(g.Problems, []Problem{{ProblemParent, "puckman"}}) {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
	})
	t.Run("another version's file", func(t *testing.T) {
		s := &Set{Name: "puckman", Files: []File{{Name: "pm1_prg1.6e", CRC: 0x12345678}, {Name: "pm1-1.7f", CRC: 0x2fc650bd}}}
		g := game(t, CheckMAME(l, sets(s), nil), "puckman")
		if g.Status != Wrong || !slices.Equal(g.Problems, []Problem{{ProblemWrong, "pm1_prg1.6e"}}) {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
	})
	t.Run("a missing file", func(t *testing.T) {
		g := game(t, CheckMAME(l, sets(set("puckman", 0xf36e88ab)), nil), "puckman")
		if g.Status != Missing || !slices.Equal(g.Problems, []Problem{{ProblemMissing, "pm1-1.7f"}}) {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
	})
	t.Run("BIOS missing", func(t *testing.T) {
		g := game(t, CheckMAME(l, sets(set("mslug", 0x08d8daa5)), nil), "mslug")
		if g.Status != NeedsBIOS || !slices.Equal(g.Problems, []Problem{{ProblemBIOS, "neogeo"}}) {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
	})
	t.Run("BIOS there", func(t *testing.T) {
		// Only the default BIOS option's file is needed, not the US one.
		c := CheckMAME(l, sets(set("mslug", 0x08d8daa5), set("neogeo", 0x9036d879, 0x94416d67)), nil)
		if g := game(t, c, "mslug"); g.Status != Complete {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
		if !slices.Equal(c.Support, []string{"neogeo"}) || len(c.Games) != 1 {
			t.Errorf("support %v, games %d", c.Support, len(c.Games))
		}
	})
	t.Run("device missing", func(t *testing.T) {
		g := game(t, CheckMAME(l, sets(set("sfa", 0x5f99e9a5)), nil), "sfa")
		if g.Status != NeedsBIOS || !slices.Equal(g.Problems, []Problem{{ProblemDevice, "qsound"}}) {
			t.Errorf("%s %v", g.Status, g.Problems)
		}
		if g := game(t, CheckMAME(l, sets(set("sfa", 0x5f99e9a5), set("qsound", 0xd6cf5ef5)), nil), "sfa"); g.Status != Complete {
			t.Errorf("with qsound: %s %v", g.Status, g.Problems)
		}
		// A non-merged set has the device's file in its own archive.
		if g := game(t, CheckMAME(l, sets(set("sfa", 0x5f99e9a5, 0xd6cf5ef5)), nil), "sfa"); g.Status != Complete {
			t.Errorf("non-merged: %s %v", g.Status, g.Problems)
		}
	})
	t.Run("not on the list", func(t *testing.T) {
		g := game(t, CheckMAME(l, sets(set("pacmanold", 1)), nil), "pacmanold")
		if g.Status != Unknown {
			t.Errorf("%s", g.Status)
		}
	})
}

func writeCHD(t *testing.T, p, sha1 string) {
	t.Helper()
	h := make([]byte, 124)
	copy(h, "MComprHD")
	binary.BigEndian.PutUint32(h[8:], 124)
	binary.BigEndian.PutUint32(h[12:], 5)
	b, _ := hex.DecodeString(sha1)
	copy(h[84:], b)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, h, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckMAMECHDs(t *testing.T) {
	l := list(t)
	dir := t.TempDir()
	const right = "81d833236e994528d1482979261401b198d1ca53"
	check := func(chd string) Game {
		s := set("kinst", 0x7b65ca3d)
		if chd != "" {
			p := filepath.Join(dir, chd, "kinst", "kinst.chd")
			writeCHD(t, p, chd)
			s.CHDs = map[string]string{"kinst": p}
		}
		return game(t, CheckMAME(l, sets(s), CHDSHA1), "kinst")
	}
	if g := check(""); g.Status != Missing || !slices.Equal(g.Problems, []Problem{{ProblemDisk, "kinst"}}) {
		t.Errorf("no CHD: %s %v", g.Status, g.Problems)
	}
	if g := check(right); g.Status != Complete {
		t.Errorf("right CHD: %s %v", g.Status, g.Problems)
	}
	if g := check("0000000000000000000000000000000000000001"); g.Status != Wrong || !slices.Equal(g.Problems, []Problem{{ProblemDiskWrong, "kinst"}}) {
		t.Errorf("wrong CHD: %s %v", g.Status, g.Problems)
	}
}

func TestCHDSHA1(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.chd")
	writeCHD(t, p, "81d833236e994528d1482979261401b198d1ca53")
	if got, err := CHDSHA1(p); err != nil || got != "81d833236e994528d1482979261401b198d1ca53" {
		t.Errorf("%q %v", got, err)
	}
	os.WriteFile(p, []byte("not a chd"), 0o644)
	if _, err := CHDSHA1(p); err == nil {
		t.Error("read a file that isn't a CHD")
	}
}
