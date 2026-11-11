package search

import "testing"

func TestTitleMatchesAccents(t *testing.T) {
	for _, c := range []struct{ release, title string }{
		{"Amelie.2001.1080p.BluRay.x264-GRP", "Amélie"},
		{"Amélie.2001.1080p.BluRay.x264-GRP", "Amélie"},
		{"Aeon.Flux.2005.1080p.BluRay.x264-GRP", "Æon Flux"},
		{"Cafe.Society.2016.1080p.BluRay.x264-GRP", "Café Society"},
		{"TAR.2022.2160p.WEB-DL", "TÁR"},
	} {
		if !titleMatches(c.release, c.title) {
			t.Errorf("titleMatches(%q, %q) = false", c.release, c.title)
		}
	}
	if titleMatches("Amelie.Poulain.Returns.2001", "Amélie 2") {
		t.Error("different titles matched")
	}
}

func TestSearchText(t *testing.T) {
	for in, want := range map[string]string{
		"Amélie":     "amelie",
		"Æon Flux":   "aeon flux",
		"The Matrix": "The Matrix",
	} {
		if got := searchText(in); got != want {
			t.Errorf("searchText(%q) = %q, want %q", in, got, want)
		}
	}
}
