// Package alerts tells admins when something needs them: a disk that's
// nearly full, a root folder that can't be read, a run of errors,
// downloads that keep failing, a backup that failed, or the server
// starting again. Alerts go
// straight from the server to the admin's own email server or webhooks
// (Discord, ntfy, Home Assistant or any other); nothing goes through anyone
// else's servers.
package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/monitor"
)

// Settings keys.
const (
	settingConfig  = "alerts"
	settingHistory = "alerts_history"
	settingRun     = "alerts_run"
	settingFiring  = "alerts_firing"
)

// A disk alert is sent once when the disk reaches the rule's percent, and
// again only when it's diskStep points fuller (90%, then 95%), not every
// day. It's all clear once the disk is diskSlack points under the percent,
// so a disk that hovers around it doesn't come and go.
const (
	diskStep  = 5
	diskSlack = 2
)

// Rule names, as alerts and the settings call them.
const (
	RuleDisk      = "disk"
	RuleFolder    = "folder"
	RuleErrors    = "errors"
	RuleDownloads = "downloads"
	RuleRestart   = "restart"
	RuleBackup    = "backup"
	RuleProblems  = "problems"
	RuleTest      = "test"
)

// Levels.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Config is what admins set up under Health → Alerts.
type Config struct {
	Email    Email     `json:"email"`
	Webhooks []Webhook `json:"webhooks"`
	Rules    Rules     `json:"rules"`
}

// Email sends alerts through the admin's own mail server.
type Email struct {
	On   bool   `json:"on"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// Security is "starttls" (the default), "tls" for a connection that's
	// encrypted from the start (usually port 465), or "none".
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
	// To is one or more addresses, separated by commas.
	To string `json:"to"`
}

// Webhook kinds.
const (
	KindDiscord       = "discord"
	KindNtfy          = "ntfy"
	KindHomeAssistant = "homeassistant"
	KindJSON          = "json"
)

// Webhook posts alerts to an address.
type Webhook struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
	// Token is an ntfy access token, for a topic that needs one.
	Token string `json:"token,omitempty"`
}

// Rules says which alerts to send.
type Rules struct {
	Disk      DiskRule `json:"disk"`
	Folder    Toggle   `json:"folder"`
	Errors    RateRule `json:"errors"`
	Downloads RateRule `json:"downloads"`
	Restart   Toggle   `json:"restart"`
	Backup    Toggle   `json:"backup"`
	// Problems is the daily look at the library finding problem files.
	Problems Toggle `json:"problems"`
}

// Toggle is a rule that's on or off.
type Toggle struct {
	On bool `json:"on"`
}

// DiskRule alerts when a disk is Percent full or more.
type DiskRule struct {
	On      bool `json:"on"`
	Percent int  `json:"percent"`
}

// RateRule alerts when Count or more happen within Minutes.
type RateRule struct {
	On      bool `json:"on"`
	Count   int  `json:"count"`
	Minutes int  `json:"minutes"`
}

// DefaultRules are the rules until an admin changes them: all on.
func DefaultRules() Rules {
	return Rules{
		Disk:      DiskRule{On: true, Percent: 90},
		Folder:    Toggle{On: true},
		Errors:    RateRule{On: true, Count: 10, Minutes: 15},
		Downloads: RateRule{On: true, Count: 3, Minutes: 60},
		Restart:   Toggle{On: true},
		Backup:    Toggle{On: true},
		Problems:  Toggle{On: true},
	}
}

// maxWindow is the longest window a rate rule may have.
const maxWindow = 24 * 60

// Normalize fills in defaults and keeps numbers in range.
func (c *Config) Normalize() {
	if c.Email.Security == "" {
		c.Email.Security = "starttls"
	}
	if c.Email.Port == 0 {
		c.Email.Port = 587
		if c.Email.Security == "tls" {
			c.Email.Port = 465
		}
	}
	if c.Webhooks == nil {
		c.Webhooks = []Webhook{}
	}
	r := &c.Rules
	r.Disk.Percent = clamp(r.Disk.Percent, 50, 99, 90)
	r.Errors.Count = clamp(r.Errors.Count, 1, 10000, 10)
	r.Errors.Minutes = clamp(r.Errors.Minutes, 1, maxWindow, 15)
	r.Downloads.Count = clamp(r.Downloads.Count, 1, 10000, 3)
	r.Downloads.Minutes = clamp(r.Downloads.Minutes, 1, maxWindow, 60)
}

func clamp(v, lo, hi, def int) int {
	switch {
	case v == 0:
		return def
	case v < lo:
		return lo
	case v > hi:
		return hi
	}
	return v
}

// Channels says whether the config sends alerts anywhere.
func (c Config) Channels() int {
	n := len(c.Webhooks)
	if c.Email.On {
		n++
	}
	return n
}

// channels counts where alerts go, Forward included.
func (m *Manager) channels(c Config) int {
	n := c.Channels()
	if m.Forward != nil {
		n++
	}
	return n
}

// Alert is one message to the admins.
type Alert struct {
	ID string `json:"id"`
	// Rule is what raised it; Key tells apart alerts from the same rule,
	// such as one per disk.
	Rule     string `json:"rule"`
	Key      string `json:"key"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Time     int64  `json:"time"` // milliseconds
	Resolved bool   `json:"resolved,omitempty"`
	// Sent is how many channels got it, and Failed says why the others
	// didn't.
	Sent   int      `json:"sent"`
	Failed []string `json:"failed,omitempty"`
}

