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

func TestLengthEdition(t *testing.T) {
	const min = 60.0
	cases := []struct {
		name    string
		secs    float64
		others  []float64
		runtime int
		want    string
	}{
		{"longer than the other copy", 201 * min, []float64{178 * min}, 178, "Extended"},
		{"shorter than the other copy", 178 * min, []float64{201 * min}, 178, "Theatrical"},
		{"same cut, logos apart", 120*min + 40, []float64{120 * min}, 120, ""},
		{"a PAL copy runs 4% short", 115 * min, []float64{120 * min}, 120, ""},
		{"five minutes is too little on a long film", 185 * min, []float64{180 * min}, 180, ""},
		{"alone, well past TMDB's runtime", 150 * min, nil, 120, "Extended"},
		{"alone, credits past TMDB's runtime", 128 * min, nil, 120, ""},
		{"alone, runtime unknown", 150 * min, nil, 0, ""},
		{"length unknown", 0, []float64{120 * min}, 120, ""},
		{"others unknown", 150 * min, []float64{0}, 120, ""},
	}
	for _, c := range cases {
		if got := LengthEdition(c.secs, c.others, c.runtime); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEditionOf(t *testing.T) {
	if e, by := EditionOf("Heat (1995) {edition-Director's Cut}.mkv", 100*60, []float64{200 * 60}, 0); e != "Director's Cut" || by {
		t.Errorf("named: got %q %v", e, by)
	}
	if e, by := EditionOf("Heat (1995).mkv", 200*60, []float64{170 * 60}, 0); e != "Extended" || !by {
		t.Errorf("by length: got %q %v", e, by)
	}
}
