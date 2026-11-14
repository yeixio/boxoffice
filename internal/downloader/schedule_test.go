package downloader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// Mike's kind of week: slow while people are home and streaming, full
// speed overnight, and a weekend daytime limit.
var week = SpeedSchedule{Enabled: true, Ranges: []SpeedRange{
	{Days: []int{1, 2, 3, 4, 5}, From: "16:00", To: "23:00", Mode: SpeedLimit, Limit: 5000},
	{Days: []int{0, 6}, From: "09:00", To: "23:00", Mode: SpeedLimit, Limit: 2000},
	{Days: []int{0, 1, 2, 3, 4, 5, 6}, From: "23:00", To: "07:00", Mode: SpeedFull},
	{Days: []int{3}, From: "12:00", To: "13:00", Mode: SpeedPause},
}}

func at(day, h, m int) time.Time {
	// 2026-11-15 is a Sunday.
	return time.Date(2026, 11, 15+day, h, m, 0, 0, time.Local)
}

func TestSpeedSchedule(t *testing.T) {
	if err := week.Check(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		t      time.Time
		mode   string
		limit  int64
		source string
		until  time.Time
	}{
		{at(1, 17, 0), SpeedLimit, 5000, FromSchedule, at(1, 23, 0)},
		{at(1, 23, 30), SpeedFull, 0, FromSchedule, at(2, 7, 0)},
		{at(2, 3, 0), SpeedFull, 0, FromSchedule, at(2, 7, 0)},     // past midnight
		{at(2, 8, 0), SpeedLimit, 800, FromSetting, at(2, 16, 0)},  // outside: the setting
		{at(3, 12, 30), SpeedPause, 0, FromSchedule, at(3, 13, 0)}, // Wednesday lunch
		{at(6, 23, 30), SpeedFull, 0, FromSchedule, at(7, 7, 0)},   // Saturday night into Sunday
		{at(0, 2, 0), SpeedFull, 0, FromSchedule, at(0, 7, 0)},     // ... and the same from the week's start
	} {
		got := Speed(800, week, nil, c.t)
		if got.Mode != c.mode || got.Limit != c.limit || got.Source != c.source {
			t.Errorf("%s: %+v", c.t.Format("Mon 15:04"), got.SpeedStep)
		}
		if got.Until == nil || !got.Until.Equal(c.until) {
			t.Errorf("%s: until %v, want %s", c.t.Format("Mon 15:04"), got.Until, c.until.Format("Mon 15:04"))
		}
	}
	// Off, it's the setting all week, and never changes.
	off := week
	off.Enabled = false
	if got := Speed(0, off, nil, at(1, 17, 0)); got.Mode != SpeedFull || got.Source != FromSetting || got.Until != nil {
		t.Fatalf("off: %+v", got)
	}
}

func TestSpeedOverride(t *testing.T) {
	end := at(1, 18, 0)
	o := &SpeedOverride{Mode: SpeedFull, Until: &end}
	got := Speed(0, week, o, at(1, 17, 0))
	if got.Mode != SpeedFull || got.Source != FromOverride || got.Until == nil || !got.Until.Equal(end) || got.Next.Limit != 5000 {
		t.Fatalf("override: %+v next %+v", got, got.Next)
	}
	if got := Speed(0, week, o, at(1, 18, 0)); got.Source != FromSchedule || got.Override != nil {
		t.Fatalf("ran out: %+v", got)
	}
	// Until undone: it holds, with no end.
	if got := Speed(0, week, &SpeedOverride{Mode: SpeedPause}, at(1, 17, 0)); got.Mode != SpeedPause || got.Until != nil {
		t.Fatalf("until undone: %+v", got)
	}
}

func TestScheduleCheck(t *testing.T) {
	for _, c := range []struct {
		r    []SpeedRange
		want string
	}{
		{[]SpeedRange{{Days: []int{1}, From: "7:5", To: "09:00", Mode: SpeedFull}}, "isn't a time"},
		{[]SpeedRange{{Days: []int{1}, From: "24:00", To: "09:00", Mode: SpeedFull}}, "isn't a time"},
		{[]SpeedRange{{Days: nil, From: "07:00", To: "09:00", Mode: SpeedFull}}, "at least one day"},
		{[]SpeedRange{{Days: []int{7}, From: "07:00", To: "09:00", Mode: SpeedFull}}, "the days"},
		{[]SpeedRange{{Days: []int{1}, From: "07:00", To: "09:00", Mode: SpeedLimit}}, "the speed"},
		{[]SpeedRange{{Days: []int{1}, From: "07:00", To: "09:00", Mode: "slow"}}, "pick"},
		{[]SpeedRange{
			{Days: []int{1}, From: "22:00", To: "02:00", Mode: SpeedFull},
			{Days: []int{2}, From: "01:00", To: "03:00", Mode: SpeedPause},
		}, "times 1 and 2 both cover Tuesday at 01:00"},
		{[]SpeedRange{
			{Days: []int{6}, From: "22:00", To: "02:00", Mode: SpeedFull},
			{Days: []int{0}, From: "00:00", To: "01:00", Mode: SpeedPause},
		}, "both cover Sunday at 00:00"},
	} {
		err := SpeedSchedule{Enabled: true, Ranges: c.r}.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.r, err, c.want)
		}
	}
	// All day, every day is one range, not an overlap with itself.
	all := SpeedSchedule{Ranges: []SpeedRange{{Days: []int{0, 1, 2, 3, 4, 5, 6}, From: "00:00", To: "00:00", Mode: SpeedLimit, Limit: 1}}}
	if err := all.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleSaved(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if s, err := LoadSchedule(ctx, st); err != nil || s.Enabled || len(s.Ranges) != 0 {
		t.Fatalf("default %+v %v", s, err)
	}
	if err := SaveSchedule(ctx, st, week); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(time.Hour)
	if err := SaveOverride(ctx, st, &SpeedOverride{Mode: SpeedLimit, Limit: 100, Until: &end}); err != nil {
		t.Fatal(err)
	}
	sp, err := LoadSpeedNow(ctx, st, Settings{}, time.Now())
	if err != nil || sp.Source != FromOverride || sp.Limit != 100 {
		t.Fatalf("now %+v %v", sp, err)
	}
	// After it runs out, it's gone without anyone undoing it.
	if o, _ := LoadOverride(ctx, st, end.Add(time.Second)); o != nil {
		t.Fatalf("ran out: %+v", o)
	}
	SaveOverride(ctx, st, nil)
	if o, _ := LoadOverride(ctx, st, time.Now()); o != nil {
		t.Fatalf("undone: %+v", o)
	}
}