// Disk is a disk the server uses, for the disk and folder rules.
type Disk struct {
	// Name says what's on it, such as "data, downloads" or "Movies".
	Name      string
	Path      string
	Total     uint64
	Free      uint64
	Reachable bool
	// Library is true for a disk with a root folder (a library) on it.
	Library bool
}

// Readings is the server now, for the rules.
type Readings struct {
	Disks []Disk
	// Errors is how many errors were logged since the server started, and
	// Problems the latest kinds of them.
	Errors   int64
	Problems []monitor.Problem
	// DownloadFailures is how many downloads failed since it started.
	DownloadFailures int64
}

// Store keeps settings.
type Store interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Interval is how often the rules are checked.
const Interval = 30 * time.Second

// historyLength is how many sent alerts are kept for Settings.
const historyLength = 50

// Manager checks the rules and sends alerts.
type Manager struct {
	Store Store
	Log   *slog.Logger
	// Server is the server's name, and URL its address (for a link to
	// the Health page), when it has one.
	Server string
	URL    string
	// Version is the server's version, for the restart alert.
	Version string
	// Read takes the readings; nil reads nothing.
	Read func(ctx context.Context) Readings
	// Client sends webhooks; nil uses a client with a timeout.
	Client *http.Client
	// SendMail sends an email; nil uses the email settings' server.
	SendMail func(ctx context.Context, e Email, subject, body string) error
	// Forward, when set, also hands every alert to Matinee's notifications
	// (or anything else that wants them), whatever the channels are.
	Forward func(ctx context.Context, a Alert)

	now      func() time.Time
	mu       sync.Mutex
	firing   map[string]*firing
	samples  []sample
	history  []Alert
	loaded   bool
	sendLock sync.Mutex
}

type firing struct {
	since    time.Time // when the condition started
	sent     time.Time // when it was last sent; zero while it's held
	alert    Alert
	resolves bool // send an all-clear when it ends
	step     int  // how far past the rule it was when last sent (disks)
}

// firingState is a sent alert as kept in settings, so starting again (after
// an update, say) doesn't send it again.
type firingState struct {
	Since int64 `json:"since"` // milliseconds
	Sent  int64 `json:"sent"`
	Step  int   `json:"step,omitempty"`
	Alert Alert `json:"alert"`
	// Resolves says to send an all-clear when it ends.
	Resolves bool `json:"resolves,omitempty"`
}

type sample struct {
	at        time.Time
	errors    int64
	downloads int64
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Config reads the admin's settings, with defaults for what was never set.
func (m *Manager) Config(ctx context.Context) (Config, error) {
	c := Config{Rules: DefaultRules()}
	v, err := m.Store.Setting(ctx, settingConfig)
	if err != nil {
		return c, err
	}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &c); err != nil {
			return Config{Rules: DefaultRules()}, err
		}
	}
	c.Normalize()
	return c, nil
}

