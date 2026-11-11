package names

import "testing"

func TestRepair(t *testing.T) {
	for in, want := range map[string]string{
		"Æon Flux":              "Æon Flux",
		"Ã\u0086on Flux":        "Æon Flux", // UTF-8 read as Latin-1
		"A\u0303\u0086on Flux":  "Æon Flux", // and then written as NFD
		"Ã†on Flux":             "Æon Flux", // UTF-8 read as Windows-1252
		"Amélie":               "Amélie",   // NFD, as macOS writes it
		"Café Society":         "Café Society",
		"LÃ©on":                 "Léon",
		"WALLÂ·E":               "WALL·E",
		"ÃƒÂ©":                  "é",        // garbled twice
		"\xc6on Flux":           "Æon Flux", // Latin-1 bytes
		"The Matrix":            "The Matrix",
		"Pokémon":               "Pokémon",
		"東京物語":                  "東京物語",
		"Ã":                     "Ã", // a real Ã on its own stays
		"Ça tourne à Manhattan": "Ça tourne à Manhattan",
	} {
		if got := Repair(in); got != want {
			t.Errorf("Repair(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFold(t *testing.T) {
	for _, s := range []string{"Amélie", "Amélie", "AMELIE", "amelie"} {
		if got := Fold(s); got != "amelie" {
			t.Errorf("Fold(%q) = %q", s, got)
		}
	}
	if got := Fold("Ã\u0086on Flux"); got != "aeon flux" {
		t.Errorf("Fold(garbled Æon Flux) = %q", got)
	}
}
