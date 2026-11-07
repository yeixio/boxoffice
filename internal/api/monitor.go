package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/monitor"
)

// Server monitoring for the Health page: how busy Box Office and its
// machine are, the disks it uses, what's downloading, the problems it
// logged and the latest log lines, plus switching debug logging on and off.
// Everything is read on this server; nothing is sent anywhere.

// SettingDebugLogging keeps debug logging on across restarts.
const SettingDebugLogging = "debug_logging"

// Monitoring is what the Health page's server section needs. The zero value
// works: it makes a monitor with its own log and a level it alone controls.
type Monitoring struct {
	Monitor *monitor.Monitor
	Alerts  *alerts.Manager
	// Level is the log level, which the Health page switches between Info
	// and Debug; nil means it can't be changed.
	Level *slog.LevelVar
	// ForcedDebug says BOXOFFICE_DEBUG is set, so debug logging stays on.
	ForcedDebug bool
	// DownloadFailures counts downloads that failed since the server started.
	DownloadFailures atomic.Int64

	once     sync.Once
	diskMu   sync.Mutex
	diskBusy map[string]bool
	// url is the address an admin last opened Box Office at, for links in
	// alerts.
	url atomic.Value
}

func (s *Server) mon() *Monitoring {
	if s.Monitoring == nil {
		s.Monitoring = &Monitoring{}
	}
	m := s.Monitoring
	m.once.Do(func() {
		if m.Monitor == nil {
			m.Monitor = monitor.NewMonitor(nil)
		}
		if m.Monitor.Downloads == nil && s.Downloads != nil {
			m.Monitor.Downloads = func() (int, float64) {
				l := s.Downloads.Live()
				return len(l.Progress), float64(l.Speed)
			}
		}
		if m.Alerts == nil {
			m.Alerts = &alerts.Manager{Store: s.Store, Log: s.Log, Version: s.Version}
		}
		if m.Alerts.Read == nil {
			m.Alerts.Read = s.AlertReadings
		}
	})
	return m
}

// Alerts is the alert manager, for main to run.
func (s *Server) Alerts() *alerts.Manager { return s.mon().Alerts }

// RestoreDebugLogging turns debug logging back on at start when an admin
// left it on.
func (s *Server) RestoreDebugLogging(ctx context.Context) {
	m := s.mon()
	if m.Level == nil || m.ForcedDebug {
		return
	}
	if v, _ := s.Store.Setting(ctx, SettingDebugLogging); v != "" {
		m.Level.Set(slog.LevelDebug)
		s.Log.Info("debug logging is on; turn it off on the Health page")
	}
}

// ---- counting requests ----

// countingWriter counts what a response sends, and its status.
type countingWriter struct {
	http.ResponseWriter
	status int
	n      int
}

func (w *countingWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *countingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.n += n
	return n, err
}

// Flush keeps streaming responses streaming.
func (w *countingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *countingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// quietPaths are asked so often (by Kubernetes, Prometheus and the Health
// page itself) that logging each would bury everything else.
var quietPaths = []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/monitor", "/api/v1/admin/logs"}

// counted counts every request for the charts, logs each at debug level
// (and failures always), and notes the address admins use.
func (s *Server) counted(h http.Handler) http.Handler {
	m := s.mon()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		cw := &countingWriter{ResponseWriter: w}
		h.ServeHTTP(cw, r)
		if cw.status == 0 {
			cw.status = http.StatusOK
		}
		m.Monitor.Traffic.Sent(cw.n)
		m.Monitor.Traffic.Answered(cw.status)
		if !strings.HasPrefix(r.URL.Path, "/assets/") && !slices.Contains(quietPaths, r.URL.Path) {
			lv := slog.LevelDebug
			if cw.status >= 500 {
				lv = slog.LevelWarn
			}
			if s.Log.Enabled(r.Context(), lv) {
				// The path only: query strings can carry an API key.
				s.Log.Log(r.Context(), lv, "request", "method", r.Method, "path", r.URL.Path, "status", cw.status,
					"took", time.Since(start).Round(time.Millisecond).String(), "bytes", cw.n, "from", clientName(r))
			}
		}
		if _, err := r.Cookie(cookieName); err == nil && cw.status < 400 && r.Host != "" {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			u := scheme + "://" + r.Host
			if old, _ := m.url.Load().(string); old != u {
				m.url.Store(u)
				m.Alerts.URL = u
			}
		}
	})
}

