package quality

import "testing"

func TestAirDate(t *testing.T) {
	for name, want := range map[string]string{
		"The.Daily.Show.2026.11.19.Some.Guest.1080p.WEB.h264-GRP": "2026-11-19",
		"Late Night 2026-11-19 720p":                              "2026-11-19",
		"Show (2026 01 05) HDTV":                                  "2026-01-05",
		"Show.2026.13.40.1080p":                                   "",
		"Show.2026.02.30.1080p":                                   "",
		"Show.S01E02.1080p":                                       "",
		"Blade.Runner.2049.1080p":                                 "",
		"Show.2026.1080p.WEB":                                     "",
		"Show.12026.11.19.x":                                      "",
	} {
		day, ok := AirDate(name)
		got := ""
		if ok {
			got = day.Format("2006-01-02")
		}
		if got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
