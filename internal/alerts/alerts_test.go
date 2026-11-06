package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/monitor"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Setting(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}

func (s *memStore) SetSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

// hook is a webhook receiver that keeps what it got.
type hook struct {
	mu   sync.Mutex
	got  []map[string]any
	hdrs []http.Header
	srv  *httptest.Server
}

func newHook(t *testing.T) *hook {
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		v := map[string]any{}
		if json.Unmarshal(b, &v) != nil {
			v = map[string]any{"text": string(b)}
		}
		h.mu.Lock()
		h.got = append(h.got, v)
		h.hdrs = append(h.hdrs, r.Header.Clone())
		h.mu.Unlock()
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) titles() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, v := range h.got {
		out = append(out, v["title"].(string))
	}
	return out
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func setup(t *testing.T, kind string, r *Readings) (*Manager, *hook, *fakeClock) {
	t.Helper()
	h := newHook(t)
	st := &memStore{}
	clock := &fakeClock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	m := &Manager{Store: st, Server: "Home", Version: "v0.1.12", now: clock.now,
		Read: func(context.Context) Readings { return *r }}
	c := Config{Webhooks: []Webhook{{ID: "a", Kind: kind, URL: h.srv.URL}}, Rules: DefaultRules()}
	if err := m.SaveConfig(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return m, h, clock
}

func TestDiskAlertAndAllClear(t *testing.T) {
	r := &Readings{Disks: []Disk{{Name: "data", Path: "/data", Total: 100 << 30, Free: 10 << 30, Reachable: true}}}
	m, h, clock := setup(t, KindJSON, r)
	ctx := context.Background()
	m.Check(ctx)
	m.Check(ctx) // still full: not sent twice
	if got := h.titles(); len(got) != 1 || got[0] != "Disk almost full" {
		t.Fatalf("sent %v", got)
	}
	if len(m.Firing()) != 1 {
		t.Fatalf("firing %v", m.Firing())
	}
	clock.t = clock.t.Add(25 * time.Hour)
	m.Check(ctx) // a day later, still 90%: nothing new to say
	// Starting again (after an update) doesn't send it again either.
	m = &Manager{Store: m.Store, Server: m.Server, Version: m.Version, now: m.now, Read: m.Read}
	m.Check(ctx)
	// Hovering just under the percent and back isn't all clear and full again.
	r.Disks[0].Free = 11 << 30
	m.Check(ctx)
	r.Disks[0].Free = 10 << 30
	m.Check(ctx)
	if got := h.titles(); len(got) != 1 {
		t.Fatalf("sent again %v", got)
	}
	// Five points fuller, and critical, are news.
	r.Disks[0].Free = 5 << 30
	m.Check(ctx)
	r.Disks[0].Free = 4 << 30
	m.Check(ctx)
	r.Disks[0].Free = 1 << 30
	m.Check(ctx)
	r.Disks[0].Free = 50 << 30
	m.Check(ctx)
	got := h.titles()
	if len(got) != 4 || got[1] != "Disk almost full" || got[2] != "Disk almost full" || got[3] != "Disk space is back to normal" {
		t.Fatalf("sent %v", got)
	}
	h.mu.Lock()
	msg, level := h.got[1]["message"], h.got[2]["level"]
	h.mu.Unlock()
	if msg != "The disk with data (/data) is 95% full: 5.0 GB free of 100.0 GB." || level != LevelCritical {
		t.Fatalf("95%%: %v, 99%%: %v", msg, level)
	}
	if len(m.Firing()) != 0 {
		t.Fatalf("still firing %v", m.Firing())
	}
	hist := m.History(ctx)
	if len(hist) != 4 || !hist[0].Resolved || hist[0].Sent != 1 {
		t.Fatalf("history %+v", hist)
	}
}

func TestFolderWaitsBeforeAlerting(t *testing.T) {
	r := &Readings{Disks: []Disk{{Name: "Movies", Path: "/mnt/movies", Library: true}}}
	m, h, clock := setup(t, KindJSON, r)
	ctx := context.Background()
	m.Check(ctx)
	clock.t = clock.t.Add(time.Minute)
	m.Check(ctx)
	if len(h.titles()) != 0 {
		t.Fatalf("sent too soon: %v", h.titles())
	}
	clock.t = clock.t.Add(90 * time.Second)
	m.Check(ctx)
	if got := h.titles(); len(got) != 1 || got[0] != "Root folder can't be read" {
		t.Fatalf("sent %v", got)
	}
}

func TestErrorRate(t *testing.T) {
	r := &Readings{Errors: 5}
	m, h, clock := setup(t, KindJSON, r)
	ctx := context.Background()
	m.Check(ctx) // the baseline
	for i := 0; i < 5; i++ {
		clock.t = clock.t.Add(Interval)
		r.Errors += 1
		m.Check(ctx)
	}
	if len(h.titles()) != 0 {
		t.Fatalf("5 errors shouldn't alert: %v", h.titles())
	}
	r.Errors += 6
	r.Problems = []monitor.Problem{{Level: "error", Message: "probe failed", Detail: "exit status 1", Count: 9, Last: clock.t.UnixMilli()}}
	clock.t = clock.t.Add(Interval)
	m.Check(ctx)
	if got := h.titles(); len(got) != 1 || got[0] != "Errors on the server" {
		t.Fatalf("sent %v", got)
	}
	msg := h.got[0]["message"].(string)
	if !strings.Contains(msg, "11 errors in the last 15 minutes") || !strings.Contains(msg, "probe failed (exit status 1)") {
		t.Fatalf("message %q", msg)
	}
	// Once the errors stop, the window empties without an all-clear.
	clock.t = clock.t.Add(20 * time.Minute)
	m.Check(ctx)
	if len(h.titles()) != 1 || len(m.Firing()) != 0 {
		t.Fatalf("sent %v firing %v", h.titles(), m.Firing())
	}
}

func TestDownloadFailures(t *testing.T) {
	r := &Readings{}
	m, h, clock := setup(t, KindJSON, r)
	ctx := context.Background()
	m.Check(ctx)
	clock.t = clock.t.Add(Interval)
	r.DownloadFailures = 3
	m.Check(ctx)
	if got := h.titles(); len(got) != 1 || got[0] != "Downloads are failing" {
		t.Fatalf("sent %v", got)
	}
}

func TestRulesOff(t *testing.T) {
	r := &Readings{Disks: []Disk{{Name: "data", Path: "/data", Total: 100, Free: 1, Reachable: true}}}
	m, h, _ := setup(t, KindJSON, r)
	ctx := context.Background()
	c, _ := m.Config(ctx)
	c.Rules.Disk.On = false
	m.SaveConfig(ctx, c)
	m.Check(ctx)
	if len(h.titles()) != 0 {
		t.Fatalf("sent %v", h.titles())
	}
}

func TestNoChannelsHoldsAlerts(t *testing.T) {
	r := &Readings{Disks: []Disk{{Name: "data", Path: "/data", Total: 100, Free: 1, Reachable: true}}}
	m, h, _ := setup(t, KindJSON, r)
	ctx := context.Background()
	m.SaveConfig(ctx, Config{Rules: DefaultRules()})
	m.Check(ctx)
	// Adding a channel later still sends what's wrong now.
	m.SaveConfig(ctx, Config{Rules: DefaultRules(), Webhooks: []Webhook{{ID: "a", Kind: KindJSON, URL: h.srv.URL}}})
	m.Check(ctx)
	if got := h.titles(); len(got) != 1 {
		t.Fatalf("sent %v", got)
	}
}

func TestRestart(t *testing.T) {
	r := &Readings{}
	m, h, _ := setup(t, KindJSON, r)
	ctx := context.Background()
	m.Started(ctx)
	m.Stopped(ctx)
	m.Version = "v0.1.13"
	m.Started(ctx) // updated, cleanly
	m.Started(ctx) // never stopped: a crash
	got := h.titles()
	if len(got) != 3 || got[0] != "Box Office started" || got[1] != "Box Office started" || got[2] != "Box Office restarted unexpectedly" {
		t.Fatalf("sent %v", got)
	}
	if msg := h.got[1]["message"].(string); !strings.Contains(msg, "updated from v0.1.12 to v0.1.13") {
		t.Fatalf("message %q", msg)
	}
}

func TestDiscordAndNtfy(t *testing.T) {
	ctx := context.Background()
	h := newHook(t)
	m := &Manager{Store: &memStore{}, Server: "Home", URL: "https://boxoffice.example.com/"}
	c := Config{Webhooks: []Webhook{{ID: "d", Kind: KindDiscord, URL: h.srv.URL}, {ID: "n", Kind: KindNtfy, URL: h.srv.URL + "/alerts", Token: "tk"}}}
	a := m.Test(ctx, c)
	if a.Sent != 2 || len(a.Failed) != 0 {
		t.Fatalf("test %+v", a)
	}
	var discord, ntfy int = -1, -1
	for i, v := range h.got {
		if _, ok := v["embeds"]; ok {
			discord = i
		} else {
			ntfy = i
		}
	}
	if discord < 0 || ntfy < 0 {
		t.Fatalf("got %v", h.got)
	}
	embed := h.got[discord]["embeds"].([]any)[0].(map[string]any)
	if embed["title"] != "Test alert" || embed["url"] != "https://boxoffice.example.com/health" {
		t.Fatalf("discord %v", embed)
	}
	hd := h.hdrs[ntfy]
	if hd.Get("Title") != "Home: Test alert" || hd.Get("Authorization") != "Bearer tk" || hd.Get("Click") == "" {
		t.Fatalf("ntfy headers %v", hd)
	}
}

func TestFailuresAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unknown webhook", http.StatusNotFound)
	}))
	defer srv.Close()
	var mailed string
	m := &Manager{Store: &memStore{}, SendMail: func(_ context.Context, e Email, subject, body string) error {
		mailed = subject + "\n" + body
		return nil
	}}
	c := Config{
		Email:    Email{On: true, Host: "mail.example.com", Port: 587, Security: "starttls", From: "matinee@example.com", To: "me@example.com"},
		Webhooks: []Webhook{{ID: "d", Kind: KindDiscord, Name: "Discord", URL: srv.URL}},
	}
	a := m.Test(context.Background(), c)
	if a.Sent != 1 || len(a.Failed) != 1 || a.Failed[0] != "Discord: answered 404: unknown webhook" {
		t.Fatalf("test %+v", a)
	}
	if !strings.HasPrefix(mailed, "[Box Office] Test alert\n") {
		t.Fatalf("mailed %q", mailed)
	}
}

