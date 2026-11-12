package quality

import "testing"

func TestReplacementFit(t *testing.T) {
	for _, tc := range []struct {
		have, q, fit int
	}{
		{10, 10, FitSame}, // WEB 1080p for WEB 1080p
		{10, 11, FitSize}, // Blu-ray 1080p for WEB 1080p
		{10, 9, FitSize},  // WEBRip 1080p for WEB 1080p
		{10, 6, FitLower}, // WEB 720p for WEB 1080p
		{10, 17, FitNone}, // Remux 2160p for WEB 1080p
		{10, 15, FitNone}, // WEB 2160p for WEB 1080p
		{10, 12, FitNone}, // Remux 1080p for WEB 1080p
		{12, 12, FitSame}, // Remux 1080p for Remux 1080p
		{12, 11, FitSize}, // Blu-ray 1080p for Remux 1080p
		{17, 16, FitSize}, // Blu-ray 2160p for Remux 2160p
		{0, 17, FitAny},   // unknown copy: nothing to go by
		{10, 0, FitAny},   // unknown release
	} {
		fit, why := ReplacementFit(tc.have, tc.q)
		if fit != tc.fit {
			t.Errorf("ReplacementFit(%d, %d) = %d, want %d", tc.have, tc.q, fit, tc.fit)
		}
		if (fit == FitNone) != (why != "") {
			t.Errorf("ReplacementFit(%d, %d) why %q", tc.have, tc.q, why)
		}
	}
}
