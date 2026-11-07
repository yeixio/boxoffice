package quality

import (
	"regexp"
	"slices"
	"strings"
)

// Languages, for a copy reported in Matinee as being in the wrong
// language: what a release's name says its sound is in, and what a file's
// audio tracks are tagged as.

// language is one language: its ISO 639-1 code, its English name, the
// ISO 639-2 codes files tag tracks with, and the words release names use.
type language struct {
	code, name string
	iso2       []string
	words      []string
}

var languages = []language{
	{"en", "English", []string{"eng"}, []string{"english", "eng"}},
	{"de", "German", []string{"ger", "deu"}, []string{"german", "deutsch", "ger"}},
	{"fr", "French", []string{"fre", "fra"}, []string{"french", "truefrench", "vff", "vfq", "vf2", "vfi", "fre"}},
	{"es", "Spanish", []string{"spa"}, []string{"spanish", "castellano", "latino", "esp", "spa"}},
	{"it", "Italian", []string{"ita"}, []string{"italian", "ita"}},
	{"pt", "Portuguese", []string{"por"}, []string{"portuguese", "brazilian", "dublado", "por"}},
	{"nl", "Dutch", []string{"dut", "nld"}, []string{"dutch", "flemish"}},
	{"sv", "Swedish", []string{"swe"}, []string{"swedish", "swe"}},
	{"da", "Danish", []string{"dan"}, []string{"danish", "dan"}},
	{"no", "Norwegian", []string{"nor", "nob", "nno"}, []string{"norwegian", "nor"}},
	{"fi", "Finnish", []string{"fin"}, []string{"finnish", "fin"}},
	{"pl", "Polish", []string{"pol"}, []string{"polish", "pldub", "pol"}},
	{"cs", "Czech", []string{"cze", "ces"}, []string{"czech", "cze"}},
	{"hu", "Hungarian", []string{"hun"}, []string{"hungarian", "hun"}},
	{"ru", "Russian", []string{"rus"}, []string{"russian", "rus"}},
	{"uk", "Ukrainian", []string{"ukr"}, []string{"ukrainian", "ukr"}},
	{"tr", "Turkish", []string{"tur"}, []string{"turkish", "tur"}},
	{"el", "Greek", []string{"gre", "ell"}, []string{"greek"}},
	{"ja", "Japanese", []string{"jpn"}, []string{"japanese", "jpn"}},
	{"ko", "Korean", []string{"kor"}, []string{"korean", "kor"}},
	{"zh", "Chinese", []string{"chi", "zho"}, []string{"chinese", "mandarin", "cantonese", "chi"}},
	{"hi", "Hindi", []string{"hin"}, []string{"hindi", "hin"}},
	{"ta", "Tamil", []string{"tam"}, []string{"tamil", "tam"}},
	{"te", "Telugu", []string{"tel"}, []string{"telugu", "tel"}},
	{"ar", "Arabic", []string{"ara"}, []string{"arabic", "ara"}},
	{"he", "Hebrew", []string{"heb"}, []string{"hebrew", "heb"}},
	{"th", "Thai", []string{"tha"}, []string{"thai"}},
	{"vi", "Vietnamese", []string{"vie"}, []string{"vietnamese"}},
}

// LanguageCode is the ISO 639-1 code for a language given by that code,
// an ISO 639-2 code (as files tag tracks), or its English name; "" when
// it isn't one Box Office knows.
func LanguageCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i] // en-US
	}
	for _, l := range languages {
		if s == l.code || s == strings.ToLower(l.name) || slices.Contains(l.iso2, s) {
			return l.code
		}
	}
	return ""
}

// LanguageName is a language's English name, from its ISO 639-1 code.
func LanguageName(code string) string {
	for _, l := range languages {
		if l.code == code {
			return l.name
		}
	}
	return code
}

var (
	reWords = regexp.MustCompile(`[a-z0-9]+`)
	// Dual-language (German "DL"), multi-language and original-sound tags.
	multiWords = []string{"multi", "multi3", "dual", "dl", "dualaudio", "vostfr", "subbed", "vo", "ov"}
)

// ReleaseLanguages are the languages a release's name says its sound is in,
// and whether it says it has more than one (MULTi, German "DL", Dual
// Audio) or the original sound (VOSTFR, OV). Most names say nothing: such
// releases are nearly always in the movie's own language, often English.
// Words that are also short codes ("ita", "spa") only count after the
// title, from the year or season on, so a movie called "Eng" isn't one.
func ReleaseLanguages(name string) (langs []string, multi bool) {
	words := reWords.FindAllString(strings.ToLower(name), -1)
	start := 0
	for i, w := range words {
		if (len(w) == 4 && (strings.HasPrefix(w, "19") || strings.HasPrefix(w, "20"))) || reSeasonWord.MatchString(w) {
			start = i
			break
		}
	}
	for i, w := range words {
		if w == "dl" && i > 0 && words[i-1] == "web" {
			continue // WEB-DL
		}
		if slices.Contains(multiWords, w) && i >= start {
			multi = true
			continue
		}
		for _, l := range languages {
			if !slices.Contains(l.words, w) || slices.Contains(langs, l.code) {
				continue
			}
			if len(w) <= 3 && i < start {
				continue
			}
			langs = append(langs, l.code)
		}
	}
	return langs, multi
}

var reSeasonWord = regexp.MustCompile(`^s\d{1,2}(e\d{1,3})*$`)

// ReleaseInLanguage says whether a release's name allows that its sound
// is in a language: it names the language, has more than one, or names
// none (the file's tracks then tell, once it's downloaded). A release
// naming only other languages isn't in it. Named says it names the
// language itself, which makes it the likelier pick.
func ReleaseInLanguage(name, code string) (ok, named bool) {
	langs, multi := ReleaseLanguages(name)
	named = slices.Contains(langs, code)
	return named || multi || len(langs) == 0, named
}
