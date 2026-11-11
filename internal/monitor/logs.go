// Package monitor keeps what the Health page shows about the server
// itself: the latest log lines, the problems among them, how busy the
// machine is, and how much the server sends. Nothing here leaves the server.
package monitor

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is one log line.
type Entry struct {
	Seq     int64  `json:"seq"`
	Time    int64  `json:"time"` // milliseconds
	Level   string `json:"level"`
	Message string `json:"message"`
	Attrs   []Attr `json:"attrs,omitempty"`
}

// Attr is one of a log line's details, such as err=... or item=12.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Problem is a warning or error, with how often it happened while the
// server kept it in memory.
type Problem struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	// Detail is the error, when the line has one, so different failures
	// of the same kind are told apart.
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count"`
	First  int64  `json:"first"` // milliseconds
	Last   int64  `json:"last"`
	// Attrs are the latest occurrence's details.
	Attrs []Attr `json:"attrs,omitempty"`

	seq int64 // the latest occurrence's line
}

const (
	// DefaultLines is how many log lines the server keeps in memory.
	DefaultLines = 5000
	// maxProblems is how many different problems it keeps.
	maxProblems = 300
	// maxValue cuts very long values, such as a program's output.
	maxValue = 2000
)

// Logs keeps the latest log lines in memory. The zero value is not usable;
// call NewLogs.
type Logs struct {
	mu       sync.Mutex
	lines    []Entry // a ring of cap(lines) lines
	next     int     // where the next line goes
	full     bool
	seq      int64
	problems map[string]*Problem
	warnings int64 // since the server started
	errors   int64
	now      func() time.Time
	// journal, when set, writes every line to a file too (OpenJournal).
	journal *Journal
}

// NewLogs keeps up to n lines.
func NewLogs(n int) *Logs {
	if n <= 0 {
		n = DefaultLines
	}
	return &Logs{lines: make([]Entry, n), problems: map[string]*Problem{}, now: time.Now}
}

// Handler logs to inner and keeps a copy of every line at Info and above
// (and Debug when inner logs Debug).
func (l *Logs) Handler(inner slog.Handler) slog.Handler {
	return &teeHandler{inner: inner, logs: l}
}

