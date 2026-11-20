package api

import (
	"errors"
	"net/http"

	"github.com/yeixio/boxoffice/internal/relocate"
)

// Moving titles to another root folder: a whole library, or titles picked
// on Movies or Shows, to a new drive, say. See package relocate.
func (s *Server) moveRoutes() []route {
	return []route{
		{"GET /api/v1/moves", true, s.moveStatus},
		{"POST /api/v1/moves/plan", true, s.movePlan},
		{"POST /api/v1/moves", true, s.startMove},
		{"POST /api/v1/moves/pause", true, s.moveControl(func(m *relocate.Service, r *http.Request) error { return m.Pause(r.Context()) })},
		{"POST /api/v1/moves/resume", true, s.moveControl(func(m *relocate.Service, r *http.Request) error { return m.Resume(r.Context()) })},
		{"POST /api/v1/moves/cancel", true, s.moveControl(func(m *relocate.Service, r *http.Request) error { return m.Cancel(r.Context()) })},
		{"POST /api/v1/moves/retry", true, s.moveControl(func(m *relocate.Service, r *http.Request) error { return m.Retry(r.Context()) })},
		{"DELETE /api/v1/moves/finished", true, s.moveControl(func(m *relocate.Service, r *http.Request) error { return m.Clear(r.Context()) })},
		{"PUT /api/v1/moves/speed", true, s.moveSpeed},
	}
}

// moves is the mover; one that only plans and answers when none was set.
func (s *Server) moves() *relocate.Service {
	s.movesOnce.Do(func() {
		if s.Moves == nil {
			s.Moves = &relocate.Service{Store: s.Store, Log: s.Log}
		}
	})
	return s.Moves
}

func moveError(err error) error {
	var bad relocate.ErrBadPick
	switch {
	case errors.As(err, &bad):
		return errorf(http.StatusBadRequest, bad.Why)
	case errors.Is(err, relocate.ErrChanged):
		return errorf(http.StatusConflict, err.Error())
	}
	return err
}

func (s *Server) moveStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.moves().Status(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) movePlan(w http.ResponseWriter, r *http.Request) error {
	var in relocate.Pick
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if len(in.IDs) > maxBulk {
		return errorf(http.StatusBadRequest, "that's too many at once")
	}
	p, err := s.moves().Plan(r.Context(), in)
	if err != nil {
		return moveError(err)
	}
	return writeJSON(w, http.StatusOK, p)
}

// startMove starts moving what a plan showed: expectCount titles taking
// expectSize bytes. If that's changed, nothing starts (409).
func (s *Server) startMove(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		relocate.Pick
		ExpectCount int   `json:"expectCount"`
		ExpectSize  int64 `json:"expectSize"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if len(in.IDs) > maxBulk {
		return errorf(http.StatusBadRequest, "that's too many at once")
	}
	if _, err := s.moves().Start(r.Context(), in.Pick, in.ExpectCount, in.ExpectSize); err != nil {
		return moveError(err)
	}
	st, err := s.moves().Status(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) moveControl(f func(*relocate.Service, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := f(s.moves(), r); err != nil {
			return err
		}
		return s.moveStatus(w, r)
	}
}

func (s *Server) moveSpeed(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		SpeedLimit int64 `json:"speedLimit"` // MB/s; 0 for none
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.SpeedLimit < 0 || in.SpeedLimit > 100000 {
		return errorf(http.StatusBadRequest, "give the speed limit in MB/s, or 0 for none")
	}
	if err := s.moves().SetSpeed(r.Context(), in.SpeedLimit); err != nil {
		return err
	}
	return s.moveStatus(w, r)
}
