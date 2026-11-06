package problems

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Service looks through the library in the background and carries out
// fixes one at a time, since a remux can take minutes and the library
// can be on a slow share.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// Tools finds FFmpeg; FindTools when nil.
	Tools func() (Tools, error)
	// Alert tells the admins what a daily look found that's new: the
	// Problem files alert rule (internal/alerts). Nil sends nothing.
	Alert func(ctx context.Context, title, message string)

	mu      sync.Mutex
	status  Status
	report  *Report
	queue   []job
	working bool
	next    time.Time
}

// Every is how often Box Office looks through the library by itself.
const Every = 24 * time.Hour

// firstWait is how long after starting the first look waits, so it
// doesn't add to the work a start does.
const firstWait = 10 * time.Minute

// Settings: when the last look was, and which problems the admins have
// been told about (or have seen, after a look of their own), so the daily
// look only alerts about new ones, across restarts.
const (
	settingLookedAt = "problems:looked_at"
	settingKnown    = "problems:known"
)

// Status is where the look and the fixes stand.
type Status struct {
	Looking  bool       `json:"looking"`
	Done     int        `json:"done"`
	Total    int        `json:"total"`
	LookedAt *time.Time `json:"lookedAt,omitempty"`
	// Next is when Box Office looks again by itself.
	Next  *time.Time `json:"next,omitempty"`
	Error string     `json:"error,omitempty"`
	// FFmpeg says whether disc images can be remuxed and parts joined.
	FFmpeg bool `json:"ffmpeg"`
	// Fixing is the fix being carried out, Queued the ones waiting.
	Fixing *Fixing   `json:"fixing,omitempty"`
	Queued []string  `json:"queued"`
	Failed []Failure `json:"failed"`
}

// Fixing is the fix being carried out.
type Fixing struct {
	ID       string  `json:"id"`
	Action   string  `json:"action"`
	Progress float64 `json:"progress"`
}

// Failure is a fix that couldn't be carried out.
type Failure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type job struct {
	p    Problem
	keep string
}

func (s *Service) tools() (Tools, error) {
	if s.Tools != nil {
		return s.Tools()
	}
	return FindTools()
}

// State is the status and what the last look found.
func (s *Service) State() (Status, *Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	_, err := s.tools()
	st.FFmpeg = err == nil
	st.Queued = []string{}
	for _, j := range s.queue {
		st.Queued = append(st.Queued, j.p.ID)
	}
	if !s.next.IsZero() {
		n := s.next
		st.Next = &n
	}
	st.Failed = slices.Clone(s.status.Failed)
	if st.Failed == nil {
		st.Failed = []Failure{}
	}
	if st.Fixing != nil {
		f := *st.Fixing
		st.Fixing = &f
	}
	return st, s.report
}

// Look starts a look through the library, unless one is running. It
// reports false when one already is.
func (s *Service) Look(ctx context.Context) bool {
	return s.start(ctx, false)
}

func (s *Service) start(ctx context.Context, daily bool) bool {
	s.mu.Lock()
	if s.status.Looking {
		s.mu.Unlock()
		return false
	}
	s.status.Looking, s.status.Done, s.status.Total, s.status.Error = true, 0, 0, ""
	s.mu.Unlock()
	go s.look(context.WithoutCancel(ctx), daily)
	return true
}

