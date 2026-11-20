package quality

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// A profile can prefer or avoid releases by words in their names: a
// release group (NTb), a way of packing video (x265), a kind of picture
// or sound (HDR, Atmos), or a warning sign (upscaled). It's kept far
// simpler than Radarr's custom formats: three lists and a few ready-made
// presets, no scores to tune.
//
// Among releases of the same quality, one with more preferred words comes
// first and one with an avoided word comes last. A word on the never list
// turns a release down outright.

// Prefs are a profile's release preferences.
type Prefs struct {
	// Presets are the ready-made lists turned on, by id (see Presets).
	Presets []string `json:"presets,omitempty"`
	// Prefer, Avoid and Never are words or release groups, matched whole
	// and without regard to case in the part of a release's name after
	// the title.
	Prefer []string `json:"prefer,omitempty"`
	Avoid  []string `json:"avoid,omitempty"`
	Never  []string `json:"never,omitempty"`
}

// Preset is a ready-made list of words to prefer and avoid.
type Preset struct {
	ID     string   `json:"id"`
	Prefer []string `json:"prefer,omitempty"`
	Avoid  []string `json:"avoid,omitempty"`
}

// Presets are the ready-made lists, in the order the UI offers them.
var Presets = []Preset{
	{
		// The best picture and sound a quality comes in.
		ID:     "best",
		Prefer: []string{"Remux", "Dolby Vision", "DV", "DoVi", "HDR10+", "HDR10", "HDR", "Atmos", "TrueHD", "DTS-HD", "DTS-X"},
		Avoid:  []string{"upscaled", "upscale"},
	},
	{
		// Smaller files for the same quality: the newer ways of packing video.
		ID:     "small",
		Prefer: []string{"x265", "H.265", "H265", "HEVC", "AV1"},
		Avoid:  []string{"Remux"},
	},
	{
		// Releases that tend to disappoint: upscales, hardcoded subtitles,
		// 3D, and groups known for heavy-handed re-encodes.
		ID: "poor",
		Avoid: []string{"upscaled", "upscale", "HC", "KORSUB", "3D", "HSBS", "HOU",
			"YIFY", "YTS", "aXXo", "CM8", "CrEwSaDe", "FaNGDiNG0", "HD2DVD", "iPlanet", "KiNGDOM", "mHD", "mSD", "nHD", "nSD", "nikt0", "STUTTERSHIT", "x0r"},
	},
}

// PresetByID finds a ready-made list.
func PresetByID(id string) (Preset, bool) {
	i := slices.IndexFunc(Presets, func(p Preset) bool { return p.ID == id })
	if i < 0 {
		return Preset{}, false
	}
	return Presets[i], true
}

// Any says whether any preference is set.
func (p Prefs) Any() bool {
	return len(p.Presets)+len(p.Prefer)+len(p.Avoid)+len(p.Never) > 0
}

const (
	maxPrefWords = 100 // in each list
	maxPrefWord  = 60  // letters in one
)

func (p Prefs) check() error {
	for _, id := range p.Presets {
		if _, ok := PresetByID(id); !ok {
			return fmt.Errorf("there's no ready-made preference called %q", id)
		}
	}
	for _, l := range []struct {
		name  string
		words []string
	}{{"prefer", p.Prefer}, {"avoid", p.Avoid}, {"never take", p.Never}} {
		if len(l.words) > maxPrefWords {
			return fmt.Errorf("the %s list has more than %d words", l.name, maxPrefWords)
		}
		for _, w := range l.words {
			switch {
			case strings.TrimSpace(prefNorm(w)) == "":
				return fmt.Errorf("the %s list has a word with no letters or numbers in it: %q", l.name, w)
			case len(w) > maxPrefWord:
				return fmt.Errorf("a word in the %s list is longer than %d letters", l.name, maxPrefWord)
			}
		}
	}
	return nil
}