func levelName(lv slog.Level) string {
	switch {
	case lv >= slog.LevelError:
		return "error"
	case lv >= slog.LevelWarn:
		return "warn"
	case lv >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

// LevelRank orders level names: debug 0, info 1, warn 2, error 3; -1 for
// anything else.
func LevelRank(name string) int {
	switch name {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn":
		return 2
	case "error":
		return 3
	}
	return -1
}

func (l *Logs) add(t time.Time, lv slog.Level, msg string, attrs []Attr) {
	l.mu.Lock()
	l.seq++
	e := Entry{Seq: l.seq, Time: t.UnixMilli(), Level: levelName(lv), Message: msg, Attrs: attrs}
	l.keep(e)
	j := l.journal
	l.mu.Unlock()
	j.write(e)
}

// addEntry keeps a line read back from a journal. It's numbered again:
// lines logged at the same moment can reach the file out of order.
func (l *Logs) addEntry(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq = l.seq
	l.keep(e)
}

// keep puts a line in the ring and counts it if it's a problem. l.mu is held.
func (l *Logs) keep(e Entry) {
	l.lines[l.next] = e
	l.next++
	if l.next == len(l.lines) {
		l.next, l.full = 0, true
	}
	rank := LevelRank(e.Level)
	if rank < 2 {
		return
	}
	attrs, msg := e.Attrs, e.Message
	if rank >= 3 {
		l.errors++
	} else {
		l.warnings++
	}
	detail := ""
	for _, a := range attrs {
		if a.Key == "err" || a.Key == "error" {
			detail = a.Value
			if len(detail) > 200 {
				detail = detail[:200]
			}
			break
		}
	}
	key := e.Level + "\x00" + msg + "\x00" + detail
	p := l.problems[key]
	if p == nil {
		if len(l.problems) >= maxProblems {
			l.forgetOldestProblem()
		}
		p = &Problem{Level: e.Level, Message: msg, Detail: detail, First: e.Time}
		l.problems[key] = p
	}
	p.Count++
	p.Last, p.seq = e.Time, e.Seq
	p.Attrs = attrs
}

func (l *Logs) forgetOldestProblem() {
	var oldest string
	var at int64
	for k, p := range l.problems {
		if oldest == "" || p.seq < at {
			oldest, at = k, p.seq
		}
	}
	delete(l.problems, oldest)
}

// Query picks lines to show.
type Query struct {
	// Level is the least serious level to show: debug, info, warn or error.
	Level string
	// Text, when set, is a word or phrase the line has, in any case.
	Text string
	// Before shows only lines older than this one (its Seq), for paging.
	Before int64
	// Limit is the most lines to show; 0 means 200.
	Limit int
}

// Lines returns matching lines, newest first, and whether older ones match too.
func (l *Logs) Lines(q Query) ([]Entry, bool) {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	min := LevelRank(q.Level)
	text := strings.ToLower(strings.TrimSpace(q.Text))
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	n := l.next
	if l.full {
		n = len(l.lines)
	}
	for i := 0; i < n; i++ {
		idx := (l.next - 1 - i + len(l.lines)) % len(l.lines)
		e := l.lines[idx]
		if q.Before > 0 && e.Seq >= q.Before {
			continue
		}
		if LevelRank(e.Level) < min || text != "" && !matches(e, text) {
			continue
		}
		if len(out) == limit {
			return out, true
		}
		out = append(out, e)
	}
	return out, false
}

func matches(e Entry, text string) bool {
	if strings.Contains(strings.ToLower(e.Message), text) {
		return true
	}
	for _, a := range e.Attrs {
		if strings.Contains(strings.ToLower(a.Key+"="+a.Value), text) {
			return true
		}
	}
	return false
}

// Problems returns warnings and errors, latest first, at most limit.
func (l *Logs) Problems(limit int) []Problem {
	l.mu.Lock()
	out := make([]Problem, 0, len(l.problems))
	for _, p := range l.problems {
		out = append(out, *p)
	}
	l.mu.Unlock()
	sortProblems(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Counts is how many warnings and errors there have been since the server started.
func (l *Logs) Counts() (warnings, errors int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.warnings, l.errors
}

// Kept is how many lines are in memory, and the oldest one's time (ms).
func (l *Logs) Kept() (n int, oldest int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full {
		return len(l.lines), l.lines[l.next].Time
	}
	if l.next == 0 {
		return 0, 0
	}
	return l.next, l.lines[0].Time
}

// CloseJournal stops writing lines to the journal.
func (l *Logs) CloseJournal() error {
	l.mu.Lock()
	j := l.journal
	l.journal = nil
	l.mu.Unlock()
	return j.Close()
}

// teeHandler passes lines on to the real log and keeps a copy.
type teeHandler struct {
	inner  slog.Handler
	logs   *Logs
	attrs  []Attr // from WithAttrs
	prefix string // groups from WithGroup, as "a.b."
}

func (h *teeHandler) Enabled(ctx context.Context, lv slog.Level) bool {
	return lv >= slog.LevelInfo || h.inner.Enabled(ctx, lv)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.inner.Enabled(ctx, r.Level) {
		err = h.inner.Handle(ctx, r)
	}
	attrs := append([]Attr{}, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = appendAttr(attrs, h.prefix, a)
		return true
	})
	t := r.Time
	if t.IsZero() {
		t = h.logs.now()
	}
	h.logs.add(t, r.Level, r.Message, attrs)
	return err
}

func (h *teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	attrs := append([]Attr{}, h.attrs...)
	for _, a := range as {
		attrs = appendAttr(attrs, h.prefix, a)
	}
	return &teeHandler{inner: h.inner.WithAttrs(as), logs: h.logs, attrs: attrs, prefix: h.prefix}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &teeHandler{inner: h.inner.WithGroup(name), logs: h.logs, attrs: h.attrs, prefix: h.prefix + name + "."}
}

func appendAttr(out []Attr, prefix string, a slog.Attr) []Attr {
	v := a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return out
	}
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range v.Group() {
			out = appendAttr(out, p, g)
		}
		return out
	}
	s := v.String()
	if len(s) > maxValue {
		s = s[:maxValue] + "…"
	}
	return append(out, Attr{Key: prefix + a.Key, Value: strings.TrimRight(s, "\n")})
}
