package api

import (
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/zone"
)

// The time zone picked in Settings is the one days and hours are counted
// in, at once; a name that isn't a zone is turned down.
func TestTimeZoneSetting(t *testing.T) {
	_, hs, _ := monitorServer(t)
	t.Cleanup(func() { zone.Set("") })

	var v timeZoneView
	if code := call(t, hs, "GET", "/api/v1/timezone", "", &v); code != 200 || v.Saved != "" || v.Zone == "" {
		t.Fatalf("before: %d %+v", code, v)
	}
	for _, bad := range []string{"Mars/Olympus", "../etc/passwd", "Local"} {
		if code := call(t, hs, "PUT", "/api/v1/timezone", `{"zone":"`+bad+`"}`, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if code := call(t, hs, "PUT", "/api/v1/timezone", `{"zone":"Pacific/Auckland"}`, &v); code != 200 || v.Zone != "Pacific/Auckland" || v.Saved != "Pacific/Auckland" {
		t.Fatalf("pick: %d %+v", code, v)
	}
	if _, offset := v.Now.Zone(); offset < 12*3600 {
		t.Fatalf("the clock isn't Auckland's: %v", v.Now)
	}
	// Half past twelve in the afternoon in UTC is half past midnight the
	// next day in Auckland.
	noon := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	if day := store.DataDay(noon); day != "2026-06-02" {
		t.Fatalf("the day in Auckland at %v is %s", noon, day)
	}
	if now := downloader.Speed(0, downloader.SpeedSchedule{}, nil, noon); now.Zone != "Pacific/Auckland" || now.Clock != "00:30" {
		t.Fatalf("the schedule's clock: %s %s", now.Zone, now.Clock)
	}
	if code := call(t, hs, "PUT", "/api/v1/timezone", `{"zone":""}`, &v); code != 200 || v.Saved != "" || v.Zone == "Pacific/Auckland" {
		t.Fatalf("back to the server's: %d %+v", code, v)
	}
}
