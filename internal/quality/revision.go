package quality

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Revision is how many times a release was put out again to fix it. A
// group whose release turns out flawed (bad audio sync, a missing scene,
// the wrong episode) posts a REPACK; another group posts a PROPER of it;
// a REAL fixes a PROPER that was wrong itself. Anime and some web groups
// write v2, v3 instead.
type Revision struct {
	Version int  `json:"version"` // 1 for a first release, 2 for a PROPER, REPACK or v2, and so on
	Real    int  `json:"real"`    // how many times REAL is written
	Repack  bool `json:"repack"`  // a REPACK or RERIP, which comes from the group that made the first one

	v bool // the version is written v2, v3
}

// Fix says whether a release is a re-release that fixes an earlier one.
func (r Revision) Fix() bool { return r.Version > 1 || r.Real > 0 }

// Newer says whether r fixes a release of revision o: REAL counts first,
// then the version.
func (r Revision) Newer(o Revision) bool {
	if r.Real != o.Real {
		return r.Real > o.Real
	}
	return r.Version > o.Version
}

// Word is how the fix is written: REAL, PROPER, REPACK or v2; "" for a
// first release.
func (r Revision) Word() string {
	switch {
	case r.Real > 0:
		return "REAL"
	case r.Repack:
		return "REPACK"
	case r.Version > 1 && r.v:
		return "v" + strconv.Itoa(r.Version)
	case r.Version > 1:
		return "PROPER"
	}
	return ""
}

var (
	reFixWord = regexp.MustCompile(`(?i)\b(proper|repack|rerip)(\d)?\b`)
	reVersion = regexp.MustCompile(`(?i)(?:\b|\d)v([2-9])\b`)
	reReal    = regexp.MustCompile(`\bREAL\b`) // in capitals only: "Real Housewives" isn't one
	// reNameEnd is where a release's title ends: its episode number or its
	// year. A fix word before it is part of the title ("Proper Manors").
	reNameEnd = regexp.MustCompile(`(?i)\bS\d{1,2}(?:[ ._-]?E\d{1,4})*\b|\b\d{1,2}x\d{2,3}\b|(?:^|[ ._(\[-])(?:19|20)\d\d(?:$|[ ._)\]-])`)
)

// RevisionOf reads a release's or file's revision from its name.
func RevisionOf(name string) Revision {
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	if loc := reNameEnd.FindAllStringIndex(name, -1); len(loc) > 0 {
		// The last year-like number is the year; an earlier one can be
		// part of the title (2001 A Space Odyssey 1968). Episode numbers
		// come once.
		name = name[loc[len(loc)-1][0]:]
	}
	r := Revision{Version: 1}
	for _, m := range reFixWord.FindAllStringSubmatch(name, -1) {
		v := 2
		if m[2] != "" {
			n, _ := strconv.Atoi(m[2])
			v = max(n+1, 2)
		}
		r.Version = max(r.Version, v)
		if w := strings.ToLower(m[1]); w == "repack" || w == "rerip" {
			r.Repack = true
		}
	}
	for _, m := range reVersion.FindAllStringSubmatch(name, -1) {
		n, _ := strconv.Atoi(m[1])
		if n > r.Version {
			r.Version, r.v = n, true
		}
	}
	r.Real = len(reReal.FindAllString(name, -1))
	return r
}

var reGroup = regexp.MustCompile(`-([A-Za-z0-9]{2,24})$`)

// notGroups end a release's name after a dash without being the group
// that made it.
var notGroups = map[string]bool{"dl": true, "hd": true, "rip": true, "dts": true, "ma": true, "x": true, "es": true,
	"obfuscated": true, "postbot": true, "xpost": true, "sample": true, "scrambled": true, "rp": true}

// ReleaseGroup is the group that made a release, as the end of its name
// gives it (Show.S01E01.1080p.WEB-DL-NTb → NTb), or "" when it doesn't.
func ReleaseGroup(name string) string {
	name = strings.TrimSpace(name)
	if ext := filepath.Ext(name); len(ext) >= 3 && len(ext) <= 5 && !strings.ContainsAny(ext, " -") {
		if _, err := strconv.Atoi(ext[1:]); err != nil {
			name = strings.TrimSuffix(name, ext)
		}
	}
	// Trailing tags some posters add: [rartv], [eztv].
	if i := strings.LastIndex(name, "["); i > 0 && strings.HasSuffix(name, "]") {
		name = strings.TrimSpace(name[:i])
	}
	for range 2 {
		m := reGroup.FindStringSubmatch(name)
		if m == nil {
			return ""
		}
		if !notGroups[strings.ToLower(m[1])] {
			return m[1]
		}
		if strings.ToLower(m[1]) == "dl" || strings.ToLower(m[1]) == "hd" {
			return "" // WEB-DL, DTS-HD: nothing after it
		}
		name = strings.TrimSuffix(name, m[0])
	}
	return ""
}

// FixOf says whether release is a fixed re-release of the copy that came
// from have (a release or file name): the same edition, a newer revision,
// and, for a REPACK, from the same group, as a repack only fixes its own
// group's release. A PROPER can come from any group. why says why not,
// when have's name is known and release is a fix of something.
func FixOf(have, release string) (ok bool, why string) {
	rv := RevisionOf(release)
	if !rv.Fix() {
		return false, ""
	}
	hv := RevisionOf(have)
	if !rv.Newer(hv) {
		return false, "the copy there is that fix already, or a later one"
	}
	if !SameEdition(Edition(have), Edition(release)) {
		return false, "it's another edition"
	}
	if rv.Repack && rv.Real == 0 && !reProperWord.MatchString(release) {
		hg, rg := ReleaseGroup(have), ReleaseGroup(release)
		if hg != "" && rg != "" && !strings.EqualFold(hg, rg) {
			return false, "it's a repack of " + rg + "'s release, and the copy there is " + hg + "'s"
		}
	}
	return true, ""
}

var reProperWord = regexp.MustCompile(`(?i)\bproper\d?\b`)
