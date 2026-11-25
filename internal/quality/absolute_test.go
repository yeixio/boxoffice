package quality

import (
	"reflect"
	"testing"
)

func TestAbsoluteEpisodes(t *testing.T) {
	for _, c := range []struct {
		name, title string
		loose       bool
		want        []int
	}{
		{"[SubsPlease] One Piece - 1071 (1080p) [ABCD1234]", "One Piece", false, []int{1071}},
		{"One.Piece.1071.1080p.WEB.x264-GRP", "One Piece", false, []int{1071}},
		{"[Group] Frieren - 05v2 [720p]", "Frieren", false, []int{5}},
		{"[Group] Frieren: Beyond Journey's End - 05 [720p]", "Frieren: Beyond Journey’s End", false, []int{5}},
		{"Naruto - 001-003 [BD 1080p]", "Naruto", false, []int{1, 2, 3}},
		{"Naruto (2002) - 12 [DVD]", "Naruto", false, []int{12}},
		{"Naruto Episode 12", "Naruto", false, []int{12}},
		{"Naruto.E012.720p", "Naruto", false, []int{12}},
		{"[Group] Pokémon - 7 [480p].mkv", "Pokemon", false, []int{7}},
		// Not episode numbers: a picture size, a season and episode, another show.
		{"Naruto.1080p.BluRay", "Naruto", false, nil},
		{"Naruto.S01E12.1080p", "Naruto", false, nil},
		{"Naruto Shippuden - 12", "Naruto", false, nil},
		// A year reads as a number; no show has that many episodes, so
		// whoever maps it to an episode finds none.
		{"Akira.1988.1080p", "Akira", false, []int{1988}},
		// Under another name: only read when found by the show's id.
		{"[Group] Shingeki no Kyojin - 25 [1080p]", "Attack on Titan", false, nil},
		{"[Group] Shingeki no Kyojin - 25 [1080p]", "Attack on Titan", true, []int{25}},
		{"Shingeki.no.Kyojin.S02E01.1080p", "Attack on Titan", true, nil},
		{"[Group] Show - 03.mkv", "", true, []int{3}},
	} {
		if got := AbsoluteEpisodes(c.name, c.title, c.loose); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q for %q (loose %v): %v, want %v", c.name, c.title, c.loose, got, c.want)
		}
	}
}

func TestDubbing(t *testing.T) {
	for name, want := range map[string][2]bool{
		"[Group] Show - 05 [1080p][Dual Audio]":   {true, true},
		"Show.S01E05.DUAL.1080p.BluRay":           {true, true},
		"Show.S01E05.MULTi.1080p.WEB":             {true, true},
		"Show.S01E05.DUBBED.1080p.WEB":            {true, false},
		"Show - 05 (English Dub) [720p]":          {true, false},
		"[Group] Show - 05 [1080p] [English Sub]": {false, true},
		"Show.S01E05.SUBBED.720p":                 {false, true},
		"[SubsPlease] Show - 05 (1080p)":          {false, false},
		"The.Dubliners.S01E05.720p":               {false, false},
	} {
		if d, o := Dubbing(name); d != want[0] || o != want[1] {
			t.Errorf("%q: dubbed %v original %v, want %v", name, d, o, want)
		}
	}
}
