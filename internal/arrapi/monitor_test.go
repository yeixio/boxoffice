package arrapi

import (
	"encoding/json"
	"testing"
)

func TestAddMonitor(t *testing.T) {
	for _, c := range []struct {
		body    string
		monitor string
		seasons string
	}{
		// Matinee: "all", every regular season on. Box Office's default decides.
		{`{"addOptions":{"monitor":"all"},"seasons":[{"seasonNumber":0},{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}`, "", "null"},
		{`{"seasons":[{"seasonNumber":0},{"seasonNumber":1,"monitored":true}]}`, "", "null"},
		{`{}`, "", "null"},
		// A request for some seasons keeps them.
		{`{"seasons":[{"seasonNumber":1,"monitored":false},{"seasonNumber":2,"monitored":true}]}`, "", `{"1":false,"2":true}`},
		{`{"addOptions":{"monitor":"latestSeason"},"seasons":[{"seasonNumber":1,"monitored":true}]}`, "latest", "null"},
		{`{"addOptions":{"monitor":"future"}}`, "future", "null"},
		{`{"addOptions":{"monitor":"missing"}}`, "all", "null"},
		{`{"addOptions":{"monitor":"firstSeason"},"seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2}]}`, "all", `{"1":true,"2":false}`},
	} {
		var in titleInput
		if err := json.Unmarshal([]byte(c.body), &in); err != nil {
			t.Fatal(err)
		}
		m, seasons := addMonitor(in)
		got, _ := json.Marshal(seasons)
		if m != c.monitor || string(got) != c.seasons {
			t.Errorf("%s: got %q %s, want %q %s", c.body, m, got, c.monitor, c.seasons)
		}
	}
}
