package homemedia

import (
	"regexp"
	"strconv"
	"time"

	"github.com/yeixio/boxoffice/internal/zone"
)

// nameDate finds a date (and maybe a time) in a file's name, as phones
// and cameras name files: IMG_20240714_153012.jpg, PXL_20240714_153012345,
// 2024-07-14 15.30.12.jpg, VID-20240714-WA0003.mp4, Screenshot_2024-07-14.
var nameDate = regexp.MustCompile(`(?:^|[^0-9])((?:19[7-9]|20[0-9])[0-9])[-_.]?(0[1-9]|1[0-2])[-_.]?(0[1-9]|[12][0-9]|3[01])(?:[-_. T]{0,3}([01][0-9]|2[0-3])[-_.:h]?([0-5][0-9])[-_.:m]?([0-5][0-9])(?:[0-9]{3})?)?(?:[^0-9]|$)`)

// dateFromName reads the clock time a file's name gives, read as UTC.
func dateFromName(name string) (time.Time, bool) {
	m := nameDate.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	y, mo, d := n(m[1]), n(m[2]), n(m[3])
	t := time.Date(y, time.Month(mo), d, n(m[4]), n(m[5]), n(m[6]), 0, time.UTC)
	// 31 February and the like aren't dates.
	if t.Day() != d || t.After(time.Now().Add(48*time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

// wallClock reads a moment as the clock time it was where Box Office is,
// for times (a video's, a file's) that are moments rather than clock times.
func wallClock(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = zone.Get()
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
}
