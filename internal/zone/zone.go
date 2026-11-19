// Package zone is the time zone Box Office keeps its clock in: the speed
// schedule's hours, the day the data cap's month starts, the days daily
// limits count, and the times written into messages.
//
// It's the zone picked in Settings, or else the server's own (the TZ
// setting, or the system's, which in a container is usually UTC).
package zone

import (
	"errors"
	"strings"
	"sync/atomic"
	"time"

	// The zone names come with Box Office, so picking one works where the
	// system has no zone files (a bare container, Windows).
	_ "time/tzdata"
)

// Setting is the settings key of the zone picked: a name such as
// Europe/Berlin, or "" for the server's own.
const Setting = "time_zone"

var picked atomic.Pointer[time.Location]

// Get is the zone in use.
func Get() *time.Location {
	if l := picked.Load(); l != nil {
		return l
	}
	return time.Local
}

// In is t on Box Office's clock.
func In(t time.Time) time.Time { return t.In(Get()) }

// Now is the time on Box Office's clock.
func Now() time.Time { return In(time.Now()) }

// Name is the zone's name, such as Europe/Berlin, or its abbreviation when
// the server's own zone has no name.
func Name() string {
	if name := Get().String(); name != "" && name != "Local" {
		return name
	}
	abbr, _ := Now().Zone()
	return abbr
}

// Load reads a zone by name, as Settings takes them: a name from the zone
// list (Europe/Berlin, America/New_York) or UTC.
func Load(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" || strings.ContainsAny(name, `\. `) || strings.HasPrefix(name, "/") {
		return nil, errors.New("pick a time zone from the list, such as Europe/Berlin")
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, errors.New("there's no time zone called " + name)
	}
	return l, nil
}

// Set uses the zone called name from now on; "" goes back to the server's
// own.
func Set(name string) error {
	if strings.TrimSpace(name) == "" {
		picked.Store(nil)
		return nil
	}
	l, err := Load(name)
	if err != nil {
		return err
	}
	picked.Store(l)
	return nil
}