// clientName says who sent a request, for the log: Matinee, Radarr's
// clients, a browser, or whatever it says it is.
func clientName(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	switch {
	case ua == "":
		return "unknown"
	case strings.Contains(ua, "Mozilla/"):
		return "browser"
	}
	if name, _, ok := strings.Cut(ua, " "); ok {
		ua = name
	}
	if len(ua) > 40 {
		ua = ua[:40]
	}
	return ua
}

// ---- the server section of the Health page ----

type monitorDisk struct {
	monitor.Disk
	// Reachable is false when the folder can't be read, as when a network
	// share has dropped.
	Reachable bool         `json:"reachable"`
	Uses      []monitorUse `json:"uses"`
}

type monitorUse struct {
	Use  string `json:"use"` // data, downloads, backups, movies or tv
	Path string `json:"path"`
}

type monitorReport struct {
	Version  string `json:"version"`
	Database string `json:"database"`
	// DatabaseMs is how long the database took to answer, or -1 when it didn't.
	DatabaseMs    float64 `json:"databaseMs"`
	DatabaseError string  `json:"databaseError,omitempty"`
	// Debug says debug logging is on, and DebugForced that BOXOFFICE_DEBUG
	// keeps it on.
	Debug       bool           `json:"debug"`
	DebugForced bool           `json:"debugForced,omitempty"`
	System      monitor.System `json:"system"`
	Disks       []monitorDisk  `json:"disks"`
	// Downloads counts the queue and history by status.
	Downloads        map[string]int    `json:"downloads"`
	DownloadFailures int64             `json:"downloadFailures"`
	Jobs             monitorJobs       `json:"jobs"`
	Problems         []monitor.Problem `json:"problems"`
	Alerts           []alerts.Alert    `json:"alerts"`
	LogLines         int               `json:"logLines"`
	LogsSince        int64             `json:"logsSince,omitempty"`
}

type monitorJobs struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	// Failed is how many failed in the last day.
	Failed int `json:"failed"`
}