// SaveConfig stores the admin's settings.
func (m *Manager) SaveConfig(ctx context.Context, c Config) error {
	c.Normalize()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return m.Store.SetSetting(ctx, settingConfig, string(b))
}

// Run checks the rules every Interval until ctx ends, after saying the
// server started.
func (m *Manager) Run(ctx context.Context) {
	m.Started(ctx)
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Check(ctx)
		}
	}
}

type runState struct {
	Version string `json:"version"`
	Running bool   `json:"running"`
	Started int64  `json:"started,omitempty"`
}

// Started notes that the server is running, and sends the restart alert:
// whether it was updated, and whether it stopped without shutting down
// cleanly last time.
func (m *Manager) Started(ctx context.Context) {
	var prev runState
	known := false
	if v, err := m.Store.Setting(ctx, settingRun); err == nil && v != "" {
		known = json.Unmarshal([]byte(v), &prev) == nil
	}
	now := m.clock()
	b, _ := json.Marshal(runState{Version: m.Version, Running: true, Started: now.UnixMilli()})
	if err := m.Store.SetSetting(ctx, settingRun, string(b)); err != nil {
		m.logWarn("save run state", err)
	}
	c, err := m.Config(ctx)
	if err != nil || !c.Rules.Restart.On || m.channels(c) == 0 {
		return
	}
	a := Alert{Rule: RuleRestart, Key: RuleRestart, Level: LevelInfo, Title: "Box Office started"}
	var lines []string
	if known && prev.Running {
		a.Level, a.Title = LevelWarning, "Box Office restarted unexpectedly"
		lines = append(lines, "The server stopped without shutting down cleanly, so it may have crashed, been killed, or the machine lost power. The log from before the restart is wherever the server's output goes.")
	} else {
		lines = append(lines, "The server started.")
	}
	if known && prev.Version != "" && prev.Version != m.Version {
		lines = append(lines, fmt.Sprintf("It was updated from %s to %s.", prev.Version, m.Version))
	} else if m.Version != "" {
		lines = append(lines, "Version: "+m.Version+".")
	}
	a.Message = strings.Join(lines, " ")
	m.deliver(ctx, c, a)
}

// BackupFailed tells the admins a database backup failed.
func (m *Manager) BackupFailed(ctx context.Context, err error) {
	c, cerr := m.Config(ctx)
	if cerr != nil || !c.Rules.Backup.On || m.channels(c) == 0 {
		return
	}
	m.deliver(ctx, c, Alert{Rule: RuleBackup, Key: RuleBackup, Level: LevelWarning, Title: "Database backup failed",
		Message: "Box Office couldn't back up its database: " + err.Error() + ". See Backups in Box Office."})
}

// ProblemFiles tells the admins the daily look at the library found new
// problem files.
func (m *Manager) ProblemFiles(ctx context.Context, title, message string) {
	c, err := m.Config(ctx)
	if err != nil || !c.Rules.Problems.On || m.channels(c) == 0 {
		return
	}
	m.deliver(ctx, c, Alert{Rule: RuleProblems, Key: RuleProblems, Level: LevelInfo, Title: title, Message: message})
}

// Stopped notes a clean shutdown, so the next start isn't taken for a crash.
func (m *Manager) Stopped(ctx context.Context) {
	b, _ := json.Marshal(runState{Version: m.Version})
	if err := m.Store.SetSetting(ctx, settingRun, string(b)); err != nil {
		m.logWarn("save run state", err)
	}
}

// condition is a rule that's true now.
type condition struct {
	alert Alert
	// hold is how long it must stay true before it's sent; repeat how
	// often it's sent again while it stays true.
	hold, repeat time.Duration
	resolves     bool
	// step is sent again when it goes up; near is true for one that's
	// only kept while it's already firing (a disk just under the percent).
	step int
	near bool
}

