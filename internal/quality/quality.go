// Package quality names the kinds of copy Box Office can want for a movie
// or episode, ranked from worst to best, and the profiles that say which of
// them a title takes and when to stop upgrading.
package quality

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Quality is one kind of copy, such as a 1080p web download.
type Quality struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`     // tv, web, webrip, dvd, bluray, remux, unknown
	Resolution int    `json:"resolution"` // vertical lines; 0 when unknown
}

// All lists every quality from worst to best. IDs never change: profiles
// store them.
var All = []Quality{
	{0, "Unknown", "unknown", 0},
	{1, "SDTV", "tv", 480},
	{2, "DVD", "dvd", 480},
	{3, "WEB 480p", "web", 480},
	{4, "HDTV 720p", "tv", 720},
	{5, "WEBRip 720p", "webrip", 720},
	{6, "WEB 720p", "web", 720},
	{7, "Blu-ray 720p", "bluray", 720},
	{8, "HDTV 1080p", "tv", 1080},
	{9, "WEBRip 1080p", "webrip", 1080},
	{10, "WEB 1080p", "web", 1080},
	{11, "Blu-ray 1080p", "bluray", 1080},
	{12, "Remux 1080p", "remux", 1080},
	{13, "HDTV 2160p", "tv", 2160},
	{14, "WEBRip 2160p", "webrip", 2160},
	{15, "WEB 2160p", "web", 2160},
	{16, "Blu-ray 2160p", "bluray", 2160},
	{17, "Remux 2160p", "remux", 2160},
}

// ByID finds a quality.
func ByID(id int) (Quality, bool) {
	i := slices.IndexFunc(All, func(q Quality) bool { return q.ID == id })
	if i < 0 {
		return Quality{}, false
	}
	return All[i], true
}

// Rank orders qualities: higher is better.
func Rank(id int) int {
	return slices.IndexFunc(All, func(q Quality) bool { return q.ID == id })
}

// Profile says which qualities a title takes and the one at which Box
// Office stops looking for a better copy.
type Profile struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Allowed   []int  `json:"allowed"` // quality IDs
	Cutoff    int    `json:"cutoff"`  // a quality ID in Allowed
	UpgradeOK bool   `json:"upgrade"` // keep looking for better copies until the cutoff
}

// Check says what's wrong with a profile, if anything.
func (p Profile) Check() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("give the profile a name")
	}
	if len(p.Allowed) == 0 {
		return errors.New("pick at least one quality")
	}
	for _, id := range p.Allowed {
		if _, ok := ByID(id); !ok {
			return fmt.Errorf("there's no quality %d", id)
		}
	}
	if !slices.Contains(p.Allowed, p.Cutoff) {
		return errors.New("the cutoff has to be one of the qualities the profile takes")
	}
	return nil
}

// Accepts says whether a copy of quality q is one the profile takes.
func (p Profile) Accepts(q int) bool { return slices.Contains(p.Allowed, q) }

// Wants says whether a title whose best copy has quality have (or no copy,
// when has is false) still wants a copy of quality q.
func (p Profile) Wants(has bool, have, q int) bool {
	if !p.Accepts(q) {
		return false
	}
	if !has {
		return true
	}
	if !p.CutoffUnmet(have) {
		return false
	}
	return Rank(q) > Rank(have)
}

// CutoffUnmet says whether a copy of quality have is one the profile
// still looks to upgrade: upgrades are on and it's below the cutoff.
func (p Profile) CutoffUnmet(have int) bool {
	return p.UpgradeOK && Rank(have) < Rank(p.Cutoff)
}

// Defaults are the profiles a new server starts with.
func Defaults() []Profile {
	return []Profile{
		{Name: "Any", Allowed: ids(1, 17), Cutoff: 10, UpgradeOK: true},
		{Name: "HD 1080p", Allowed: []int{8, 9, 10, 11, 12}, Cutoff: 10, UpgradeOK: true},
		{Name: "Ultra HD 4K", Allowed: []int{13, 14, 15, 16, 17}, Cutoff: 15, UpgradeOK: true},
	}
}

func ids(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

var (
	reRes    = regexp.MustCompile(`(?i)\b(2160|1080|720|576|480)[pi]\b|\b(4k|uhd)\b`)
	reRemux  = regexp.MustCompile(`(?i)\bremux\b`)
	reBluray = regexp.MustCompile(`(?i)\b(blu-?ray|bdrip|brrip|bd(25|50)?)\b`)
	reWebRip = regexp.MustCompile(`(?i)\bweb-?rip\b`)
	reWeb    = regexp.MustCompile(`(?i)\bweb(-?dl)?\b|\b(amzn|nf|dsnp|hmax|atvp|hulu)\b`)
	reHDTV   = regexp.MustCompile(`(?i)\b(hdtv|pdtv|sdtv|tvrip)\b`)
	reDVD    = regexp.MustCompile(`(?i)\b(dvd(rip|r|5|9)?)\b`)
)

// Parse guesses a release's or file's quality from its name.
func Parse(name string) Quality {
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	res := 0
	if m := reRes.FindStringSubmatch(name); m != nil {
		switch {
		case m[2] != "":
			res = 2160
		default:
			fmt.Sscan(m[1], &res)
		}
	}
	if res == 576 {
		res = 480
	}
	source := ""
	switch {
	case reRemux.MatchString(name):
		source = "remux"
	case reBluray.MatchString(name):
		source = "bluray"
	case reWebRip.MatchString(name):
		source = "webrip"
	case reWeb.MatchString(name):
		source = "web"
	case reHDTV.MatchString(name):
		source = "tv"
	case reDVD.MatchString(name):
		source = "dvd"
	}
	if source == "" && res == 0 {
		return All[0]
	}
	if source == "" {
		source = "web" // the commonest source today, when a name only gives the size
	}
	if res == 0 {
		res = map[string]int{"dvd": 480, "tv": 480, "web": 1080, "webrip": 1080, "bluray": 1080, "remux": 1080}[source]
	}
	if source == "remux" && res < 1080 {
		res = 1080
	}
	if source == "dvd" {
		return All[2]
	}
	for _, q := range All {
		if q.Source == source && q.Resolution == res {
			return q
		}
	}
	// Combinations the list doesn't have, like 480p Blu-ray, go to the nearest.
	for _, q := range slices.Backward(All) {
		if q.Resolution == res {
			return q
		}
	}
	return All[0]
}
