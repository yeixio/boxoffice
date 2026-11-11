package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/monitor"
	"github.com/yeixio/boxoffice/internal/store"
)

func monitorServer(t *testing.T) (*Server, *httptest.Server, *slog.LevelVar) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	level := new(slog.LevelVar)
	logs := monitor.NewLogs(100)
	log := slog.New(logs.Handler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: level})))
	s := &Server{Store: db, Log: log, Version: "test", Database: "sqlite", DataDir: t.TempDir(), EnvAPIKey: "key",
		Monitoring: &Monitoring{Monitor: monitor.NewMonitor(logs), Level: level}}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs, level
}

func call(t *testing.T, hs *httptest.Server, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, hs.URL+path, strings.NewReader(body))
	req.Header.Set("X-Api-Key", "key")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestMonitorCountsAndLogs(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Log.Error("something broke", "err", "disk on fire")
	var rep monitorReport
	if code := call(t, hs, "GET", "/api/v1/admin/monitor", "", &rep); code != 200 {
		t.Fatalf("monitor: %d", code)
	}
	if rep.Version != "test" || rep.DatabaseMs < 0 || len(rep.Problems) != 1 || rep.Problems[0].Detail != "disk on fire" {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Disks) == 0 || rep.Disks[0].Uses[0].Use != "data" || !rep.Disks[0].Reachable {
		t.Fatalf("disks %+v", rep.Disks)
	}
	if rep.Debug {
		t.Fatal("debug on by default")
	}
	if n := s.Monitoring.Monitor.System().Requests; n < 1 {
		t.Fatalf("requests counted: %d", n)
	}
	var logs struct {
		Lines []monitor.Entry `json:"lines"`
	}
	call(t, hs, "GET", "/api/v1/admin/logs?level=error", "", &logs)
	if len(logs.Lines) != 1 || logs.Lines[0].Message != "something broke" {
		t.Fatalf("logs %+v", logs)
	}
}

func TestDebugLoggingSwitch(t *testing.T) {
	s, hs, level := monitorServer(t)
	if code := call(t, hs, "PUT", "/api/v1/admin/debug", `{"on":true}`, nil); code != 200 {
		t.Fatalf("on: %d", code)
	}
	if level.Level() != slog.LevelDebug {
		t.Fatal("not debug")
	}
	// Requests are logged at debug level, without their query strings.
	call(t, hs, "GET", "/api/v1/status?apikey=secret", "", nil)
	lines, _ := s.Monitoring.Monitor.Logs.Lines(monitor.Query{Text: "/api/v1/status"})
	if len(lines) == 0 || lines[0].Level != "debug" {
		t.Fatalf("request not logged: %+v", lines)
	}
	if got, _ := s.Monitoring.Monitor.Logs.Lines(monitor.Query{Text: "secret"}); len(got) != 0 {
		t.Fatalf("query string logged: %+v", got)
	}
	// It stays on across a restart.
	level.Set(slog.LevelInfo)
	s.RestoreDebugLogging(context.Background())
	if level.Level() != slog.LevelDebug {
		t.Fatal("not restored")
	}
	call(t, hs, "PUT", "/api/v1/admin/debug", `{"on":false}`, nil)
	if level.Level() != slog.LevelInfo {
		t.Fatal("still debug")
	}
	s.Monitoring.ForcedDebug = true
	if code := call(t, hs, "PUT", "/api/v1/admin/debug", `{"on":false}`, nil); code != http.StatusConflict {
		t.Fatalf("forced: %d", code)
	}
}

func TestMetricsNeedToken(t *testing.T) {
	_, hs, _ := monitorServer(t)
	res, _ := http.Get(hs.URL + "/metrics")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("off: %d", res.StatusCode)
	}
	var v metricsView
	call(t, hs, "POST", "/api/v1/admin/metrics/token", "", &v)
	if !v.On || v.Token == "" {
		t.Fatalf("token %+v", v)
	}
	req, _ := http.NewRequest("GET", hs.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+v.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, want := range []string{"boxoffice_build_info{version=\"test\"", "boxoffice_http_requests_total", "boxoffice_disk_free_bytes{path="} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in\n%s", want, b)
		}
	}
}