func TestChecks(t *testing.T) {
	if CheckWebhook(Webhook{Kind: KindDiscord, URL: "discord.com/api/webhooks/1"}) == nil {
		t.Error("an address without a scheme passed")
	}
	if CheckWebhook(Webhook{Kind: "slack", URL: "https://x"}) == nil {
		t.Error("an unknown kind passed")
	}
	if CheckEmail(Email{On: true, Host: "h", Port: 25, Security: "none", From: "a@b.c", To: "x@y.z, w@y.z"}) != nil {
		t.Error("good email settings failed")
	}
	if CheckEmail(Email{On: true, Host: "h", Port: 25, Security: "none", From: "a@b.c", To: ""}) == nil {
		t.Error("no recipients passed")
	}
}

func TestNormalize(t *testing.T) {
	c := Config{Rules: Rules{Disk: DiskRule{Percent: 10}, Errors: RateRule{Count: -1, Minutes: 99999}}}
	c.Normalize()
	if c.Rules.Disk.Percent != 50 || c.Rules.Errors.Count != 1 || c.Rules.Errors.Minutes != maxWindow || c.Email.Port != 587 {
		t.Fatalf("%+v", c)
	}
}

func TestMessage(t *testing.T) {
	from := &mail.Address{Name: "Matinee", Address: "matinee@example.com"}
	b := string(message(from, []*mail.Address{{Address: "me@example.com"}}, "[Home] Disk almost full", "line one\nline two\n"))
	for _, want := range []string{"From: \"Matinee\" <matinee@example.com>\r\n", "To: <me@example.com>\r\n", "Subject: [Home] Disk almost full\r\n", "\r\n\r\nline one\r\nline two\r\n"} {
		if !strings.Contains(b, want) {
			t.Errorf("message lacks %q:\n%s", want, b)
		}
	}
}

// With nowhere else set up, Forward (Matinee's notifications) is enough
// for alerts to go out.
func TestForwardAlone(t *testing.T) {
	r := &Readings{Disks: []Disk{{Name: "data", Path: "/data", Total: 100 << 30, Free: 1 << 30, Reachable: true}}}
	var got []Alert
	m := &Manager{Store: &memStore{}, Read: func(context.Context) Readings { return *r },
		Forward: func(_ context.Context, a Alert) { got = append(got, a) }}
	m.Check(context.Background())
	if len(got) != 1 || got[0].Rule != RuleDisk || got[0].Level != LevelCritical {
		t.Fatalf("forwarded %+v", got)
	}
	if h := m.History(context.Background()); len(h) != 1 || h[0].Sent != 1 {
		t.Fatalf("history %+v", h)
	}
}