func (s *Service) look(ctx context.Context, daily bool) {
	rep, err := Find(ctx, s.Store, func(done, total int) {
		s.mu.Lock()
		s.status.Done, s.status.Total = done, total
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Looking = false
	if err != nil {
		s.status.Error = err.Error()
		s.Log.Warn("problem files: look failed", "err", err)
		return
	}
	now := time.Now().UTC()
	s.status.LookedAt = &now
	s.next = now.Add(Every)
	news := s.remember(ctx, rep, now)
	if daily && len(news) > 0 && s.Alert != nil {
		title, msg := alertText(news, len(rep.Problems))
		go s.Alert(context.WithoutCancel(ctx), title, msg)
	}
	// Fixes still to come keep their problems listed.
	s.report = &rep
	// Failures for problems no longer found are old news.
	s.status.Failed = slices.DeleteFunc(s.status.Failed, func(f Failure) bool {
		return !slices.ContainsFunc(rep.Problems, func(p Problem) bool { return p.ID == f.ID })
	})
}

// ErrNotFound means there's no such problem in the last look.
var ErrNotFound = errors.New("there's no such problem now; look again")

// Fix queues fixes for problems from the last look. keep says, for
// problems with several copies, which to keep.
func (s *Service) Fix(ids []string, keep map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.report == nil {
		return ErrNotFound
	}
	var jobs []job
	for _, id := range ids {
		i := slices.IndexFunc(s.report.Problems, func(p Problem) bool { return p.ID == id })
		if i < 0 {
			return ErrNotFound
		}
		p := s.report.Problems[i]
		if p.Fix == nil {
			continue
		}
		if slices.ContainsFunc(s.queue, func(j job) bool { return j.p.ID == id }) || s.status.Fixing != nil && s.status.Fixing.ID == id {
			continue
		}
		jobs = append(jobs, job{p, keep[id]})
	}
	s.queue = append(s.queue, jobs...)
	s.status.Failed = slices.DeleteFunc(s.status.Failed, func(f Failure) bool { return slices.Contains(ids, f.ID) })
	if !s.working && len(s.queue) > 0 {
		s.working = true
		go s.work()
	}
	return nil
}

func (s *Service) work() {
	ctx := context.Background()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.working = false
			s.status.Fixing = nil
			s.mu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.status.Fixing = &Fixing{ID: j.p.ID, Action: j.p.Fix.Action}
		s.mu.Unlock()

		fix, err := Apply(ctx, s.Store, s.tools, j.p, j.keep, func(f float64) {
			s.mu.Lock()
			if s.status.Fixing != nil {
				s.status.Fixing.Progress = f
			}
			s.mu.Unlock()
		})

		s.mu.Lock()
		if err != nil {
			s.Log.Warn("problem files: fix failed", "problem", j.p.Kind, "action", j.p.Fix.Action, "err", err)
			s.status.Failed = append(s.status.Failed, Failure{ID: j.p.ID, Error: err.Error()})
		} else {
			s.Log.Info("problem files: fixed", "problem", j.p.Kind, "action", j.p.Fix.Action, "fix", fix.ID, "title", fix.Title)
			if s.report != nil {
				s.report.Problems = slices.DeleteFunc(s.report.Problems, func(p Problem) bool { return p.ID == j.p.ID })
			}
		}
		s.mu.Unlock()
	}
}

// remember saves when the look was and which problems it found, and
// returns the ones no earlier look found.
func (s *Service) remember(ctx context.Context, rep Report, at time.Time) []Problem {
	var known []string
	if v, _ := s.Store.Setting(ctx, settingKnown); v != "" {
		json.Unmarshal([]byte(v), &known)
	}
	var news []Problem
	ids := make([]string, 0, len(rep.Problems))
	for _, p := range rep.Problems {
		ids = append(ids, p.ID)
		if !slices.Contains(known, p.ID) {
			news = append(news, p)
		}
	}
	b, _ := json.Marshal(ids)
	if err := s.Store.SetSetting(ctx, settingKnown, string(b)); err != nil {
		s.Log.Warn("problem files: couldn't save what the look found", "err", err)
	}
	if err := s.Store.SetSetting(ctx, settingLookedAt, at.Format(time.RFC3339)); err != nil {
		s.Log.Warn("problem files: couldn't save when it looked", "err", err)
	}
	return news
}

// kindNames are the problem kinds as the alert says them.
var kindNames = map[string][2]string{
	Duplicate: {"title with more than one copy", "titles with more than one copy"},
	Parts:     {"movie in parts", "movies in parts"},
	DiscImage: {"disc image", "disc images"},
	Untracked: {"copy Box Office doesn't know", "copies Box Office doesn't know"},
	Missing:   {"missing file", "missing files"},
	Naming:    {"name or folder to tidy", "names or folders to tidy"},
	Unknown:   {"video that isn't an episode", "videos that aren't an episode"},
	NotListed: {"folder or video not on the list", "folders or videos not on the list"},
	Leftover:  {"leftover", "leftovers"},
	EmptyDir:  {"empty folder", "empty folders"},
}

// alertText says what's new, by kind, in the order the page lists them.
func alertText(news []Problem, all int) (string, string) {
	count := map[string]int{}
	var order []string
	for _, p := range news {
		if count[p.Kind] == 0 {
			order = append(order, p.Kind)
		}
		count[p.Kind]++
	}
	var parts []string
	for _, k := range order {
		n := count[k]
		name, ok := kindNames[k]
		if !ok {
			name = [2]string{k, k}
		}
		if n == 1 {
			parts = append(parts, "1 "+name[0])
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", n, name[1]))
		}
	}
	title := "1 new problem file"
	if len(news) != 1 {
		title = fmt.Sprintf("%d new problem files", len(news))
	}
	msg := "Box Office's daily look through the library found " + strings.Join(parts, ", ") + "."
	if all > len(news) {
		msg += fmt.Sprintf(" %d in all.", all)
	}
	msg += " Nothing changes until you pick a fix under Settings → Problem files."
	return title, msg
}

// Run looks through the library every day, the first time a while after
// starting (or a day after the last look, if that's later), until ctx
// ends. A look of the admin's own puts the next one a day after it.
func (s *Service) Run(ctx context.Context) {
	next := time.Now().Add(firstWait)
	if v, _ := s.Store.Setting(ctx, settingLookedAt); v != "" {
		if at, err := time.Parse(time.RFC3339, v); err == nil && at.Add(Every).After(next) {
			next = at.Add(Every)
		}
	}
	s.mu.Lock()
	s.next = next
	s.mu.Unlock()
	t := time.NewTimer(time.Until(next))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		due := !s.next.After(time.Now())
		if due {
			s.next = time.Now().Add(Every)
		}
		wait := time.Until(s.next)
		s.mu.Unlock()
		if due {
			s.start(ctx, true)
		}
		t.Reset(wait)
	}
}