// Check takes readings, and sends alerts for rules that became true (and
// all-clears for those that ended).
func (m *Manager) Check(ctx context.Context) {
	var r Readings
	if m.Read != nil {
		r = m.Read(ctx)
	}
	c, err := m.Config(ctx)
	if err != nil {
		m.logWarn("read alert settings", err)
		return
	}
	now := m.clock()
	conds := m.evaluate(c.Rules, r, now)
	// With nowhere to send them, alerts wait until there is somewhere.
	canSend := m.channels(c) > 0

	m.mu.Lock()
	if m.firing == nil {
		m.firing = m.loadFiring(ctx)
	}
	var send []Alert
	changed := false
	for key, cond := range conds {
		f, ok := m.firing[key]
		if cond.near && (!ok || f.sent.IsZero()) {
			delete(conds, key)
			if ok {
				delete(m.firing, key)
			}
			continue
		}
		if !ok {
			f = &firing{since: now}
			m.firing[key] = f
		}
		if cond.near {
			continue // keep what was sent, with its wording then
		}
		f.alert, f.resolves = cond.alert, cond.resolves
		switch {
		case !canSend:
		case f.sent.IsZero() && now.Sub(f.since) >= cond.hold:
			f.sent, f.step, changed = now, cond.step, true
			send = append(send, f.alert)
		case !f.sent.IsZero() && cond.step > f.step:
			// Fuller by another step: tell them again.
			f.sent, f.step, changed = now, cond.step, true
			send = append(send, f.alert)
		case !f.sent.IsZero() && cond.repeat > 0 && now.Sub(f.sent) >= cond.repeat:
			f.sent, changed = now, true
			a := f.alert
			a.Title += " (still)"
			send = append(send, a)
		}
	}
	for key, f := range m.firing {
		if _, ok := conds[key]; ok {
			continue
		}
		delete(m.firing, key)
		if !f.sent.IsZero() {
			changed = true
		}
		if !f.sent.IsZero() && f.resolves {
			a := f.alert
			a.Level, a.Resolved = LevelInfo, true
			a.Title, a.Message = resolvedText(a)
			send = append(send, a)
		}
	}
	if changed {
		m.saveFiring(ctx)
	}
	m.mu.Unlock()

	for _, a := range send {
		m.deliver(ctx, c, a)
	}
}

// loadFiring reads the alerts that were sent and hadn't ended when the
// server last stopped.
func (m *Manager) loadFiring(ctx context.Context) map[string]*firing {
	out := map[string]*firing{}
	v, err := m.Store.Setting(ctx, settingFiring)
	if err != nil || v == "" {
		return out
	}
	var saved map[string]firingState
	if json.Unmarshal([]byte(v), &saved) != nil {
		return out
	}
	for key, st := range saved {
		if st.Sent == 0 {
			continue
		}
		out[key] = &firing{since: time.UnixMilli(st.Since), sent: time.UnixMilli(st.Sent), step: st.Step, alert: st.Alert, resolves: st.Resolves}
	}
	return out
}

// saveFiring keeps the alerts that were sent and haven't ended. m.mu is held.
func (m *Manager) saveFiring(ctx context.Context) {
	saved := map[string]firingState{}
	for key, f := range m.firing {
		if f.sent.IsZero() {
			continue
		}
		saved[key] = firingState{Since: f.since.UnixMilli(), Sent: f.sent.UnixMilli(), Step: f.step, Alert: f.alert, Resolves: f.resolves}
	}
	b, _ := json.Marshal(saved)
	if err := m.Store.SetSetting(ctx, settingFiring, string(b)); err != nil {
		m.logWarn("save firing alerts", err)
	}
}

func resolvedText(a Alert) (string, string) {
	switch a.Rule {
	case RuleDisk:
		return "Disk space is back to normal", strings.Replace(a.Message, " is ", " was ", 1) + " It has enough free space again."
	case RuleFolder:
		return "Root folder is back", strings.Replace(a.Message, " can't be read.", " can be read again.", 1)
	}
	return a.Title + ": resolved", a.Message
}

