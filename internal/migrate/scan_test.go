package migrate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitScan(t *testing.T, m *Migrator, id string) Scan {
	t.Helper()
	for range 300 {
		sc, ok := m.ScanByID(id)
		if !ok {
			t.Fatal("the scan went away")
		}
		if sc.Status != "running" {
			return sc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the scan didn't finish")
	return Scan{}
}

func TestScan(t *testing.T) {
	w := newWorld(t)
	ts, calls := fakeRadarr(t, legacyZip(t, "radarr"))

	sc, err := w.m.StartScan(Request{App: "Radarr", URL: ts.URL, APIKey: legacyKey, PathMap: []PathMap{{From: "/movies", To: w.media}}})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Status != "running" || sc.App != "radarr" {
		t.Fatalf("started: %+v", sc)
	}
	// A second app can be looked over at the same time.
	bad, err := w.m.StartScan(Request{App: "radarr", URL: ts.URL, APIKey: "wrong"})
	if err != nil {
		t.Fatal(err)
	}

	sc = waitScan(t, w.m, sc.ID)
	if sc.Status != "ready" || sc.Plan == nil || sc.Plan.Titles.Total != 2 {
		t.Fatalf("ready: %+v", sc)
	}
	if _, ok := calls.Load("backup"); ok {
		t.Fatal("looking it over asked Radarr for a backup")
	}
	if b := waitScan(t, w.m, bad.ID); b.Status != "failed" || !strings.Contains(b.Error, "API key") {
		t.Fatalf("a wrong key: %+v", b)
	}
	if n := len(w.m.Scans()); n != 2 {
		t.Fatalf("%d scans listed", n)
	}

	// Moving in uses the plan, and the scan is then done with.
	id, err := w.m.Run(context.Background(), sc.Plan.Token, Options{})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, w.m, id)
	if _, ok := w.m.ScanByID(sc.ID); ok {
		t.Error("the scan stayed after its plan was used")
	}
	w.m.DismissScan(bad.ID)
	if n := len(w.m.Scans()); n != 0 {
		t.Errorf("%d scans left", n)
	}
}

func TestStartScanChecksFirst(t *testing.T) {
	m := &Migrator{}
	if _, err := m.StartScan(Request{App: "plex", URL: "http://plex"}); err == nil {
		t.Error("an unknown app started")
	}
	if _, err := m.StartScan(Request{App: "radarr"}); err == nil {
		t.Error("no address started")
	}
	if _, err := m.StartScan(Request{App: "radarr", URL: "http://radarr:7878"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("no API key: %v", err)
	}
}
