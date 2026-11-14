package quality

import "testing"

func TestRevisionOf(t *testing.T) {
	for _, c := range []struct {
		name          string
		version, real int
		repack        bool
		word          string
	}{
		{"Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-NTb", 1, 0, false, ""},
		{"Show.S01E02.PROPER.1080p.WEB-DL.DDP5.1.H.264-GRP", 2, 0, false, "PROPER"},
		{"Show.S01E02.REPACK.1080p.WEB.h264-NTb", 2, 0, true, "REPACK"},
		{"Show.S01E02.REPACK2.1080p.WEB.h264-NTb", 3, 0, true, "REPACK"},
		{"Show.S01E02.REAL.PROPER.720p.HDTV.x264-GRP", 2, 1, false, "REAL"},
		{"Movie.2019.RERIP.1080p.BluRay.x264-GRP", 2, 0, true, "REPACK"},
		{"[Group] Show - S01E05v2 [1080p]", 2, 0, false, "v2"},
		{"Show.S01E05v3.1080p.WEB.x264-GRP", 3, 0, false, "v3"},
		// Fix words in the title, before the year or episode, don't count.
		{"Proper.Manors.S01E01.1080p.WEB.h264-GRP", 1, 0, false, ""},
		{"The.Real.Housewives.S10E01.720p.HDTV.x264-GRP", 1, 0, false, ""},
		{"REAL.Sports.S01E01.720p.HDTV.x264-GRP", 1, 0, false, ""},
		{"Repack.2020.1080p.WEB.h264-GRP", 1, 0, false, ""},
		{"Movie 2020 1080p WEB H264 x264 DTS-HD MA 5.1-GRP", 1, 0, false, ""},
	} {
		r := RevisionOf(c.name)
		if r.Version != c.version || r.Real != c.real || r.Repack != c.repack || r.Word() != c.word {
			t.Errorf("RevisionOf(%q) = %+v %q, want version %d real %d repack %v %q", c.name, r, r.Word(), c.version, c.real, c.repack, c.word)
		}
	}
}

func TestReleaseGroup(t *testing.T) {
	for name, want := range map[string]string{
		"Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-NTb":     "NTb",
		"Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-NTb.mkv": "NTb",
		"Show.S01E02.1080p.WEB-DL":                      "",
		"Movie.2020.1080p.BluRay.x264":                  "",
		"Show.S01E02.720p.HDTV.x264-KILLERS[rartv]":     "KILLERS",
		"Show.S01E02.720p.HDTV.x264-KILLERS-Obfuscated": "KILLERS",
		"Show (2020) - S01E02 - Pilot.mkv":              "",
	} {
		if got := ReleaseGroup(name); got != want {
			t.Errorf("ReleaseGroup(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFixOf(t *testing.T) {
	for _, c := range []struct {
		have, release string
		ok            bool
	}{
		{"Show.S01E02.1080p.WEB.h264-NTb", "Show.S01E02.REPACK.1080p.WEB.h264-NTb", true},
		{"Show.S01E02.1080p.WEB.h264-NTb", "Show.S01E02.PROPER.1080p.WEB.h264-GRP", true},
		// A repack fixes its own group's release only.
		{"Show.S01E02.1080p.WEB.h264-NTb", "Show.S01E02.REPACK.1080p.WEB.h264-GRP", false},
		// Without a group to go by, it's taken.
		{"Show (2020) - S01E02 - Pilot.mkv", "Show.S01E02.REPACK.1080p.WEB.h264-GRP", true},
		{"Show.S01E02.PROPER.1080p.WEB.h264-GRP", "Show.S01E02.PROPER.1080p.WEB.h264-OTHER", false},
		{"Show.S01E02.PROPER.1080p.WEB.h264-GRP", "Show.S01E02.REAL.PROPER.1080p.WEB.h264-GRP", true},
		{"Show.S01E02.REPACK.1080p.WEB.h264-NTb", "Show.S01E02.1080p.WEB.h264-NTb", false},
		{"Movie.2019.1080p.BluRay.x264-GRP", "Movie.2019.Directors.Cut.PROPER.1080p.BluRay.x264-GRP", false},
		{"Movie.2019.1080p.BluRay.x264-GRP", "Movie.2019.PROPER.1080p.BluRay.x264-OTHER", true},
	} {
		if ok, why := FixOf(c.have, c.release); ok != c.ok {
			t.Errorf("FixOf(%q, %q) = %v (%s), want %v", c.have, c.release, ok, why, c.ok)
		}
	}
}
