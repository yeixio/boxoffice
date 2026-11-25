package quality

import "testing"

func TestParsePreRelease(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Film.2026.CAM.x264-GRP", "Cam"},
		{"Film.2026.HDCAM.1080p.x264-GRP", "Cam"},
		{"Film 2026 HD-CAM 720p", "Cam"},
		{"Film.2026.CAMRip.XviD-GRP", "Cam"},
		{"Film.2026.1080p.HDTS.x264-GRP", "Telesync"},
		{"Film.2026.TELESYNC.720p-GRP", "Telesync"},
		{"Film.2026.TS.XviD-GRP", "Telesync"},
		{"Film.2026.720p.HD-TS", "Telesync"},
		{"Film.2026.PDVD.x264-GRP", "Telesync"},
		{"Film.2026.TC.x264-GRP", "Telecine"},
		{"Film.2026.HDTC.1080p-GRP", "Telecine"},
		{"Film.2026.WORKPRINT.XviD-GRP", "Workprint"},
		{"Film.2026.R5.LINE.XviD-GRP", "Regional"},
		{"Film.2026.DVDSCR.x264-GRP", "Screener SD"},
		{"Film.2026.SCR.XviD-GRP", "Screener SD"},
		{"Film.2026.SCREENER.480p.x264-GRP", "Screener SD"},
		{"Film.2026.SCREENER.720p.x264-GRP", "Screener HD"},
		{"Film.2026.BDSCR.1080p-GRP", "Screener HD"},
		{"Film.2026.BDSCR.x264-GRP", "Screener HD"},
		{"Film.2026.WEBSCR.1080p-GRP", "Screener HD"},
		{"Film.2026.DVDSCR.mkv", "Screener SD"},
		// Radarr's own names, as Move in brings them.
		{"CAM", "Cam"}, {"TELESYNC", "Telesync"}, {"TELECINE", "Telecine"}, {"WORKPRINT", "Workprint"},
		{"DVDSCR", "Screener SD"}, {"REGIONAL", "Regional"},
		// Not pre-release copies.
		{"Cam.2018.1080p.WEB-DL.x264-GRP", "WEB 1080p"},
		{"Scrubs.S01E01.720p.WEB-DL", "WEB 720p"},
		{"Scream.2022.1080p.BluRay.x264", "Blu-ray 1080p"},
		{"Film.2026.1080p.WEB-DL.DTS-HD.MA.5.1-GRP", "WEB 1080p"},
		{"Film.2026.1080p.BluRay.x264-TS", "Blu-ray 1080p"},
		{"Film.2026.1080p.WEB-DL.ts", "WEB 1080p"},
		{"/media/movies/Film (2026)/Film (2026) Bluray-1080p.ts", "Blu-ray 1080p"},
		{"Film.2026.2160p.UHD.BluRay.REMUX.HDR.HEVC.TrueHD.7.1-GRP", "Remux 2160p"},
	} {
		if got := Parse(c.name).Name; got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestPreReleaseRanksBelowRealCopies(t *testing.T) {
	sdtv := Rank(1)
	for _, q := range All {
		if q.PreRelease && (Rank(q.ID) >= sdtv || Rank(q.ID) <= Rank(0)) {
			t.Errorf("%s ranks %d, want between Unknown and SDTV", q.Name, Rank(q.ID))
		}
		if !q.PreRelease && q.ID != 0 && Rank(q.ID) < sdtv {
			t.Errorf("%s ranks below SDTV", q.Name)
		}
	}
	// IDs profiles store don't move.
	for id, name := range map[int]string{0: "Unknown", 1: "SDTV", 2: "DVD", 10: "WEB 1080p", 17: "Remux 2160p", 24: "Screener HD"} {
		if q, ok := ByID(id); !ok || q.Name != name {
			t.Errorf("quality %d is %q, want %s", id, q.Name, name)
		}
	}
	seen := map[int]bool{}
	for _, q := range All {
		if seen[q.ID] {
			t.Errorf("quality %d listed twice", q.ID)
		}
		seen[q.ID] = true
	}
}

func TestDefaultsTakeNoPreRelease(t *testing.T) {
	for _, p := range Defaults() {
		for _, id := range p.Allowed {
			if q, _ := ByID(id); q.PreRelease {
				t.Errorf("%s takes %s", p.Name, q.Name)
			}
		}
	}
	dnt := DoNotTouch()
	for _, q := range All {
		if !contains(dnt.Allowed, q.ID) {
			t.Errorf("Do not touch leaves out %s", q.Name)
		}
	}
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestScreenerStillWantsTheRealCopy(t *testing.T) {
	p := Profile{Name: "HD", Allowed: []int{24, 8, 9, 10, 11}, Cutoff: 10, UpgradeOK: true}
	if !p.Wants(false, 0, 24) {
		t.Fatal("a profile that takes Screener HD should take one when there's no copy")
	}
	if !p.Wants(true, 24, 10) {
		t.Fatal("a screener should still be upgraded to the real copy")
	}
	if p.Wants(true, 10, 24) {
		t.Fatal("a real copy should never be swapped for a screener")
	}
	off := p
	off.UpgradeOK = false
	if off.Wants(true, 24, 10) {
		t.Fatal("with upgrades off a screener stays")
	}
}

func TestCorrectKeepsPreRelease(t *testing.T) {
	anyP := Defaults()[0]
	withScr := Profile{Name: "HD", Allowed: []int{23, 24, 10}, Cutoff: 10, UpgradeOK: true}
	p1080 := Media{Width: 1920, Height: 800}
	p480 := Media{Width: 720, Height: 480}
	for _, c := range []struct {
		name    string
		current int
		file    string
		m       Media
		p       Profile
		want    int
	}{
		{"cam stays a cam", 18, "Film.2026.HDCAM.1080p.mkv", p1080, anyP, 18},
		{"unknown cam by name", 0, "Film.2026.HDCAM.mkv", p1080, anyP, 18},
		{"SD screener that's really HD", 23, "Film.2026.DVDSCR.mkv", p1080, withScr, 24},
		{"HD screener that's really SD", 24, "Film.2026.SCREENER.mkv", p480, withScr, 23},
		{"unknown screener by name", 0, "Film.2026.BDSCR.mkv", p1080, anyP, 24},
		{"real copy never becomes a screener", 0, "Film.2026.mkv", p1080, withScr, 10},
	} {
		if got := Correct(c.current, c.file, c.m, c.p); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
