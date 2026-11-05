package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/alerts"
)

// Alerts (Health → Alerts): Box Office emails an admin, or posts to their
// webhooks, when a disk is nearly full, a root folder can't be read, errors
// pile up, downloads keep failing, a backup fails or the server starts
// again (see internal/alerts). Everything goes straight from this server to
// the admin's own mail server and webhooks. Prometheus can read the same
// numbers from GET /metrics.

// alertsView is GET /api/v1/admin/alerts. Passwords, tokens and webhook
// addresses (which work as passwords) never leave the server.
type alertsView struct {
	Email    alertsEmailView     `json:"email"`
	Webhooks []alertsWebhookView `json:"webhooks"`
	Rules    alerts.Rules        `json:"rules"`
	Firing   []alerts.Alert      `json:"firing"`
	History  []alerts.Alert      `json:"history"`
	// Links says alerts link to the Health page (an admin has opened Box
	// Office since it started, so it knows its address).
	Links bool `json:"links"`
	// Forwarded says alerts also go to Matinee, for its admins.
	Forwarded bool `json:"forwarded"`
}

type alertsEmailView struct {
	alerts.Email
	HasPassword bool `json:"hasPassword"`
}

type alertsWebhookView struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	// Hint shows the address's host and its last few characters.
	Hint     string `json:"hint"`
	HasToken bool   `json:"hasToken,omitempty"`
}

func webhookHint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	tail := strings.TrimRight(u.Path, "/")
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return u.Host + "/…" + tail
}

func (s *Server) getAlerts(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	m := s.Alerts()
	c, err := m.Config(ctx)
	if err != nil {
		return err
	}
	v := alertsView{Rules: c.Rules, Webhooks: []alertsWebhookView{}, Firing: m.Firing(), History: m.History(ctx), Links: m.URL != "", Forwarded: m.Forward != nil}
	v.Email.Email, v.Email.HasPassword = c.Email, c.Email.Password != ""
	v.Email.Password = ""
	for _, h := range c.Webhooks {
		v.Webhooks = append(v.Webhooks, alertsWebhookView{ID: h.ID, Kind: h.Kind, Name: h.Name, Hint: webhookHint(h.URL), HasToken: h.Token != ""})
	}
	return writeJSON(w, http.StatusOK, v)
}

func validateAlerts(c alerts.Config) error {
	e := c.Email
	bad := func(msg string) error { return errorf(http.StatusBadRequest, msg) }
	if e.On {
		if e.Host == "" {
			return bad("enter the mail server, such as smtp.gmail.com")
		}
		if e.Port <= 0 || e.Port > 65535 {
			return bad("enter the mail server's port: usually 587, or 465 for TLS")
		}
		switch e.Security {
		case "starttls", "tls", "none":
		default:
			return bad("pick how to connect to the mail server")
		}
		if _, err := mail.ParseAddress(e.From); err != nil {
			return bad("enter the address alerts come from")
		}
		if _, err := mail.ParseAddressList(e.To); err != nil || e.To == "" {
			return bad("enter the addresses alerts go to, separated by commas")
		}
	}
	if len(c.Webhooks) > 20 {
		return bad("that's too many webhooks")
	}
	for _, h := range c.Webhooks {
		switch h.Kind {
		case alerts.KindDiscord, alerts.KindNtfy, alerts.KindHomeAssistant, alerts.KindJSON:
		default:
			return bad("unknown kind of webhook")
		}
		if alerts.CheckWebhook(h) != nil {
			return bad("enter the webhook's full address, starting with https:// or http://")
		}
	}
	return nil
}

// putAlerts saves the alert settings. A webhook that keeps its id keeps
// its address and token when they're left empty, and the email keeps its
// password while the server and username stay the same.
func (s *Server) putAlerts(w http.ResponseWriter, r *http.Request) error {
	var in alerts.Config
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	m := s.Alerts()
	old, err := m.Config(ctx)
	if err != nil {
		return err
	}
	c := mergeAlerts(old, in)
	if err := validateAlerts(c); err != nil {
		return err
	}
	if err := m.SaveConfig(ctx, c); err != nil {
		return err
	}
	s.Log.Info("alert settings saved", "by", currentUser(r), "email", c.Email.On, "webhooks", len(c.Webhooks))
	return s.getAlerts(w, r)
}

