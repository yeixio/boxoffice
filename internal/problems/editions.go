package problems

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A movie's folder can hold more than one cut of it, the theatrical one
// next to the extended one, say. Those aren't copies to choose between:
// once the look inside has said how long each runs, a group of copies
// that are all different cuts becomes an Editions problem, whose fix
// keeps them all, the movie's copy as it is and each other one recorded
// as an edition next to it (store.AddEditionFile), named with its
// {edition-…} tag so Matinee, Plex and Jellyfin can tell them apart too.
// Nothing is set aside, and editions never set off a search.

// labelEditions fills in each copy's edition, from its name or the title
// it gives itself, or else how long it runs against the others.
func labelEditions(files []File) {
	length := func(f File) float64 {
		if f.Media == nil {
			return 0
		}
		return f.Media.Duration
	}
	for i := range files {
		f := &files[i]
		f.Edition, f.ByLength = quality.Edition(filepath.Base(f.Path)), false
		if f.Edition == "" && f.Media != nil && f.Media.Title != "" {
			f.Edition = quality.Edition(f.Media.Title)
		}
		if f.Edition != "" {
			continue
		}
		var others []float64
		for j, o := range files {
			if j != i {
				others = append(others, length(o))
			}
		}
		f.Edition = quality.LengthEdition(length(*f), others, 0)
		f.ByLength = f.Edition != ""
	}
}

// distinctCuts says whether copies are each a different cut. Where both
// running times are known, two copies are when they run notably apart,
// or are both named, for different editions: one named and one not, of
// the same length, are taken for the same cut. Where a time isn't known,
// the names decide, and a copy named for no edition counts as one.
func distinctCuts(files []File) bool {
	if len(files) < 2 {
		return false
	}
	known := func(f File) bool { return f.Media != nil && f.Media.Duration > 0 }
	for i, a := range files {
		for _, b := range files[i+1:] {
			if known(a) && known(b) {
				if quality.DifferentCut(a.Media.Duration, b.Media.Duration) {
					continue
				}
				if a.Edition != "" && !a.ByLength && b.Edition != "" && !b.ByLength && !quality.SameEdition(a.Edition, b.Edition) {
					continue
				}
				return false
			}
			if a.Edition == b.Edition || a.Edition != "" && b.Edition != "" && quality.SameEdition(a.Edition, b.Edition) {
				return false
			}
		}
	}
	return true
}

// asEditions turns a movie's Duplicate problem into an Editions one when
// its copies are different cuts.
func asEditions(p *Problem) {
	if p.Kind != Duplicate || p.Title == nil || p.Title.Kind != "movie" || p.Detail != "" {
		return
	}
	labelEditions(p.Files)
	if !distinctCuts(p.Files) {
		return
	}
	main := mainCut(p.Files)
	fix := &Fix{Action: ActKeepEditions, Keep: main}
	taken := map[string]bool{}
	for _, f := range p.Files {
		if f.Path == main || f.Edition == "" || hasEditionTag(f.Path) {
			continue
		}
		// Named for its cut, so everything reading the folder can tell.
		base := FileName(p.Title.Name, p.Title.Year)
		dest := filepath.Join(filepath.Dir(f.Path), base+" "+quality.EditionTag(f.Edition)+strings.ToLower(filepath.Ext(f.Path)))
		if _, err := os.Lstat(dest); err == nil || taken[strings.ToLower(dest)] {
			// Two cuts both guessed Extended, say: the admin names them.
			fix.Blocked = "output_exists"
		}
		taken[strings.ToLower(dest)] = true
		fix.Moves = append(fix.Moves, withSubtitles(f.Path, dest)...)
	}
	p.Kind, p.Fix = Editions, fix
}

// mainCut is the copy that stays the movie's: the one recorded as it,
// or else the one no edition is named or guessed for, or the Theatrical
// cut, or else the shortest.
func mainCut(files []File) string {
	if i := slices.IndexFunc(files, func(f File) bool { return f.Recorded && (f.record == nil || f.record.Kind == "movie") }); i >= 0 {
		return files[i].Path
	}
	for _, plain := range []func(File) bool{
		func(f File) bool { return f.Edition == "" },
		func(f File) bool { return quality.SameEdition(f.Edition, "Theatrical") },
	} {
		if i := slices.IndexFunc(files, plain); i >= 0 {
			return files[i].Path
		}
	}
	length := func(f File) float64 {
		if f.Media == nil {
			return 0
		}
		return f.Media.Duration
	}
	return slices.MinFunc(files, func(a, b File) int { return cmp.Compare(length(a), length(b)) }).Path
}

var reEditionTag = regexp.MustCompile(`(?i)\{edition-[^}]+\}`)

func hasEditionTag(path string) bool {
	return reEditionTag.MatchString(filepath.Base(path))
}