func (s *Server) getMonitor(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	m := s.mon()
	out := monitorReport{
		Version: s.Version, Database: s.Database, System: m.Monitor.System(), DownloadFailures: m.DownloadFailures.Load(),
		Downloads: map[string]int{}, Problems: []monitor.Problem{}, Alerts: m.Alerts.Firing(), DebugForced: m.ForcedDebug,
	}
	out.Debug = s.Log.Enabled(ctx, slog.LevelDebug)
	if l := m.Monitor.Logs; l != nil {
		out.Problems = l.Problems(50)
		out.LogLines, out.LogsSince = l.Kept()
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.Store.Ping(dbCtx); err != nil {
		out.DatabaseMs, out.DatabaseError = -1, err.Error()
	} else {
		out.DatabaseMs = float64(time.Since(start).Microseconds()) / 1000
	}
	if q, err := s.Store.DownloadQueue(dbCtx); err == nil {
		for _, d := range q {
			out.Downloads[d.Status]++
		}
	}
	if h, err := s.Store.DownloadHistory(dbCtx, 1000); err == nil {
		for _, d := range h {
			out.Downloads[d.Status]++
		}
	}
	if jobs, err := s.Store.Jobs(dbCtx, 500); err == nil {
		dayAgo := time.Now().Add(-24 * time.Hour)
		for _, j := range jobs {
			switch {
			case j.Status == "queued":
				out.Jobs.Queued++
			case j.Status == "running":
				out.Jobs.Running++
			case j.Status == "failed" && j.FinishedAt.After(dayAgo):
				out.Jobs.Failed++
			}
		}
	}
	out.Disks = s.disks(s.storageUses(dbCtx))
	return writeJSON(w, http.StatusOK, out)
}

// storageUses lists Box Office's own folders and its root folders.
func (s *Server) storageUses(ctx context.Context) []monitorUse {
	var uses []monitorUse
	if s.DataDir != "" {
		uses = append(uses, monitorUse{Use: "data", Path: s.DataDir})
	}
	if s.Downloads != nil && s.Downloads.Dir != "" {
		uses = append(uses, monitorUse{Use: "downloads", Path: s.Downloads.Dir})
	}
	if s.Backups != nil && s.Backups.Dir != "" {
		uses = append(uses, monitorUse{Use: "backups", Path: s.Backups.Dir})
	}
	roots, _ := s.Store.RootFolders(ctx, "")
	for _, f := range roots {
		use := "movies"
		switch f.Kind {
		case "series":
			use = "tv"
		case "movie":
		default:
			use = strings.ToLower(f.Kind)
			if t, ok := mediatypes.Lookup(f.Kind); ok {
				use = strings.ToLower(t.Name)
			}
		}
		uses = append(uses, monitorUse{Use: use, Path: f.Path})
	}
	return uses
}

// isLibrary says a use is a root folder.
func isLibrary(use string) bool { return use == "movies" || use == "tv" }

// disks looks up the space on each folder's disk and puts folders on the
// same disk together.
func (s *Server) disks(uses []monitorUse) []monitorDisk {
	type result struct {
		d  monitor.Disk
		ok bool
	}
	paths := make([]string, len(uses))
	byPath := map[string]*result{}
	for i, u := range uses {
		paths[i] = u.Path
		if !isLibrary(u.Use) {
			// Box Office makes its own folders when it first needs them.
			paths[i] = existingParent(u.Path)
		}
		byPath[paths[i]] = &result{}
	}
	var wg sync.WaitGroup
	for path, r := range byPath {
		wg.Go(func() { r.d, r.ok = s.diskSpace(path) })
	}
	wg.Wait()
	out := []monitorDisk{}
	for i, u := range uses {
		res := *byPath[paths[i]]
		if res.ok {
			if j := slices.IndexFunc(out, func(d monitorDisk) bool { return d.Reachable && sameDisk(d.Disk, res.d) }); j >= 0 {
				out[j].Uses = append(out[j].Uses, u)
				continue
			}
		}
		out = append(out, monitorDisk{Disk: res.d, Reachable: res.ok, Uses: []monitorUse{u}})
	}
	return out
}

// sameDisk says whether two folders' disk space is one disk's. Network
// shares of one server (Unraid's, say) each read as a disk of their own,
// and as files are written their free space differs from one reading to
// the next, so a little difference is allowed: otherwise they're grouped
// differently each time, and an alert about the disk comes and goes.
func sameDisk(a, b monitor.Disk) bool {
	if a.Total != b.Total {
		return false
	}
	diff := max(a.Free, b.Free) - min(a.Free, b.Free)
	return diff <= a.Total/1000
}

// diskWait is how long the Health page waits for a folder's disk. A network
// share that dropped can hang for minutes.
const diskWait = 2 * time.Second

// diskSpace reads a folder's disk space, giving up after diskWait. A folder
// that's still being read from an earlier ask isn't asked again.
func (s *Server) diskSpace(path string) (monitor.Disk, bool) {
	m := s.mon()
	m.diskMu.Lock()
	if m.diskBusy == nil {
		m.diskBusy = map[string]bool{}
	}
	if m.diskBusy[path] {
		m.diskMu.Unlock()
		return monitor.Disk{}, false
	}
	m.diskBusy[path] = true
	m.diskMu.Unlock()
	done := make(chan struct{})
	var d monitor.Disk
	var ok bool
	go func() {
		d, ok = monitor.DiskUsage(path)
		m.diskMu.Lock()
		delete(m.diskBusy, path)
		m.diskMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return d, ok
	case <-time.After(diskWait):
		return monitor.Disk{}, false
	}
}

// existingParent is path, or the nearest folder above it that exists.
func existingParent(path string) string {
	for p := path; ; {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return path
		}
		p = up
	}
}