// mergeAlerts fills in what the admin left out to keep: secrets, and the
// ids of new webhooks.
func mergeAlerts(old, c alerts.Config) alerts.Config {
	e := &c.Email
	e.Host, e.Username, e.From, e.To = strings.TrimSpace(e.Host), strings.TrimSpace(e.Username), strings.TrimSpace(e.From), strings.TrimSpace(e.To)
	if e.Security == "" {
		e.Security = "starttls"
	}
	if e.Password == "" && e.Host == old.Email.Host && e.Username == old.Email.Username {
		e.Password = old.Email.Password
	}
	byID := map[string]alerts.Webhook{}
	for _, h := range old.Webhooks {
		byID[h.ID] = h
	}
	for i := range c.Webhooks {
		h := &c.Webhooks[i]
		h.URL, h.Name, h.Token = strings.TrimSpace(h.URL), strings.TrimSpace(h.Name), strings.TrimSpace(h.Token)
		if was, ok := byID[h.ID]; ok && h.ID != "" {
			if h.URL == "" {
				h.URL = was.URL
			}
			if h.Token == "" && h.URL == was.URL {
				h.Token = was.Token
			}
			continue
		}
		b := make([]byte, 6)
		rand.Read(b)
		h.ID = hex.EncodeToString(b)
	}
	if c.Webhooks == nil {
		c.Webhooks = []alerts.Webhook{}
	}
	return c
}

// testAlerts sends a test alert everywhere alerts go: to the settings in
// the request, if any (so an admin can try them before saving), or else the
// saved ones.
func (s *Server) testAlerts(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	m := s.Alerts()
	c, err := m.Config(ctx)
	if err != nil {
		return err
	}
	if r.ContentLength != 0 {
		var in alerts.Config
		if err := readJSON(r, &in); err != nil {
			return err
		}
		c = mergeAlerts(c, in)
		if err := validateAlerts(c); err != nil {
			return err
		}
	}
	if c.Channels() == 0 && m.Forward == nil {
		return errorf(http.StatusConflict, "set up email or a webhook first")
	}
	return writeJSON(w, http.StatusOK, m.Test(ctx, c))
}

// ---- Prometheus ----

const settingMetricsToken = "metrics_token"

type metricsView struct {
	On    bool   `json:"on"`
	Token string `json:"token,omitempty"`
	Path  string `json:"path"`
}

func (s *Server) getMetricsSettings(w http.ResponseWriter, r *http.Request) error {
	tok, err := s.Store.Setting(r.Context(), settingMetricsToken)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, metricsView{On: tok != "", Token: tok, Path: "/metrics"})
}

// newMetricsToken turns metrics on with a new token, or changes the token.
func (s *Server) newMetricsToken(w http.ResponseWriter, r *http.Request) error {
	b := make([]byte, 24)
	rand.Read(b)
	if err := s.Store.SetSetting(r.Context(), settingMetricsToken, hex.EncodeToString(b)); err != nil {
		return err
	}
	return s.getMetricsSettings(w, r)
}