func TestAlertSettingsHideSecrets(t *testing.T) {
	_, hs, _ := monitorServer(t)
	in := `{"email":{"on":true,"host":"smtp.example.com","port":587,"security":"starttls","username":"me","password":"pw","from":"bo@example.com","to":"me@example.com"},
		"webhooks":[{"kind":"discord","url":"https://discord.com/api/webhooks/1/abcdefgh"}],"rules":{"disk":{"on":true,"percent":80}}}`
	var v alertsView
	if code := call(t, hs, "PUT", "/api/v1/admin/alerts", in, &v); code != 200 {
		t.Fatalf("save: %d", code)
	}
	if v.Email.Password != "" || !v.Email.HasPassword || len(v.Webhooks) != 1 || v.Webhooks[0].Hint != "discord.com/…efgh" || v.Rules.Disk.Percent != 80 {
		t.Fatalf("view %+v", v)
	}
	// Saving again without the secrets keeps them.
	again := `{"email":{"on":true,"host":"smtp.example.com","port":587,"security":"starttls","username":"me","from":"bo@example.com","to":"me@example.com"},
		"webhooks":[{"id":"` + v.Webhooks[0].ID + `","kind":"discord"}]}`
	call(t, hs, "PUT", "/api/v1/admin/alerts", again, &v)
	if !v.Email.HasPassword || v.Webhooks[0].Hint != "discord.com/…efgh" {
		t.Fatalf("lost secrets: %+v", v)
	}
	if code := call(t, hs, "PUT", "/api/v1/admin/alerts", `{"webhooks":[{"kind":"discord","url":"nope"}]}`, nil); code != 400 {
		t.Fatalf("bad webhook: %d", code)
	}
}

func TestPreviousRunLogs(t *testing.T) {
	s, hs, _ := monitorServer(t)
	// Nothing from before: no previous run to show.
	var rep monitorReport
	call(t, hs, "GET", "/api/v1/admin/monitor", "", &rep)
	if rep.PreviousRun != nil {
		t.Fatalf("previous run with no journal: %+v", rep.PreviousRun)
	}

	// A run that logged and crashed, read back from the journal.
	dir := t.TempDir()
	before := monitor.NewLogs(0)
	if _, err := monitor.OpenJournal(dir, before); err != nil {
		t.Fatal(err)
	}
	slog.New(before.Handler(slog.DiscardHandler)).Info("importing", "title", "Nosferatu")
	before.CloseJournal()
	os.WriteFile(filepath.Join(dir, "crash.log"), []byte("panic: boom\n\ngoroutine 1 [running]:\n"), 0o600)
	prev, err := monitor.OpenJournal(dir, s.Monitoring.Monitor.Logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Monitoring.Monitor.Logs.CloseJournal() })
	s.Monitoring.Monitor.Previous = prev
	s.Log.Info("this run")

	call(t, hs, "GET", "/api/v1/admin/monitor", "", &rep)
	if rep.PreviousRun == nil || rep.PreviousRun.Lines != 1 || rep.PreviousRun.Crash != "panic: boom" {
		t.Fatalf("previous run = %+v", rep.PreviousRun)
	}
	var page struct {
		Lines []monitor.Entry `json:"lines"`
		Crash string          `json:"crash"`
	}
	call(t, hs, "GET", "/api/v1/admin/logs?run=previous", "", &page)
	if len(page.Lines) != 1 || page.Lines[0].Message != "importing" || !strings.HasPrefix(page.Crash, "panic: boom") {
		t.Fatalf("previous page = %+v", page)
	}

	// This run's download starts with the end of the run before, and its crash.
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/admin/logs/download", nil)
	req.Header.Set("X-Api-Key", "key")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	text := string(b)
	i, j, k := strings.Index(text, "importing"), strings.Index(text, "panic: boom"), strings.Index(text, "this run")
	if i < 0 || j < i || k < j {
		t.Fatalf("download:\n%s", text)
	}
}
