package quality

import "testing"

func TestEdition(t *testing.T) {
	for name, want := range map[string]string{
		"Blade.Runner.1982.Directors.Cut.1080p.BluRay.x264-GRP": "Director's Cut",
		"Blade Runner 1982 Final Cut 2160p UHD BluRay":          "Final Cut",
		"The.Lord.of.the.Rings.2001.EXTENDED.1080p.BluRay":      "Extended",
		"Movie.2004.UNRATED.720p.WEB-DL":                        "Unrated",
		"Movie.2004.Theatrical.Cut.1080p":                       "Theatrical",
		"Movie.2019.IMAX.2160p.WEB-DL":                          "IMAX",
		"Aliens.1986.Special.Edition.1080p":                     "Special Edition",
		"Jaws.1975.25th.Anniversary.Edition.DVDRip":             "Anniversary Edition",
		"Movie.1999.REMASTERED.1080p.BluRay":                    "Remastered",
		"Movie (1982) {edition-Director's Cut}.mkv":             "Director's Cut",
		"Movie (1982) {edition-Fan Edit}.mkv":                   "Fan Edit",
		"Movie.2010.1080p.BluRay.x264-GRP":                      "",
	} {
		if got := Edition(name); got != want {
			t.Errorf("Edition(%q) = %q, want %q", name, got, want)
		}
	}
	if !SameEdition("Directors Cut", "Director's Cut") || SameEdition("Extended", "Unrated") {
		t.Error("SameEdition")
	}
	if got := EditionTag("Director's Cut"); got != "{edition-Director's Cut}" {
		t.Errorf("EditionTag = %q", got)
	}
}
