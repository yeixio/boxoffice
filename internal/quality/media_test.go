package quality

import "testing"

func TestResolution(t *testing.T) {
	for _, c := range []struct{ w, h, want int }{
		{3840, 2160, 2160}, {3840, 1600, 2160}, {1920, 1080, 1080}, {1920, 800, 1080}, {1440, 1080, 1080},
		{1280, 720, 720}, {1280, 536, 720}, {960, 720, 720}, {720, 480, 480}, {720, 576, 480}, {0, 0, 0},
	} {
		if got := (Media{Width: c.w, Height: c.h}).Resolution(); got != c.want {
			t.Errorf("%dx%d = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestCorrect(t *testing.T) {
	hd := Profile{Name: "HD 1080p", Allowed: []int{8, 9, 10, 11, 12}, Cutoff: 10, UpgradeOK: true}
	uhd := Profile{Name: "Ultra HD 4K", Allowed: []int{13, 14, 15, 16, 17}, Cutoff: 15, UpgradeOK: true}
	anyP := Defaults()[0]
	p1080 := Media{Width: 1920, Height: 800}
	p720 := Media{Width: 1280, Height: 720}
	p2160 := Media{Width: 3840, Height: 2160}
	for _, c := range []struct {
		name    string
		current int
		file    string
		m       Media
		p       Profile
		want    int
	}{
		{"size matches: kept", 11, "Movie.mkv", p1080, hd, 11},
		{"size matches, not in profile: kept", 9, "x", p1080, uhd, 9},
		{"no picture: kept", 10, "x", Media{}, hd, 10},
		{"recorded 720p is really 1080p", 7, "Movie.720p.BluRay.mkv", p1080, hd, 11},
		{"recorded 1080p is really 720p, profile has none", 10, "x", p720, hd, 6},
		{"unknown, name gives source", 0, "Movie.BluRay.x264.mkv", p1080, hd, 11},
		{"unknown, name gives nothing: meets cutoff", 0, "Movie (2010).mkv", p1080, hd, 10},
		{"unknown, size only in name: meets cutoff", 0, "Movie.1080p.mkv", p1080, hd, 10},
		{"unknown 4K: meets cutoff", 0, "Movie.mkv", p2160, uhd, 15},
		{"unknown, profile below the size", 0, "Movie.mkv", p2160, hd, 15},
		{"unknown in Any at 720p: best at 720p", 0, "Movie.mkv", p720, anyP, 7},
		{"source not in profile: closest it takes", 0, "Movie.REMUX.mkv", p2160, Profile{Allowed: []int{15, 16}, Cutoff: 15}, 16},
		{"DVD name, HD picture", 2, "Movie.DVDRip.mkv", p1080, hd, 10},
	} {
		if got := Correct(c.current, c.file, c.m, c.p); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
