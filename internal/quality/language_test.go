package quality

import (
	"slices"
	"testing"
)

func TestReleaseLanguages(t *testing.T) {
	for _, c := range []struct {
		name  string
		langs []string
		multi bool
	}{
		{"The.Matrix.1999.1080p.WEB-DL.x264-GRP", nil, false},
		{"The.Matrix.1999.German.DL.1080p.BluRay.x264-GRP", []string{"de"}, true},
		{"Amelie.2001.TRUEFRENCH.1080p.BluRay-GRP", []string{"fr"}, false},
		{"Movie.2010.MULTi.1080p.BluRay-GRP", nil, true},
		{"Show.S01E02.iTALiAN.720p.WEB-GRP", []string{"it"}, false},
		{"Movie.2010.Hindi.Tamil.Telugu.1080p.WEB-GRP", []string{"hi", "ta", "te"}, false},
		// Short codes count only after the title.
		{"Ita.2015.1080p.BluRay-GRP", nil, false},
		{"Ita.2015.ITA.1080p.BluRay-GRP", []string{"it"}, false},
	} {
		langs, multi := ReleaseLanguages(c.name)
		if !slices.Equal(langs, c.langs) || multi != c.multi {
			t.Errorf("%s: %v %v, want %v %v", c.name, langs, multi, c.langs, c.multi)
		}
	}
	if ok, named := ReleaseInLanguage("Movie.2010.GERMAN.1080p-GRP", "en"); ok || named {
		t.Error("German-only taken for English")
	}
	if ok, named := ReleaseInLanguage("Movie.2010.1080p-GRP", "de"); !ok || named {
		t.Error("an unnamed release is left to the file's tracks")
	}
}

func TestLanguageCode(t *testing.T) {
	for in, want := range map[string]string{"en": "en", "eng": "en", "ger": "de", "deu": "de", "French": "fr", "pt-BR": "pt", "und": "", "": ""} {
		if got := LanguageCode(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
