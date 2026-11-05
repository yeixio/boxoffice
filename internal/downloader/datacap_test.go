package downloader

import (
	"context"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestCapLevels(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	c, err := LoadCap(ctx, st)
	if err != nil || !c.Enabled || c.Limit != 3000*gb || c.UpgradesAt != 70 || c.MissingAt != 90 || c.Level != CapOK {
		t.Fatalf("defaults %+v %v", c, err)
	}
	for _, step := range []struct {
		add  int64
		want string
	}{{2000 * gb, CapOK}, {150 * gb, CapUpgrades}, {600 * gb, CapMissing}, {300 * gb, CapFull}} {
		st.AddDataUsage(ctx, 1, step.add)
		if c, _ = LoadCap(ctx, st); c.Level != step.want {
			t.Fatalf("at %d GB: %q, want %q", c.Used/gb, c.Level, step.want)
		}
	}
	if c.Why() == "" {
		t.Fatal("no reason given")
	}
	SaveCapSettings(ctx, st, CapSettings{Enabled: false, GB: 3000, UpgradesAt: 70, MissingAt: 90, ResetDay: 1})
	if c, _ = LoadCap(ctx, st); c.Level != CapOK || c.Enabled {
		t.Fatalf("off: %+v", c)
	}
}

func TestMonthStart(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.Local) }
	start, next := monthStart(at(2026, 11, 5), 15)
	if start.Day() != 15 || start.Month() != 10 || next.Month() != 11 {
		t.Fatalf("%s %s", start, next)
	}
	if start, _ = monthStart(at(2026, 11, 20), 15); start.Month() != 11 {
		t.Fatal(start)
	}
}

func TestBlockAccounts(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	since := time.Now().Add(-time.Hour)
	list := []store.UsenetServer{
		{ID: 1, Name: "Main", Host: "main", Enabled: true, Connections: 1},
		{ID: 2, Name: "Block", Host: "block", Enabled: true, Connections: 1, Level: 1, Quota: 100, QuotaSince: since},
		{ID: 3, Name: "Old", Host: "old", Enabled: true, Connections: 1, Level: 1, Expires: time.Now().Add(-time.Minute)},
	}
	st.AddDataUsage(ctx, 2, 60)
	qs, err := quotas(ctx, st, list)
	if err != nil || qs[1].Left != -1 || qs[2].Left != 40 || qs[2].Problem != "" || qs[3].Problem == "" {
		t.Fatalf("quotas %+v %v", qs, err)
	}
	var data dataCounter
	srv := newServers(list, nil, qs, &data)
	if len(srv.pools) != 2 || len(srv.skipped) != 1 {
		t.Fatalf("pools %d, skipped %v", len(srv.pools), srv.skipped)
	}
	block := srv.pools[1]
	block.took(30)
	if block.down() {
		t.Fatal("down with 10 left")
	}
	block.took(30)
	if !block.down() {
		t.Fatal("still used past its block")
	}
	if err := data.flush(ctx, st); err != nil {
		t.Fatal(err)
	}
	use, _ := st.DataUsage(ctx, store.DataDay(since))
	if use[2] != 120 {
		t.Fatalf("usage %v", use)
	}
	// With the main server out of the picture too, the reason is given.
	qs[1] = QuotaState{Problem: "its block is used up"}
	qs[2] = QuotaState{Problem: "its block is used up"}
	if why := newServers(list, nil, qs, nil).unusable(); why == "" {
		t.Fatal("no reason")
	}
}