// Clean tidies the lists as a person typed them: spaces trimmed, empty
// and repeated words dropped.
func (p Prefs) Clean() Prefs {
	tidy := func(in []string) []string {
		var out []string
		seen := map[string]bool{}
		for _, w := range in {
			w = strings.TrimSpace(w)
			if k := prefNorm(w); w != "" && !seen[k] {
				seen[k] = true
				out = append(out, w)
			}
		}
		return out
	}
	p.Prefer, p.Avoid, p.Never = tidy(p.Prefer), tidy(p.Avoid), tidy(p.Never)
	var presets []string
	for _, id := range p.Presets {
		if !slices.Contains(presets, id) {
			presets = append(presets, id)
		}
	}
	p.Presets = presets
	return p
}

// PrefMatch is what a profile's preferences make of one release.
type PrefMatch struct {
	// Preferred and Avoided are the words found in its name, as the
	// lists spell them.
	Preferred []string
	Avoided   []string
	// Never is the first never-take word found, or "".
	Never string
}

// Score orders releases of the same quality: higher is better. Any
// avoided word puts a release below every release without one.
func (m PrefMatch) Score() int { return len(m.Preferred) - 1000*len(m.Avoided) }

var (
	rePrefSep = regexp.MustCompile(`[^a-z0-9+]+`)
	// Where a release's name stops being the title: the episode, or the
	// last year in it.
	rePrefEpisode = regexp.MustCompile(`(?i)\bS\d{1,3}(?:E\d{1,3})?\b`)
	rePrefYear    = regexp.MustCompile(`(?:^|[ ._(\[-])((?:19|20)\d\d)(?:$|[ ._)\]-])`)
)

// prefNorm spells a word or a name the way matching compares them: lower
// case, with every run of other characters as one space.
func prefNorm(s string) string {
	return strings.TrimSpace(rePrefSep.ReplaceAllString(strings.ToLower(s), " "))
}

// prefTail is the part of a release's name after the title, so a word in
// the title itself (a film called Atmos) doesn't count. A name with no
// year or episode in it is taken whole.
func prefTail(name string) string {
	if m := rePrefEpisode.FindStringIndex(name); m != nil {
		return name[m[1]:]
	}
	if all := rePrefYear.FindAllStringSubmatchIndex(name, -1); len(all) > 0 {
		return name[all[len(all)-1][3]:]
	}
	return name
}

// Judge finds the profile's words in a release's name.
func (p Prefs) Judge(name string) PrefMatch {
	var m PrefMatch
	if !p.Any() {
		return m
	}
	tail := " " + prefNorm(prefTail(name)) + " "
	has := func(w string) bool {
		k := prefNorm(w)
		return k != "" && strings.Contains(tail, " "+k+" ")
	}
	add := func(to *[]string, words []string) {
		for _, w := range words {
			if has(w) && !slices.ContainsFunc(*to, func(x string) bool { return prefNorm(x) == prefNorm(w) }) {
				*to = append(*to, w)
			}
		}
	}
	for _, w := range p.Never {
		if has(w) {
			m.Never = w
			break
		}
	}
	// The profile's own lists win over a preset's: a word a preset
	// prefers and the person avoids is avoided, and the other way round.
	add(&m.Preferred, p.Prefer)
	add(&m.Avoided, p.Avoid)
	own := func(words []string, w string) bool {
		return slices.ContainsFunc(words, func(x string) bool { return prefNorm(x) == prefNorm(w) })
	}
	for _, id := range p.Presets {
		ps, _ := PresetByID(id)
		for _, w := range ps.Prefer {
			if !own(p.Avoid, w) && !own(p.Never, w) {
				add(&m.Preferred, []string{w})
			}
		}
		for _, w := range ps.Avoid {
			if !own(p.Prefer, w) {
				add(&m.Avoided, []string{w})
			}
		}
	}
	// A word can't be both: avoided wins, as the warning sign.
	m.Preferred = slices.DeleteFunc(m.Preferred, func(w string) bool { return own(m.Avoided, w) })
	return m
}
