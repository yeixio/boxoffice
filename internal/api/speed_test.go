package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
)

// The schedule is saved whole, overlaps are refused with a reason, and the
// Downloads page's quick change beats it until it's undone.
func TestSpeedScheduleAPI(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: t.TempDir(), Log: s.Log}
	send := func(method, path string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, hs.URL+path, bytes.NewReader(b))
		req.Header.Set("X-Api-Key", "key")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	all := []int{0, 1, 2, 3, 4, 5, 6}
	code, out := send("PUT", "/api/v1/downloads/schedule", downloader.SpeedSchedule{Enabled: true, Ranges: []downloader.SpeedRange{
		{Days: all, From: "00:00", To: "12:00", Mode: downloader.SpeedLimit, Limit: 1024},
		{Days: all, From: "11:00", To: "13:00", Mode: downloader.SpeedFull},
	}})
	if code != 400 || out["error"] == nil {
		t.Fatalf("overlap: %d %v", code, out)
	}
	// All day, every day, so it's the schedule whatever the clock says.
	code, out = send("PUT", "/api/v1/downloads/schedule", downloader.SpeedSchedule{Enabled: true, Ranges: []downloader.SpeedRange{
		{Days: all, From: "00:00", To: "00:00", Mode: downloader.SpeedLimit, Limit: 1024},
	}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	now := out["speedNow"].(map[string]any)
	if now["source"] != "schedule" || now["limit"].(float64) != 1024 || now["zone"] == "" {
		t.Fatalf("now: %v", now)
	}
	// It never changes, so "until it next changes" can't be picked.
	if code, _ := send("PUT", "/api/v1/downloads/speed", map[string]any{"mode": "full", "untilNext": true}); code != 400 {
		t.Fatalf("until next: %d", code)
	}
	code, out = send("PUT", "/api/v1/downloads/speed", map[string]any{"mode": "full", "hours": 1})
	now = out["speedNow"].(map[string]any)
	if code != 200 || now["source"] != "override" || now["mode"] != "full" || now["until"] == nil {
		t.Fatalf("override: %d %v", code, now)
	}
	if code, _ := send("PUT", "/api/v1/downloads/speed", map[string]any{"mode": "limit"}); code != 400 {
		t.Fatalf("limit with no speed: %d", code)
	}
	if code, out = send("DELETE", "/api/v1/downloads/speed", nil); code != 200 || out["speedNow"].(map[string]any)["source"] != "schedule" {
		t.Fatalf("undo: %d %v", code, out)
	}
	if o, _ := downloader.LoadOverride(context.Background(), s.Store, time.Now()); o != nil {
		t.Fatalf("still saved: %+v", o)
	}
}
