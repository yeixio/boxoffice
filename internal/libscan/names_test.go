package libscan

import "testing"

func TestParseName(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Guess
	}{
		{"The Matrix (1999)", Guess{Title: "The Matrix", Year: 1999}},
		{"The.Matrix.1999.1080p.BluRay.x264-GRP", Guess{Title: "The Matrix", Year: 1999}},
		{"Blade Runner 2049 (2017)", Guess{Title: "Blade Runner 2049", Year: 2017}},
		{"1917 (2019)", Guess{Title: "1917", Year: 2019}},
		{"2012", Guess{Title: "2012"}},
		{"The Matrix (1999) {tmdb-603}", Guess{Title: "The Matrix", Year: 1999, TMDBID: 603}},
		{"The Matrix (1999) [imdbid-tt0133093]", Guess{Title: "The Matrix", Year: 1999, IMDBID: "tt0133093"}},
		{"Severance {tvdb-371980}", Guess{Title: "Severance", TVDBID: 371980}},
		{"Severance", Guess{Title: "Severance"}},
		{"Severance.S01.1080p.WEB-DL", Guess{Title: "Severance"}},
		{"Mr. Robot", Guess{Title: "Mr. Robot"}},
		{"Marvel's Agents of S.H.I.E.L.D. (2013)", Guess{Title: "Marvel's Agents of S.H.I.E.L.D.", Year: 2013}},
	} {
		if got := ParseName(c.in); got != c.want {
			t.Errorf("ParseName(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestSame(t *testing.T) {
	if !same("The Lord of the Rings: The Return of the King", "Lord of the Rings - The Return of the King") || !same("Law & Order", "Law and Order") || same("Heat", "Heater") {
		t.Fatal("same")
	}
}

func TestEpisodeCode(t *testing.T) {
	if s, e, ok := episodeCode("Severance - S01E02 - Half Loop.mkv"); !ok || s != 1 || e != 2 {
		t.Fatal(s, e, ok)
	}
	if s, e, ok := episodeCode("show.2x10.mkv"); !ok || s != 2 || e != 10 {
		t.Fatal(s, e, ok)
	}
	if _, _, ok := episodeCode("The Matrix 1999.mkv"); ok {
		t.Fatal("a movie read as an episode")
	}
}
