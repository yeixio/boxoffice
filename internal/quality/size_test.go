package quality

import (
	"strings"
	"testing"
)

func TestCodec(t *testing.T) {
	for name, want := range map[string]string{
		"Movie.2020.1080p.WEB-DL.DDP5.1.H.264-GRP":   "h264",
		"Movie.2020.1080p.BluRay.x264-GRP":           "h264",
		"Movie.2020.1080p.BluRay.AVC.DTS-GRP":        "h264",
		"Movie.2020.2160p.WEB-DL.DV.H265-GRP":        "hevc",
		"Movie 2020 1080p BluRay x265 10bit-GRP":     "hevc",
		"Movie.2020.2160p.UHD.BluRay.HEVC.REMUX-GRP": "hevc",
		"Show.S01E01.1080p.WEB-DL.AV1.Opus-GRP":      "av1",
		"Show.S01E01.1080p.WEB-DL-GRP":               "",
		"Avc1.The.Movie.2020.1080p.WEB-DL-GRP":       "",
	} {
		if got := Codec(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

func TestSizeProblem(t *testing.T) {
	p := Profile{Name: "HD 1080p", Sizes: Sizes{H264: SizeLimit{Min: 5, Max: 100}, HEVC: SizeLimit{Max: 60}, AV1: SizeLimit{Max: 25}}}
	for _, c := range []struct {
		name    string
		size    int64
		minutes int
		want    string
	}{
		{"Movie.2020.1080p.BluRay.x264-GRP", 9e9, 120, ""},                                      // 75 a minute
		{"Movie.2020.1080p.BluRay.x264-GRP", 15e9, 120, "takes at most 100 for H.264"},          // 125
		{"Movie.2020.1080p.BluRay.x264-GRP", 300e6, 120, "takes at least 5 for H.264"},          // 2.5
		{"Movie.2020.1080p.BluRay.x265-GRP", 9e9, 120, "it's 75 MB a minute, and the HD 1080p"}, // over 60
		{"Movie.2020.1080p.BluRay.x265-GRP", 300e6, 120, ""},                                    // no floor for H.265
		{"Show.S01E01.1080p.WEB-DL.AV1-GRP", 2.5e9, 60, "takes at most 25 for AV1"},             // 42
		{"Show.S01E01.1080p.WEB-DL.AV1-GRP", 1.2e9, 60, ""},                                     // 20
		{"Movie.2020.1080p.WEB-DL-GRP", 15e9, 120, "its name doesn't say how it's packed"},      // held to H.264's
		{"Movie.2020.1080p.BluRay.x264-GRP", 15e9, 0, ""},                                       // running time not known
		{"Movie.2020.1080p.BluRay.x264-GRP", 0, 120, ""},                                        // size not known
	} {
		got := p.SizeProblem(c.name, c.size, c.minutes)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s at %d for %d minutes: %q, want %q", c.name, c.size, c.minutes, got, c.want)
		}
	}
	if why := (Profile{Name: "Any"}).SizeProblem("Movie.2020.2160p.REMUX-GRP", 80e9, 90); why != "" {
		t.Errorf("a profile with no limits said %q", why)
	}
	if got := p.SizeProblem("Movie.2020.1080p.BluRay.x264-GRP", 15e9, 120); got != "at 15.0 GB for 120 minutes it's 125 MB a minute, and the HD 1080p profile takes at most 100 for H.264" {
		t.Errorf("words: %s", got)
	}
}

func TestSizesCheck(t *testing.T) {
	ok := Profile{Name: "P", Allowed: []int{10}, Cutoff: 10}
	for why, s := range map[string]Sizes{
		"below zero":            {AV1: SizeLimit{Max: -1}},
		"more than the biggest": {HEVC: SizeLimit{Min: 50, Max: 20}},
		"bigger than any":       {H264: SizeLimit{Max: 1e6}},
	} {
		p := ok
		p.Sizes = s
		if err := p.Check(); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%+v: %v, want %q", s, err, why)
		}
	}
	ok.Sizes = Sizes{H264: SizeLimit{Min: 5}, AV1: SizeLimit{Max: 25}}
	if err := ok.Check(); err != nil {
		t.Error(err)
	}
}
