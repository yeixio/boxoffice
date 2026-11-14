package subtitles

import (
	"slices"
	"strings"
)

// Language is one that subtitles can be fetched in.
type Language struct {
	Code string `json:"code"` // ISO 639-1, such as en
	Name string `json:"name"` // in English
}

// languages maps ISO 639-1 codes to their English names and the ISO 639-2
// codes (bibliographic and terminology) that video files are tagged with.
// It's Matinee's list, so what Box Office fetches Matinee can name.
var languages = []struct{ code, name, b, t string }{
	{"ar", "Arabic", "ara", ""}, {"bg", "Bulgarian", "bul", ""}, {"bn", "Bengali", "ben", ""},
	{"ca", "Catalan", "cat", ""}, {"cs", "Czech", "cze", "ces"}, {"da", "Danish", "dan", ""},
	{"de", "German", "ger", "deu"}, {"el", "Greek", "gre", "ell"}, {"en", "English", "eng", ""},
	{"es", "Spanish", "spa", ""}, {"et", "Estonian", "est", ""}, {"eu", "Basque", "baq", "eus"},
	{"fa", "Persian", "per", "fas"}, {"fi", "Finnish", "fin", ""}, {"fr", "French", "fre", "fra"},
	{"ga", "Irish", "gle", ""}, {"gl", "Galician", "glg", ""}, {"he", "Hebrew", "heb", ""},
	{"hi", "Hindi", "hin", ""}, {"hr", "Croatian", "hrv", ""}, {"hu", "Hungarian", "hun", ""},
	{"id", "Indonesian", "ind", ""}, {"is", "Icelandic", "ice", "isl"}, {"it", "Italian", "ita", ""},
	{"ja", "Japanese", "jpn", ""}, {"ko", "Korean", "kor", ""}, {"lt", "Lithuanian", "lit", ""},
	{"lv", "Latvian", "lav", ""}, {"mk", "Macedonian", "mac", "mkd"}, {"ms", "Malay", "may", "msa"},
	{"nb", "Norwegian", "nob", "nor"}, {"nl", "Dutch", "dut", "nld"}, {"pl", "Polish", "pol", ""},
	{"pt", "Portuguese", "por", ""}, {"ro", "Romanian", "rum", "ron"}, {"ru", "Russian", "rus", ""},
	{"sk", "Slovak", "slo", "slk"}, {"sl", "Slovenian", "slv", ""}, {"sr", "Serbian", "srp", ""},
	{"sv", "Swedish", "swe", ""}, {"ta", "Tamil", "tam", ""}, {"te", "Telugu", "tel", ""},
	{"th", "Thai", "tha", ""}, {"tl", "Filipino", "fil", "tgl"}, {"tr", "Turkish", "tur", ""},
	{"uk", "Ukrainian", "ukr", ""}, {"ur", "Urdu", "urd", ""}, {"vi", "Vietnamese", "vie", ""},
	{"zh", "Chinese", "chi", "zho"},
}

var languageIndex = func() map[string]int {
	m := map[string]int{}
	for i, l := range languages {
		for _, k := range []string{l.code, l.b, l.t, strings.ToLower(l.name)} {
			if k != "" {
				m[k] = i
			}
		}
	}
	// Other ways files and apps name them.
	for k, code := range map[string]string{"no": "nb", "nn": "nb", "pob": "pt", "pt-br": "pt", "pt-pt": "pt", "fil": "tl",
		"zh-hans": "zh", "zh-hant": "zh", "zh-cn": "zh", "zh-tw": "zh", "chs": "zh", "cht": "zh", "es-419": "es", "es-la": "es",
		"brazilian": "pt", "farsi": "fa", "norwegian bokmål": "nb"} {
		m[k] = m[code]
	}
	return m
}()

// Languages is every language subtitles can be fetched in.
func Languages() []Language {
	out := make([]Language, len(languages))
	for i, l := range languages {
		out[i] = Language{l.code, l.name}
	}
	slices.SortFunc(out, func(a, b Language) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Code turns a language tag or a part of a file name ("eng", "en",
// "English", "pt-BR") into its ISO 639-1 code, or "" when it's none Box
// Office knows. A region is dropped: "en-GB" is en.
func Code(s string) string {
	k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
	if k == "" || k == "und" || k == "unk" || k == "zxx" || k == "mis" {
		return ""
	}
	if i, ok := languageIndex[k]; ok {
		return languages[i].code
	}
	if base, _, ok := strings.Cut(k, "-"); ok && len(base) <= 3 {
		if i, ok := languageIndex[base]; ok {
			return languages[i].code
		}
	}
	return ""
}

// Name is a language's English name, or its code when unknown.
func Name(code string) string {
	if i, ok := languageIndex[code]; ok {
		return languages[i].name
	}
	return code
}
