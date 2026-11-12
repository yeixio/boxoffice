package quality

import "fmt"

// How well a release suits replacing a copy, best last; FitNone when it
// shouldn't be taken in its place, FitAny when there's nothing to go by.
const (
	FitNone  = -1
	FitAny   = 0
	FitLower = 1 // a lower picture size than the copy
	FitSize  = 2 // the copy's picture size, another source
	FitSame  = 3 // the copy's quality
)

// ReplacementFit says how a release of quality q suits taking the place
// of a bad copy of quality have, so a replacement costs about what the
// copy did: never a bigger picture, and no remux for a copy that wasn't
// one. A 1080p copy isn't replaced by a 2160p remux many times its size.
// The same quality fits best, then the same picture size, then smaller
// ones (a working 720p copy beats a broken 1080p one). why says why a
// release doesn't fit.
func ReplacementFit(have, q int) (fit int, why string) {
	hq, ok := ByID(have)
	if !ok || hq.Resolution == 0 {
		return FitAny, ""
	}
	rq, ok := ByID(q)
	if !ok || rq.Resolution == 0 {
		return FitAny, ""
	}
	switch {
	case q == have:
		return FitSame, ""
	case rq.Resolution > hq.Resolution:
		return FitNone, fmt.Sprintf("it's %dp, bigger than the %s copy it would replace", rq.Resolution, hq.Name)
	case rq.Source == "remux" && hq.Source != "remux":
		return FitNone, fmt.Sprintf("it's a remux, far bigger than the %s copy it would replace", hq.Name)
	case rq.Resolution == hq.Resolution:
		return FitSize, ""
	}
	return FitLower, ""
}
