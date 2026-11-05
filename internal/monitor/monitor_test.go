package monitor

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"testing"
)

func TestLogsKeepLatestLines(t *testing.T) {
	logs := NewLogs(5)
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})
	log := slog.New(logs.Handler(inner))
	log.Debug("not kept: the real log doesn't show debug either")
	for i := 0; i < 4; i++ {
		log.Info("scan finished", "library", "Movies", "n", i)
	}
	log.With("component", "scan").WithGroup("stats").Warn("slow share", "path", "/mnt/media", "took", "9s")
	log.Error("request failed", "err", errors.New("disk full"))
	log.Error("request failed", "err", errors.New("disk full"))

	lines, more := logs.Lines(Query{})
	if len(lines) != 5 || more {
		t.Fatalf("lines: %d %v", len(lines), more)
	}
	if lines[0].Level != "error" || lines[0].Message != "request failed" || lines[0].Attrs[0] != (Attr{"err", "disk full"}) {
		t.Fatalf("newest: %+v", lines[0])
	}
	warn := lines[2]
	want := []Attr{{"component", "scan"}, {"stats.path", "/mnt/media"}, {"stats.took", "9s"}}
	if warn.Level != "warn" || len(warn.Attrs) != 3 || warn.Attrs[0] != want[0] || warn.Attrs[1] != want[1] || warn.Attrs[2] != want[2] {
		t.Fatalf("warning: %+v", warn)
	}

	if got, _ := logs.Lines(Query{Level: "warn"}); len(got) != 3 {
		t.Fatalf("warn and up: %+v", got)
	}
	if got, _ := logs.Lines(Query{Text: "MNT/MEDIA"}); len(got) != 1 || got[0].Message != "slow share" {
		t.Fatalf("search: %+v", got)
	}
	page, more := logs.Lines(Query{Limit: 2})
	if len(page) != 2 || !more {
		t.Fatalf("page: %d %v", len(page), more)
	}
	next, _ := logs.Lines(Query{Limit: 2, Before: page[1].Seq})
	if len(next) != 2 || next[0].Seq != page[1].Seq-1 {
		t.Fatalf("next page: %+v", next)
	}

	problems := logs.Problems(0)
	if len(problems) != 2 || problems[0].Message != "request failed" || problems[0].Count != 2 || problems[0].Detail != "disk full" {
		t.Fatalf("problems: %+v", problems)
	}
	if w, e := logs.Counts(); w != 1 || e != 2 {
		t.Fatalf("counts: %d %d", w, e)
	}
	if n, oldest := logs.Kept(); n != 5 || oldest != lines[4].Time {
		t.Fatalf("kept: %d %d", n, oldest)
	}
}

func TestLogsKeepDebugWhenTheLogDoes(t *testing.T) {
	logs := NewLogs(10)
	log := slog.New(logs.Handler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.Debug("http", "path", "/api/v1/home", "value", strings.Repeat("x", maxValue+10))
	lines, _ := logs.Lines(Query{Level: "debug"})
	if len(lines) != 1 || lines[0].Level != "debug" || len(lines[0].Attrs[1].Value) > maxValue+4 {
		t.Fatalf("debug: %+v", lines)
	}
	if got, _ := logs.Lines(Query{Level: "info"}); len(got) != 0 {
		t.Fatalf("info and up: %+v", got)
	}
}

func TestMonitorRates(t *testing.T) {
	m := NewMonitor(NewLogs(10))
	m.Downloads = func() (int, float64) { return 2, 1e6 }
	m.sample()
	m.Traffic.Sent(1 << 20)
	m.Traffic.Answered(200)
	m.Traffic.Answered(502)
	m.sample()
	s := m.System()
	if len(s.History) != 1 || s.History[0].BytesOut <= 0 || s.History[0].ServerErrors != 1 || s.History[0].Downloads != 2 || s.History[0].DownloadRate != 1e6 {
		t.Fatalf("history: %+v", s.History)
	}
	if s.BytesOut != 1<<20 || s.Requests != 2 || s.ServerErrors != 1 || s.Cores != runtime.NumCPU() {
		t.Fatalf("system: %+v", s)
	}
	if runtime.GOOS == "linux" && (s.Memory == 0 || s.MemTotal == 0 || s.Scope != ScopeGroup) {
		t.Fatalf("linux readings: %+v", s)
	}
}

func TestDiskUsage(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip()
	}
	d, ok := DiskUsage(t.TempDir())
	if !ok || d.Total == 0 || d.Free > d.Total {
		t.Fatalf("disk: %+v %v", d, ok)
	}
	if _, ok := DiskUsage("/no/such/folder"); ok {
		t.Fatal("a missing folder has no disk")
	}
}
