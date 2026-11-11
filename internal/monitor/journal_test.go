package monitor

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalKeepsThePreviousRun(t *testing.T) {
	dir := t.TempDir()

	// The first start has nothing from before.
	logs := NewLogs(0)
	prev, err := OpenJournal(dir, logs)
	if err != nil {
		t.Fatal(err)
	}
	if !prev.Empty() || prev.Crashed() {
		t.Fatalf("first start: previous run = %+v", prev)
	}
	log := slog.New(logs.Handler(slog.DiscardHandler))
	log.Info("Box Office is running", "version", "1.2.3")
	log.Error("import failed", "err", "disk full")
	logs.CloseJournal()
	// What Go writes when the server crashes.
	crash := "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 7 [running]:\nmain.main()\n"
	if err := os.WriteFile(filepath.Join(dir, journalCrash), []byte(crash), 0o640); err != nil {
		t.Fatal(err)
	}

	// The next start reads them back as the previous run.
	logs2 := NewLogs(0)
	prev, err = OpenJournal(dir, logs2)
	if err != nil {
		t.Fatal(err)
	}
	defer logs2.CloseJournal()
	lines, _ := prev.Logs.Lines(Query{})
	if len(lines) != 2 || lines[0].Message != "import failed" || lines[1].Message != "Box Office is running" {
		t.Fatalf("previous lines = %+v", lines)
	}
	if lines[0].Attrs[0] != (Attr{Key: "err", Value: "disk full"}) {
		t.Errorf("attrs = %+v", lines[0].Attrs)
	}
	if p := prev.Logs.Problems(0); len(p) != 1 || p[0].Detail != "disk full" {
		t.Errorf("previous problems = %+v", p)
	}
	if !prev.Crashed() || prev.CrashSummary() != "panic: runtime error: index out of range [3] with length 3" {
		t.Errorf("crash = %q", prev.Crash)
	}
	if prev.Started == 0 || prev.Ended < prev.Started {
		t.Errorf("span = %d to %d", prev.Started, prev.Ended)
	}
	// This run starts a fresh log and crash file.
	if n, _ := logs2.Kept(); n != 0 {
		t.Errorf("this run has %d lines already", n)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, journalCrash)); len(b) != 0 {
		t.Errorf("crash.log = %q", b)
	}

	// A clean run after that leaves no crash behind.
	slog.New(logs2.Handler(slog.DiscardHandler)).Info("stopped")
	logs2.CloseJournal()
	prev, err = OpenJournal(dir, NewLogs(0))
	if err != nil {
		t.Fatal(err)
	}
	if prev.Crashed() || prev.Empty() {
		t.Errorf("after a clean stop: crashed %v, empty %v", prev.Crashed(), prev.Empty())
	}
}

func TestJournalRotatesAndSkipsCutLines(t *testing.T) {
	old := journalMax
	journalMax = 2000
	defer func() { journalMax = old }()
	dir := t.TempDir()
	logs := NewLogs(0)
	if _, err := OpenJournal(dir, logs); err != nil {
		t.Fatal(err)
	}
	log := slog.New(logs.Handler(slog.DiscardHandler))
	for i := range 100 {
		log.Info("line", "n", i)
	}
	logs.CloseJournal()
	if _, err := os.Stat(filepath.Join(dir, journalOlder)); err != nil {
		t.Fatalf("no older file: %v", err)
	}
	// A crash can cut the last line short.
	f, _ := os.OpenFile(filepath.Join(dir, journalCurrent), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":101,"time":17`)
	f.Close()

	prev, err := OpenJournal(dir, NewLogs(0))
	if err != nil {
		t.Fatal(err)
	}
	lines, _ := prev.Logs.Lines(Query{Limit: 1000})
	if len(lines) == 0 || lines[0].Attrs[0].Value != "99" {
		t.Fatalf("newest previous line = %+v", lines[:min(1, len(lines))])
	}
	// Both files are read, oldest first, so the lines are in order, and
	// together they hold more than one file's worth.
	for i := 1; i < len(lines); i++ {
		if lines[i].Seq >= lines[i-1].Seq || lines[i].Time > lines[i-1].Time {
			t.Fatalf("out of order at %d", i)
		}
	}
	if len(lines) <= int(journalMax)/60 {
		t.Errorf("only %d lines kept", len(lines))
	}
}