// evaluate lists the rules that are true now, by key.
func (m *Manager) evaluate(rules Rules, r Readings, now time.Time) map[string]condition {
	out := map[string]condition{}

	if rules.Disk.On {
		for _, d := range r.Disks {
			if !d.Reachable || d.Total == 0 {
				continue
			}
			used := float64(d.Total-min(d.Free, d.Total)) / float64(d.Total) * 100
			if used < float64(rules.Disk.Percent-diskSlack) {
				continue
			}
			level := LevelWarning
			if used >= 98 {
				level = LevelCritical
			}
			step := int(used-float64(rules.Disk.Percent)) / diskStep
			if level == LevelCritical {
				step = max(step, 100) // critical is always worth telling
			}
			out["disk:"+d.Path] = condition{
				near: used < float64(rules.Disk.Percent), step: step,
				alert: Alert{Rule: RuleDisk, Key: "disk:" + d.Path, Level: level, Title: "Disk almost full",
					Message: fmt.Sprintf("The disk with %s (%s) is %d%% full: %s free of %s.", d.Name, d.Path, int(used), size(d.Free), size(d.Total))},
				resolves: true,
			}
		}
	}
	if rules.Folder.On {
		for _, d := range r.Disks {
			if d.Reachable || !d.Library {
				continue
			}
			out["folder:"+d.Path] = condition{
				alert: Alert{Rule: RuleFolder, Key: "folder:" + d.Path, Level: LevelCritical, Title: "Root folder can't be read",
					Message: fmt.Sprintf("The root folder for %s (%s) can't be read, so Box Office can't check or import files there. If it's on a network share, check the share is still connected and mounted.", d.Name, d.Path)},
				// A share can blip; only one that stays away is worth a message.
				hold: 2 * time.Minute, repeat: 6 * time.Hour, resolves: true,
			}
		}
	}

	// Rates: how many errors and failed downloads since a while ago.
	m.mu.Lock()
	m.samples = append(m.samples, sample{at: now, errors: r.Errors, downloads: r.DownloadFailures})
	keep := now.Add(-time.Duration(maxWindow+1) * time.Minute)
	i := 0
	for i < len(m.samples)-1 && m.samples[i].at.Before(keep) {
		i++
	}
	m.samples = m.samples[i:]
	since := func(minutes int) sample {
		from := now.Add(-time.Duration(minutes) * time.Minute)
		// The latest reading at or before the window started, or the
		// first one there is.
		base := m.samples[0]
		for _, s := range m.samples {
			if s.at.After(from) {
				break
			}
			base = s
		}
		return base
	}
	var errs, fails int64
	if rules.Errors.On {
		errs = r.Errors - since(rules.Errors.Minutes).errors
	}
	if rules.Downloads.On {
		fails = r.DownloadFailures - since(rules.Downloads.Minutes).downloads
	}
	m.mu.Unlock()

	if rules.Errors.On && errs >= int64(rules.Errors.Count) {
		msg := fmt.Sprintf("The server logged %d errors in the last %s.", errs, minutes(rules.Errors.Minutes))
		if top := topErrors(r.Problems, now.Add(-time.Duration(rules.Errors.Minutes)*time.Minute), 3); top != "" {
			msg += " The most common: " + top + "."
		}
		msg += " See Health in Box Office for details."
		out[RuleErrors] = condition{
			alert:  Alert{Rule: RuleErrors, Key: RuleErrors, Level: LevelWarning, Title: "Errors on the server", Message: msg},
			repeat: time.Hour,
		}
	}
	if rules.Downloads.On && fails >= int64(rules.Downloads.Count) {
		out[RuleDownloads] = condition{
			alert: Alert{Rule: RuleDownloads, Key: RuleDownloads, Level: LevelWarning, Title: "Downloads are failing",
				Message: fmt.Sprintf("%d downloads failed in the last %s. Box Office blocks each failed release and looks for another, but if they all fail, check your news servers and indexers. Downloads shows why each one failed.", fails, minutes(rules.Downloads.Minutes))},
			repeat: time.Hour,
		}
	}
	return out
}

