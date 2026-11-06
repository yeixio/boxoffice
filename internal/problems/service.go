package problems

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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

	mu      sync.Mutex
	status  Status
	report  *Report
	queue   []job
	working bool
}

// Status is where the look and the fixes stand.
type Status struct {
	Looking  bool       `json:"looking"`
	Done     int        `json:"done"`
	Total    int        `json:"total"`
	LookedAt *time.Time `json:"lookedAt,omitempty"`
	Error    string     `json:"error,omitempty"`
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
	s.mu.Lock()
	if s.status.Looking {
		s.mu.Unlock()
		return false
	}
	s.status.Looking, s.status.Done, s.status.Total, s.status.Error = true, 0, 0, ""
	s.mu.Unlock()
	go s.look(context.WithoutCancel(ctx))
	return true
}

func (s *Service) look(ctx context.Context) {
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
