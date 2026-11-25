package homemedia

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/yeixio/boxoffice/internal/store"
)

// How alike two photos must look to be shown as look-alikes, and how close
// in time they must have been taken: a photo and an edited or smaller copy
// of it, or the same shot saved twice.
const (
	alikeBits = 4
	alikeTime = 10 * 60 // seconds
)

// Dupes is a group of copies of the same photo or video.
type Dupes struct {
	// Kind is same (the same bytes) or alike (the same picture, such as a
	// smaller or re-saved copy).
	Kind  string           `json:"kind"`
	Items []store.HomeItem `json:"items"`
	// Keep is the one Box Office would keep: the biggest picture, then
	// the biggest file, then the one found first.
	Keep int64 `json:"keep"`
	// Spare is how much space the others take.
	Spare int64 `json:"spare"`
}

// findDupes finds the copies: files with the same bytes first, then photos
// that look the same and were taken at about the same time.
func findDupes(items []store.HomeItem) []Dupes {
	var out []Dupes
	inGroup := map[int64]bool{}
	bySum := map[string][]store.HomeItem{}
	for _, it := range items {
		if it.Sum != "" {
			k := it.Sum + "/" + strconv.FormatInt(it.Size, 10)
			bySum[k] = append(bySum[k], it)
		}
	}
	for _, g := range bySum {
		if len(g) < 2 {
			continue
		}
		for _, it := range g {
			inGroup[it.ID] = true
		}
		out = append(out, group("same", g))
	}
	// Look-alikes: photos only, near in time, with looks a few bits apart.
	var photos []store.HomeItem
	for _, it := range items {
		if it.Kind == "photo" && it.Look != "" && !flat(it.Look) && it.Taken > 0 && !inGroup[it.ID] {
			photos = append(photos, it)
		}
	}
	slices.SortFunc(photos, func(a, b store.HomeItem) int { return cmp.Or(cmp.Compare(a.Taken, b.Taken), cmp.Compare(a.ID, b.ID)) })
	// Union-find over pairs close in time.
	parent := make([]int, len(photos))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range photos {
		for j := i + 1; j < len(photos) && photos[j].Taken-photos[i].Taken <= alikeTime; j++ {
			if lookDistance(photos[i].Look, photos[j].Look) <= alikeBits {
				parent[find(j)] = find(i)
			}
		}
	}
	groups := map[int][]store.HomeItem{}
	for i, it := range photos {
		r := find(i)
		groups[r] = append(groups[r], it)
	}
	for _, g := range groups {
		if len(g) >= 2 {
			out = append(out, group("alike", g))
		}
	}
	// The ones freeing most space first.
	slices.SortFunc(out, func(a, b Dupes) int {
		return cmp.Or(cmp.Compare(b.Spare, a.Spare), cmp.Compare(a.Keep, b.Keep))
	})
	return out
}

func group(kind string, g []store.HomeItem) Dupes {
	slices.SortFunc(g, func(a, b store.HomeItem) int {
		return cmp.Or(
			cmp.Compare(b.Width*b.Height, a.Width*a.Height),
			cmp.Compare(b.Size, a.Size),
			cmp.Compare(a.ID, b.ID))
	})
	d := Dupes{Kind: kind, Items: g, Keep: g[0].ID}
	for _, it := range g[1:] {
		d.Spare += it.Size
	}
	return d
}
