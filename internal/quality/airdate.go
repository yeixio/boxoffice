package quality

import (
	"regexp"
	"strconv"
	"time"
)

// Talk shows, news and other daily shows are released by the day they
// aired, not by season and episode: "The.Daily.Show.2026.11.19.1080p".

var reAirDate = regexp.MustCompile(`(?:^|[ ._\-(\[])((?:19|20)\d{2})[ ._\-](\d{2})[ ._\-](\d{2})(?:$|[ ._\-)\]])`)

// AirDate reads the day a release named by date aired (year, month, day
// in that order), or says there's none in its name.
func AirDate(name string) (time.Time, bool) {
	m := reAirDate.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	day := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	// A month or day there isn't (2026.13.40) rolls over: not a date.
	if day.Year() != y || int(day.Month()) != mo || day.Day() != d {
		return time.Time{}, false
	}
	return day, true
}

// SameDay says whether two times fall on the same day, as TMDB gives air
// dates (a day, without an hour).
func SameDay(a, b time.Time) bool {
	a, b = a.UTC(), b.UTC()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
