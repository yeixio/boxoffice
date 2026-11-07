package store

import (
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A copy's edition comes from its name, then the release it came from,
// then the title inside it, and only then from how long it runs.
func TestLabelEditions(t *testing.T) {
	releases := map[string]string{"/m/Heat (1995)/Heat (1995).mkv": "Heat.1995.EXTENDED.1080p.BluRay.x264-GRP"}
	m := Movie{Runtime: 170,
		File: &File{Path: "/m/Heat (1995)/Heat (1995).mkv", Media: &quality.Media{Duration: 190 * 60}},
		Editions: []File{
			{Path: "/m/Heat (1995)/Heat (1995) {edition-Director's Cut}.mkv", Media: &quality.Media{Duration: 200 * 60}},
			{Path: "/m/Heat (1995)/Heat (1995) a.mkv", Media: &quality.Media{Duration: 170 * 60, Title: "Heat: The Final Cut"}},
			{Path: "/m/Heat (1995)/Heat (1995) b.mkv", Media: &quality.Media{Duration: 150 * 60}},
		}}
	m.LabelEditions(func(p string) string { return releases[p] })
	got := []struct{ e, from string }{{m.File.Edition, m.File.EditionFrom}}
	for _, f := range m.Editions {
		got = append(got, struct{ e, from string }{f.Edition, f.EditionFrom})
	}
	want := []struct{ e, from string }{
		{"Extended", EditionFromRelease},
		{"Director's Cut", EditionFromName},
		{"Final Cut", EditionFromFile},
		{"Theatrical", EditionFromLength},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copy %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if !m.Editions[2].EditionByLength || m.File.EditionByLength {
		t.Error("only the length guess is marked as one")
	}
	// No release names known.
	m.LabelEditions(nil)
	if m.File.Edition != "Extended" || m.File.EditionFrom != EditionFromLength {
		t.Errorf("without release: %q from %q", m.File.Edition, m.File.EditionFrom)
	}
}
