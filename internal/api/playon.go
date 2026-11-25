package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/yeixio/boxoffice/internal/playon"
	"github.com/yeixio/boxoffice/internal/store"
)

// PlayOn Cloud (Settings → Downloads → PlayOn Cloud): recordings made with
// PlayOn Cloud are downloaded once they're finished and filed in the
// library through Import files (see internal/playon).
func (s *Server) playOnRoutes() []route {
	return []route{
		{"GET /api/v1/playon", true, s.getPlayOn},
		{"PUT /api/v1/playon", true, s.putPlayOn},
		{"POST /api/v1/playon/test", true, s.testPlayOn},
		{"POST /api/v1/playon/check", true, s.checkPlayOn},
		{"POST /api/v1/playon/recordings/{id}/retry", true, s.retryPlayOn},
		{"POST /api/v1/playon/recordings/{id}/skip", true, s.skipPlayOn},
	}
}

// playOn is the PlayOn service: the one main.go made, or one made here
// (it then only answers; nothing runs it).
func (s *Server) playOn() *playon.Service {
	s.playOnOnce.Do(func() {
		if s.PlayOn == nil {
			dir := ""
			if s.Downloads != nil {
				dir = s.Downloads.Dir
			}
			s.PlayOn = &playon.Service{Store: s.Store, Dir: dir, Log: s.Log, UserAgent: "BoxOffice/" + s.Version}
		}
	})
	return s.PlayOn
}

// playOnView is GET /api/v1/playon. The password never leaves the server.
type playOnView struct {
	Config struct {
		On          bool   `json:"on"`
		Email       string `json:"email"`
		HasPassword bool   `json:"hasPassword"`
	} `json:"config"`
	Status playon.Status `json:"status"`
	// Recordings are the latest, newest first.
	Recordings []store.PlayOnRecording `json:"recordings"`
	// Counts is how many recordings are in each state.
	Counts map[string]int `json:"counts"`
	// Expiring is how many not yet downloaded PlayOn deletes within a day.
	Expiring int `json:"expiring"`
}

// maxPlayOnShown is how many recordings the page lists.
const maxPlayOnShown = 100

func (s *Server) getPlayOn(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := playon.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	recs, err := s.Store.PlayOnRecordings(ctx)
	if err != nil {
		return err
	}
	v := playOnView{Status: s.playOn().State(), Counts: map[string]int{}}
	v.Config.On, v.Config.Email, v.Config.HasPassword = c.On, c.Email, c.Password != ""
	soon := time.Now().Add(24 * time.Hour)
	for _, rec := range recs {
		v.Counts[rec.State]++
		if (rec.State == store.PlayOnNew || rec.State == store.PlayOnFailed) && !rec.Expires.IsZero() && rec.Expires.Before(soon) && rec.Expires.After(time.Now()) {
			v.Expiring++
		}
	}
	v.Recordings = recs[:min(len(recs), maxPlayOnShown)]
	return writeJSON(w, http.StatusOK, v)
}

// playOnInput is the settings as the page sends them: an empty password
// keeps the saved one while the email stays the same, and Forget unlinks
// the account and forgets its recordings.
type playOnInput struct {
	playon.Config
	Forget bool `json:"forget"`
}

func (in playOnInput) merge(old playon.Config) playon.Config {
	c := in.Config
	c.Normalize()
	if in.Forget {
		return playon.Config{}
	}
	if c.Password == "" && c.Email == old.Email {
		c.Password = old.Password
	}
	if c.Email == "" {
		c.Password = ""
	}
	return c
}

func (s *Server) putPlayOn(w http.ResponseWriter, r *http.Request) error {
	var in playOnInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	old, err := playon.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	c := in.merge(old)
	if err := c.Check(); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if err := playon.SaveConfig(ctx, s.Store, c); err != nil {
		return err
	}
	if in.Forget || c.Email != old.Email {
		// Another account's recordings aren't this one's.
		if err := s.Store.ForgetPlayOn(ctx); err != nil {
			return err
		}
	}
	s.Log.Info("PlayOn settings saved", "by", currentUser(r), "on", c.On, "forget", in.Forget)
	if c.On {
		s.playOn().Wake()
	}
	return s.getPlayOn(w, r)
}

// testPlayOn signs in to PlayOn with the email and password in the
// request, or the saved ones, and says how many recordings are ready.
func (s *Server) testPlayOn(w http.ResponseWriter, r *http.Request) error {
	var in playOnInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	old, err := playon.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	c := in.merge(old)
	if c.Email == "" || c.Password == "" {
		return errorf(http.StatusBadRequest, "enter your PlayOn email and password first")
	}
	cl := s.playOn().Client(c)
	err = cl.Check(ctx)
	switch {
	case errors.Is(err, playon.ErrBadLogin):
		return errorf(http.StatusBadRequest, err.Error())
	case err != nil:
		return errorf(http.StatusBadGateway, "PlayOn couldn't be reached: "+err.Error())
	}
	list, err := cl.Library(ctx)
	if err != nil {
		return errorf(http.StatusBadGateway, "Signed in, but PlayOn wouldn't list the recordings: "+err.Error())
	}
	ready := 0
	for _, rec := range list {
		if rec.Ready(time.Now()) {
			ready++
		}
	}
	return writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": ready})
}

// checkPlayOn asks PlayOn for new recordings now.
func (s *Server) checkPlayOn(w http.ResponseWriter, r *http.Request) error {
	c, err := playon.LoadConfig(r.Context(), s.Store)
	if err != nil {
		return err
	}
	if !c.On {
		return errorf(http.StatusBadRequest, "turn PlayOn Cloud on first")
	}
	s.playOn().Wake()
	return s.getPlayOn(w, r)
}

// retryPlayOn downloads a recording that failed, or was skipped, at the
// next check, which is now.
func (s *Server) retryPlayOn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.RetryPlayOn(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		return errorf(http.StatusNotFound, "that recording isn't waiting to be tried again")
	} else if err != nil {
		return err
	}
	s.playOn().Wake()
	return s.getPlayOn(w, r)
}

// skipPlayOn leaves a recording that isn't downloaded yet in PlayOn.
func (s *Server) skipPlayOn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	rec, err := s.Store.PlayOnRecording(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return errorf(http.StatusNotFound, "there's no such recording")
	} else if err != nil {
		return err
	}
	if rec.State != store.PlayOnNew && rec.State != store.PlayOnFailed {
		return errorf(http.StatusBadRequest, "only a recording that isn't downloaded yet can be skipped")
	}
	if err := s.Store.SetPlayOnState(ctx, id, store.PlayOnSkipped, "", "", 0, false); err != nil {
		return err
	}
	return s.getPlayOn(w, r)
}