func (s *Server) deleteMetricsToken(w http.ResponseWriter, r *http.Request) error {
	if err := s.Store.SetSetting(r.Context(), settingMetricsToken, ""); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// metrics answers Prometheus in its text format, with the token from
// Health → Prometheus as a bearer token.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	tok, err := s.Store.Setting(r.Context(), settingMetricsToken)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if tok == "" {
		http.Error(w, "metrics are off; turn them on in Box Office under Health → Prometheus", http.StatusNotFound)
		return
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(tok)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="boxoffice"`)
		http.Error(w, "send the metrics token as a bearer token", http.StatusUnauthorized)
		return
	}

	var b metricsWriter
	m := s.mon()
	sys := m.Monitor.System()
	b.gauge("boxoffice_build_info", "The server's version.", 1, "version", s.Version, "goversion", runtime.Version(), "database", s.Database)
	b.gauge("boxoffice_start_time_seconds", "When the server started, in seconds since 1970.", float64(sys.Started)/1000)
	b.gauge("boxoffice_cpu_percent", "How much of the machine's processors (or the container's share) Box Office uses, 0 to 100.", sys.CPU)
	if sys.HostCPU != nil {
		b.gauge("boxoffice_host_cpu_percent", "How much of the whole machine's processors are in use, 0 to 100.", *sys.HostCPU)
	}
	b.gauge("boxoffice_memory_bytes", "Memory Box Office uses.", float64(sys.Memory))
	if sys.MemLimit > 0 {
		b.gauge("boxoffice_memory_limit_bytes", "The container's memory limit.", float64(sys.MemLimit))
	}
	b.gauge("boxoffice_goroutines", "Goroutines in the server process.", float64(sys.Goroutines))
	b.counter("boxoffice_sent_bytes_total", "Bytes sent since the server started.", float64(sys.BytesOut))
	b.counter("boxoffice_http_requests_total", "Requests answered since the server started.", float64(sys.Requests))
	b.counter("boxoffice_http_server_errors_total", "Requests that failed on the server since it started.", float64(sys.ServerErrors))
	b.counter("boxoffice_log_warnings_total", "Warnings logged since the server started.", float64(sys.Warnings))
	b.counter("boxoffice_log_errors_total", "Errors logged since the server started.", float64(sys.Errors))

	if s.Downloads != nil {
		l := s.Downloads.Live()
		b.gauge("boxoffice_downloads_active", "Downloads fetching or being unpacked now.", float64(len(l.Progress)))
		b.gauge("boxoffice_download_bytes_per_second", "How fast downloads fetch together now.", float64(l.Speed))
	}
	b.counter("boxoffice_downloads_failed_total", "Downloads that failed since the server started.", float64(m.DownloadFailures.Load()))
	if q, err := s.Store.DownloadQueue(r.Context()); err == nil {
		b.gauge("boxoffice_download_queue", "Downloads waiting or in progress.", float64(len(q)))
	}

	disks := s.disks(s.storageUses(r.Context()))
	b.help("boxoffice_disk_total_bytes", "gauge", "Size of each disk Box Office uses, by the first folder on it.")
	for _, d := range disks {
		if d.Reachable {
			b.sample("boxoffice_disk_total_bytes", float64(d.Total), "path", d.Uses[0].Path, "use", diskUses(d))
		}
	}
	b.help("boxoffice_disk_free_bytes", "gauge", "Free space on each disk Box Office uses.")
	for _, d := range disks {
		if d.Reachable {
			b.sample("boxoffice_disk_free_bytes", float64(d.Free), "path", d.Uses[0].Path, "use", diskUses(d))
		}
	}
	b.help("boxoffice_disk_reachable", "gauge", "1 when the folder can be read, 0 when it can't (such as a network share that dropped).")
	for _, d := range disks {
		v := 0.0
		if d.Reachable {
			v = 1
		}
		b.sample("boxoffice_disk_reachable", v, "path", d.Uses[0].Path, "use", diskUses(d))
	}

	counts := map[string]int{}
	for _, a := range m.Alerts.Firing() {
		counts[a.Rule]++
	}
	b.help("boxoffice_alerts_firing", "gauge", "Alerts that are true now, by rule.")
	for _, rule := range []string{alerts.RuleDisk, alerts.RuleFolder, alerts.RuleErrors, alerts.RuleDownloads} {
		b.sample("boxoffice_alerts_firing", float64(counts[rule]), "rule", rule)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(b.String()))
}

// diskUses names what's on a disk, such as "data,downloads".
func diskUses(d monitorDisk) string {
	var uses []string
	for _, u := range d.Uses {
		if !slices.Contains(uses, u.Use) {
			uses = append(uses, u.Use)
		}
	}
	slices.Sort(uses)
	return strings.Join(uses, ",")
}

// metricsWriter writes Prometheus's text format.
type metricsWriter struct {
	strings.Builder
}

func (b *metricsWriter) help(name, kind, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func (b *metricsWriter) sample(name string, v float64, labels ...string) {
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(labels[i] + `="` + labelValue(labels[i+1]) + `"`)
		}
		b.WriteByte('}')
	}
	b.WriteString(" " + strconv.FormatFloat(v, 'f', -1, 64) + "\n")
}

func (b *metricsWriter) gauge(name, help string, v float64, labels ...string) {
	b.help(name, "gauge", help)
	b.sample(name, v, labels...)
}

func (b *metricsWriter) counter(name, help string, v float64) {
	b.help(name, "counter", help)
	b.sample(name, v)
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func labelValue(s string) string { return labelEscaper.Replace(s) }
