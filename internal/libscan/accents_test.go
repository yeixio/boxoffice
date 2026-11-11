package libscan

import "testing"

func TestParseNameRepairs(t *testing.T) {
	for in, want := range map[string]string{
		"Amélie (2001)":         "Amélie",
		"Ã\u0086on Flux (2005)": "Æon Flux",
		"LÃ©on (1994)":           "Léon",
	} {
		if g := ParseName(in); g.Title != want {
			t.Errorf("ParseName(%q).Title = %q, want %q", in, g.Title, want)
		}
	}
	if !same("Amelie", "Amélie") || !same("Aeon Flux", "Æon Flux") {
		t.Error("same ignores accents")
	}
}
