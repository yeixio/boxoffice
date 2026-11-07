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
	// Deleted, when set, is told what a batch deleted for good, for
	// Activity.
	Deleted func(ctx context.Context, fixes int, freed int64)
	// Alert tells the admins what a daily look found that's new: the
	// Problem files alert rule (internal/alerts). Nil sends nothing.
	Alert func(ctx context.Context, title, message string)

	mu      sync.Mutex
	status  Status
	report  *Report
	queue   []job
	working bool
	next    time.Time
	// loadOnce brings back the last look's report after a restart.
	loadOnce sync.Once
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
	// settingReport keeps what the last look found, so the page has it
	// straight away after a restart, without looking again.
	settingReport = "problems:report"
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
	// Deciding is a batch of set-aside originals being put back or
	// deleted for good; Undecided the ones in the last batch that couldn't.
	Deciding  *Deciding   `json:"deciding,omitempty"`
	Undecided []Undecided `json:"undecided"`
}

// Decisions on the originals fixes set aside.
const (
	PutBackAll = "put_back"
	DeleteAll  = "delete"
)

// Deciding is a batch of fixes being put back or deleted for good.
type Deciding struct {
	Action string `json:"action"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	// Freed is what the deleting has freed so far.
	Freed int64 `json:"freed"`
}

// Undecided is a fix a batch couldn't put back or delete.
type Undecided struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Error string `json:"error"`
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

// Instead says, for parts that look like copies, to keep one instead of
// joining them.
const Instead = ActKeepOne

func (s *Service) tools() (Tools, error) {
	if s.Tools != nil {
		return s.Tools()
	}
	return FindTools()
}

// State is the status and what the last look found.
func (s *Service) State() (Status, *Report) {
	s.load()
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
	st.Undecided = slices.Clone(s.status.Undecided)
	if st.Undecided == nil {
		st.Undecided = []Undecided{}
	}
	if st.Deciding != nil {
		d := *st.Deciding
		st.Deciding = &d
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

// load brings back, once, what the last look before a restart found and
// when, unless a look since has found something newer.
func (s *Service) load() {
	s.loadOnce.Do(func() {
		ctx := context.Background()
		v, _ := s.Store.Setting(ctx, settingReport)
		at, _ := s.Store.Setting(ctx, settingLookedAt)
		var rep Report
		if v == "" || json.Unmarshal([]byte(v), &rep) != nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.report != nil {
			return
		}
		if rep.Roots == nil {
			rep.Roots = []Root{}
		}
		if rep.Problems == nil {
			rep.Problems = []Problem{}
		}
		s.report = &rep
		if t, err := time.Parse(time.RFC3339, at); err == nil && s.status.LookedAt == nil {
			s.status.LookedAt = &t
		}
	})
}

// save keeps what a look found, for after a restart.
func (s *Service) save(ctx context.Context, rep Report) {
	b, err := json.Marshal(rep)
	if err == nil {
		err = s.Store.SetSetting(ctx, settingReport, string(b))
	}
	if err != nil {
		s.Log.Warn("problem files: couldn't save what the look found", "err", err)
	}
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
	if err == nil {
		s.inspect(ctx, &rep)
	}
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
	s.save(ctx, rep)
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
// problems with several copies, which to keep; as, for parts, to keep one
// (Instead) instead of joining them.
func (s *Service) Fix(ids []string, keep, as map[string]string) error {
	s.load()
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
		if a := as[id]; a != "" && a != p.Fix.Action {
			if a != Instead || p.Kind != Parts && p.Kind != Editions {
				return fmt.Errorf("%s isn't a way to fix it", a)
			}
			f := *p.Fix
			f.Action = ActKeepOne
			if f.Keep == "" {
				f.Keep = p.Files[0].Path
			}
			p.Fix = &f
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
	saved := false // a fix took a problem off the report since it was saved
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
				saved = true
			}
		}
		var rep Report
		if saved && len(s.queue) == 0 {
			// What's left is kept once the queue is through.
			rep = Report{Roots: s.report.Roots, Problems: slices.Clone(s.report.Problems)}
		}
		s.mu.Unlock()
		if rep.Roots != nil {
			s.save(ctx, rep)
			saved = false
		}
	}
}

// ErrBusy means a batch of decisions is under way already.
var ErrBusy = errors.New("Box Office is still working through the last batch")

// Decide puts back, or deletes for good, the originals of many fixes, one
// at a time in the background: hundreds of folders on a slow share take
// longer than a request may. Each is checked and done the way a single
// one is (PutBack, DeleteForGood); one that can't be is listed in
// Undecided, and the rest carry on.
func (s *Service) Decide(ids []int64, action string) error {
	if action != PutBackAll && action != DeleteAll {
		return fmt.Errorf("%s isn't a way to decide", action)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Deciding != nil {
		return ErrBusy
	}
	s.status.Deciding = &Deciding{Action: action, Total: len(ids)}
	s.status.Undecided = nil
	go s.decide(slices.Clone(ids), action)
	return nil
}

func (s *Service) decide(ids []int64, action string) {
	ctx := context.Background()
	var freed int64
	deleted := 0
	for _, id := range ids {
		var err error
		if action == DeleteAll {
			var n int64
			n, err = DeleteForGood(ctx, s.Store, id)
			freed += n
			if err == nil {
				deleted++
			}
		} else {
			err = PutBack(ctx, s.Store, id)
		}
		s.mu.Lock()
		s.status.Deciding.Done++
		s.status.Deciding.Freed = freed
		if err != nil {
			title := ""
			if f, ferr := s.Store.ProblemFix(ctx, id); ferr == nil {
				title = Repair(f.Title)
			}
			s.status.Undecided = append(s.status.Undecided, Undecided{ID: id, Title: title, Error: err.Error()})
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	failed := len(s.status.Undecided)
	s.status.Deciding = nil
	s.mu.Unlock()
	s.Log.Info("problem files: decided", "action", action, "fixes", len(ids), "failed", failed, "freed", freed)
	if deleted > 0 && s.Deleted != nil {
		s.Deleted(ctx, deleted, freed)
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
