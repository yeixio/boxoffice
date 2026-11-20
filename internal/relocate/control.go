package relocate

import (
	"context"
	"errors"
	"strconv"

	"github.com/yeixio/boxoffice/internal/store"
)

// ErrChanged means what's on disk changed since the plan was shown.
var ErrChanged = errors.New("what would be moved changed since you looked; look at it again")

// Start puts the titles of p in line to be moved, as long as the plan is
// still what the person was shown: count titles taking size bytes.
func (s *Service) Start(ctx context.Context, p Pick, count int, size int64) (Plan, error) {
	plan, err := s.Plan(ctx, p)
	if err != nil {
		return plan, err
	}
	if plan.Problem != "" {
		return plan, ErrBadPick{plan.Problem}
	}
	if !plan.Matches(count, size) {
		return plan, ErrChanged
	}
	list := make([]store.TitleMove, 0, len(plan.all))
	for _, t := range plan.all {
		list = append(list, store.TitleMove{Kind: p.Kind, TitleID: t.ID, Title: t.Title, FromRoot: t.Root, FromPath: t.From,
			ToRoot: plan.To, Files: t.Files, Size: t.Size})
	}
	if err := s.Store.AddTitleMoves(ctx, list); err != nil {
		return plan, err
	}
	s.init(ctx)
	s.poke()
	return plan, nil
}

// Status is the line of moves, for the page.
type Status struct {
	Paused bool `json:"paused"`
	// PausedWhy says why Box Office paused it itself, such as the new disk
	// filling up.
	PausedWhy  string `json:"pausedWhy,omitempty"`
	SpeedLimit int64  `json:"speedLimit"` // MB/s; 0 is no limit
	// Current is the title being moved, and Progress how it's getting on.
	Current  *store.TitleMove  `json:"current,omitempty"`
	Progress *Progress         `json:"progress,omitempty"`
	Moves    []store.TitleMove `json:"moves"`
	// Size and Done are bytes: all the moves not skipped or cancelled,
	// and those moved so far (with the current one's copying).
	Size int64 `json:"size"`
	Done int64 `json:"done"`
}

// Status says how the moves are getting on.
func (s *Service) Status(ctx context.Context) (Status, error) {
	s.init(ctx)
	var st Status
	list, err := s.Store.TitleMoves(ctx)
	if err != nil {
		return st, err
	}
	paused, _ := s.Store.Setting(ctx, SettingPaused)
	st.Paused = paused == "1"
	if st.Paused {
		st.PausedWhy, _ = s.Store.Setting(ctx, SettingPausedWhy)
	}
	st.SpeedLimit = s.speed.Load()
	s.mu.Lock()
	if s.current != nil {
		c := *s.current
		pr := s.prog.progress()
		st.Current, st.Progress = &c, &pr
	}
	s.mu.Unlock()
	st.Moves = list
	for _, m := range list {
		switch m.State {
		case store.MoveSkipped, store.MoveCancelled:
			continue
		case store.MoveDone:
			st.Done += m.Size
		}
		st.Size += m.Size
	}
	if st.Current != nil && st.Progress != nil && st.Progress.Stage == "copying" {
		st.Done += st.Progress.Done
	} else if st.Current != nil {
		st.Done += st.Current.Size
	}
	return st, nil
}

// Pause stops moving titles; the one being copied stops now, and starts
// over when moving carries on. Resume carries on.
func (s *Service) Pause(ctx context.Context) error {
	s.init(ctx)
	if err := s.Store.SetSetting(ctx, SettingPaused, "1"); err != nil {
		return err
	}
	s.Store.SetSetting(ctx, SettingPausedWhy, "")
	s.interrupt(errPaused)
	return nil
}

func (s *Service) Resume(ctx context.Context) error {
	s.init(ctx)
	if err := s.Store.SetSetting(ctx, SettingPaused, ""); err != nil {
		return err
	}
	s.Store.SetSetting(ctx, SettingPausedWhy, "")
	s.poke()
	return nil
}

// Cancel cancels the moves not made yet, including the one being copied:
// its copy is cleared away, and the original stays as it was. One already
// copied and checked finishes, since that takes moments.
func (s *Service) Cancel(ctx context.Context) error {
	s.init(ctx)
	if _, err := s.Store.CancelTitleMoves(ctx); err != nil {
		return err
	}
	s.interrupt(errCancelled)
	return nil
}

// Retry puts the moves that failed back in line.
func (s *Service) Retry(ctx context.Context) error {
	s.init(ctx)
	if _, err := s.Store.RetryTitleMoves(ctx); err != nil {
		return err
	}
	s.poke()
	return nil
}

// Clear forgets the finished moves.
func (s *Service) Clear(ctx context.Context) error { return s.Store.ClearTitleMoves(ctx) }

// SetSpeed sets the speed limit, in MB/s; 0 is none. The copy going on
// keeps to it from now.
func (s *Service) SetSpeed(ctx context.Context, mbps int64) error {
	s.init(ctx)
	mbps = max(mbps, 0)
	if err := s.Store.SetSetting(ctx, SettingSpeed, strconv.FormatInt(mbps, 10)); err != nil {
		return err
	}
	s.speed.Store(mbps)
	return nil
}

func (s *Service) interrupt(why error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		s.stop(why)
	}
}