// ---- logs ----

func logQuery(r *http.Request) monitor.Query {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit > 1000 {
		limit = 1000
	}
	return monitor.Query{Level: q.Get("level"), Text: q.Get("q"), Before: before, Limit: limit}
}

// getLogs lists the latest log lines, newest first.
func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) error {
	l := s.mon().Monitor.Logs
	if l == nil {
		return writeJSON(w, http.StatusOK, map[string]any{"lines": []monitor.Entry{}, "more": false})
	}
	lines, more := l.Lines(logQuery(r))
	return writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "more": more})
}

// downloadLogs sends the matching log lines as a text file, oldest first,
// to attach to a bug report.
func (s *Server) downloadLogs(w http.ResponseWriter, r *http.Request) error {
	q := logQuery(r)
	q.Limit = monitor.DefaultLines * 4
	var lines []monitor.Entry
	if l := s.mon().Monitor.Logs; l != nil {
		lines, _ = l.Lines(q)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="boxoffice-%s.log"`, time.Now().Format("2006-01-02-150405")))
	fmt.Fprintf(w, "# Box Office %s, %s, %s/%s, %s database\n", s.Version, time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH, s.Database)
	for i := len(lines) - 1; i >= 0; i-- {
		e := lines[i]
		var b strings.Builder
		b.WriteString(time.UnixMilli(e.Time).Format("2006-01-02T15:04:05.000Z07:00"))
		b.WriteString(" " + strings.ToUpper(e.Level) + " " + e.Message)
		for _, a := range e.Attrs {
			v := a.Value
			if strings.ContainsAny(v, " \t\n\"=") {
				v = strconv.Quote(v)
			}
			b.WriteString(" " + a.Key + "=" + v)
		}
		b.WriteString("\n")
		w.Write([]byte(b.String()))
	}
	return nil
}

// putDebug switches debug logging on or off, and remembers it.
func (s *Server) putDebug(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	m := s.mon()
	if m.Level == nil {
		return errorf(http.StatusConflict, "this server's log level can't be changed while it runs")
	}
	if m.ForcedDebug && !in.On {
		return errorf(http.StatusConflict, "BOXOFFICE_DEBUG is set, so debug logging stays on. Remove it from the deployment (in Kubernetes, the env list in the Helm values) and restart Box Office to turn it off.")
	}
	v := ""
	if in.On {
		v = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.Store.SetSetting(r.Context(), SettingDebugLogging, v); err != nil {
		return err
	}
	if in.On {
		m.Level.Set(slog.LevelDebug)
		s.Log.Info("debug logging turned on", "by", currentUser(r))
	} else {
		s.Log.Info("debug logging turned off", "by", currentUser(r))
		m.Level.Set(slog.LevelInfo)
	}
	return writeJSON(w, http.StatusOK, map[string]bool{"on": in.On})
}

// currentUser names who's asking, for the log.
func currentUser(r *http.Request) string { return user(r).Username }

// AlertReadings is the server now, for the alert rules.
func (s *Server) AlertReadings(ctx context.Context) alerts.Readings {
	m := s.mon()
	var r alerts.Readings
	if l := m.Monitor.Logs; l != nil {
		_, r.Errors = l.Counts()
		r.Problems = l.Problems(50)
	}
	r.DownloadFailures = m.DownloadFailures.Load()
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, d := range s.disks(s.storageUses(dbCtx)) {
		disk := alerts.Disk{Total: d.Total, Free: d.Free, Reachable: d.Reachable}
		var names []string
		for _, u := range d.Uses {
			name := u.Use
			if isLibrary(u.Use) {
				disk.Library = true
				name = map[string]string{"movies": "movies", "tv": "TV"}[u.Use]
			}
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		disk.Name, disk.Path = strings.Join(names, ", "), d.Uses[0].Path
		r.Disks = append(r.Disks, disk)
	}
	return r
}
