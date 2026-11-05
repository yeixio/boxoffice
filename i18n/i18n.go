// Package i18n holds Box Office's text in every language (locales/, read by
// the web UI) and the list of languages people can pick (languages.json),
// the same languages Matinee has. See README.md.
package i18n

import (
	_ "embed"
	"encoding/json"
)

// Source is the language the catalog is written in, and the last fallback.
const Source = "en"

// Language is one of the languages people can pick.
type Language struct {
	Code   string `json:"code"` // BCP 47, such as de, pt-BR or zh-Hant
	Name   string `json:"name"` // in the language itself, such as Deutsch
	Dir    string `json:"dir"`  // ltr or rtl
	Status string `json:"status"`
}

//go:embed languages.json
var languagesJSON []byte

var languages = func() []Language {
	var out []Language
	if err := json.Unmarshal(languagesJSON, &out); err != nil {
		panic("i18n: languages.json: " + err.Error())
	}
	return out
}()

// Languages lists the languages people can pick, English first.
func Languages() []Language { return languages }

// Has reports whether code is one of the languages, exactly as listed.
func Has(code string) bool {
	for _, l := range languages {
		if l.Code == code {
			return true
		}
	}
	return false
}
