package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLanguages(t *testing.T) {
	if len(Languages()) < 30 || Languages()[0].Code != Source {
		t.Fatalf("languages: %v", Languages())
	}
	for _, l := range Languages() {
		if l.Name == "" || (l.Dir != "ltr" && l.Dir != "rtl") {
			t.Errorf("%s: name %q dir %q", l.Code, l.Name, l.Dir)
		}
		// Every listed language has its catalog.
		if _, err := os.Stat(filepath.Join("locales", l.Code, "web.json")); err != nil {
			t.Errorf("%s: %v", l.Code, err)
		}
	}
	if !Has("pt-BR") || Has("pt") || Has("") {
		t.Error("Has")
	}
}