// topErrors names the errors logged most often since from.
func topErrors(ps []monitor.Problem, from time.Time, n int) string {
	var errs []monitor.Problem
	for _, p := range ps {
		if p.Level == "error" && p.Last >= from.UnixMilli() {
			errs = append(errs, p)
		}
	}
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Count > errs[j].Count })
	var parts []string
	for _, p := range errs[:min(n, len(errs))] {
		s := p.Message
		if p.Detail != "" {
			d := p.Detail
			if len(d) > 120 {
				d = d[:120] + "…"
			}
			s += " (" + d + ")"
		}
		parts = append(parts, fmt.Sprintf("“%s” ×%d", s, p.Count))
	}
	return strings.Join(parts, ", ")
}

func minutes(n int) string {
	switch {
	case n == 1:
		return "minute"
	case n%60 == 0 && n > 60:
		return strconv.Itoa(n/60) + " hours"
	case n == 60:
		return "hour"
	}
	return strconv.Itoa(n) + " minutes"
}

func size(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

// Firing lists the alerts that are true now and were sent.
func (m *Manager) Firing() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Alert{}
	for _, f := range m.firing {
		if !f.sent.IsZero() {
			out = append(out, f.alert)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// History lists the latest alerts sent, newest first.
func (m *Manager) History(ctx context.Context) []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadHistoryLocked(ctx)
	out := make([]Alert, len(m.history))
	for i, a := range m.history {
		out[len(out)-1-i] = a
	}
	return out
}

func (m *Manager) loadHistoryLocked(ctx context.Context) {
	if m.loaded {
		return
	}
	if v, err := m.Store.Setting(ctx, settingHistory); err == nil {
		m.loaded = true
		if v != "" {
			json.Unmarshal([]byte(v), &m.history)
		}
	}
}

func (m *Manager) remember(ctx context.Context, a Alert) {
	m.mu.Lock()
	m.loadHistoryLocked(ctx)
	m.history = append(m.history, a)
	if len(m.history) > historyLength {
		m.history = append([]Alert{}, m.history[len(m.history)-historyLength:]...)
	}
	b, _ := json.Marshal(m.history)
	m.mu.Unlock()
	if err := m.Store.SetSetting(ctx, settingHistory, string(b)); err != nil {
		m.logWarn("save alert history", err)
	}
}

// Test sends a test alert to every channel in c and says how it went.
func (m *Manager) Test(ctx context.Context, c Config) Alert {
	a := Alert{Rule: RuleTest, Key: RuleTest, Level: LevelInfo, Title: "Test alert",
		Message: "This is a test from Box Office. Alerts from this server will arrive here."}
	return m.deliver(ctx, c, a)
}

var idSeq struct {
	sync.Mutex
	n int64
}

// deliver sends a to every channel, keeps it in the history, and returns
// it with how it went.
func (m *Manager) deliver(ctx context.Context, c Config, a Alert) Alert {
	m.sendLock.Lock()
	defer m.sendLock.Unlock()
	now := m.clock()
	a.Time = now.UnixMilli()
	idSeq.Lock()
	idSeq.n++
	a.ID = strconv.FormatInt(now.UnixMilli(), 36) + "-" + strconv.FormatInt(idSeq.n, 36)
	idSeq.Unlock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	result := func(channel string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			a.Failed = append(a.Failed, channel+": "+err.Error())
			return
		}
		a.Sent++
	}
	msg := a // what's sent, apart from how it went
	if c.Email.On {
		wg.Go(func() { result("Email", m.email(ctx, c.Email, msg)) })
	}
	for _, h := range c.Webhooks {
		wg.Go(func() { result(h.label(), m.webhook(ctx, h, msg)) })
	}
	wg.Wait()
	if m.Forward != nil {
		m.Forward(ctx, msg)
		a.Sent++
	}
	sort.Strings(a.Failed)
	if len(a.Failed) > 0 && m.Log != nil {
		m.Log.Warn("couldn't send an alert", "alert", a.Title, "failed", strings.Join(a.Failed, "; "))
	} else if m.Log != nil {
		m.Log.Info("alert sent", "alert", a.Title, "channels", a.Sent)
	}
	m.remember(ctx, a)
	return a
}

func (m *Manager) logWarn(what string, err error) {
	if m.Log != nil {
		m.Log.Warn(what, "err", err)
	}
}
