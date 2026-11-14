package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/subtitles"
)

// Subtitles (Settings → Subtitles): Box Office fetches subtitles in the
// languages picked, for new downloads at once and for the rest of the
// library a few hundred files a day (see internal/subtitles). Each movie
// and episode file says which it has.
func (s *Server) subtitleRoutes() []route {
	return []route{
		{"GET /api/v1/subtitles", true, s.getSubtitles},
		{"PUT /api/v1/subtitles", true, s.putSubtitles},
		{"POST /api/v1/subtitles/test", true, s.testSubtitles},
		{"POST /api/v1/subtitles/again", true, s.subtitlesAgain},
		{"POST /api/v1/movies/{id}/subtitles", true, s.movieSubtitles},
		{"POST /api/v1/series/{id}/subtitles", true, s.seriesSubtitles},
	}
}

// subtitler is the subtitle fetcher: the one main.go made, or one made
// here (it then only answers; nothing runs it).
func (s *Server) subtitler() *subtitles.Service {
	s.subtitleOnce.Do(func() {
		if s.Subtitles == nil {
			s.Subtitles = &subtitles.Service{Store: s.Store, Log: s.Log, UserAgent: "BoxOffice/" + s.Version}
		}
	})
	return s.Subtitles
}

// subtitlesView is GET /api/v1/subtitles. The OpenSubtitles API key and
// password never leave the server.
type subtitlesView struct {
	Config    subtitlesConfigView  `json:"config"`
	Status    subtitles.Status     `json:"status"`
	Languages []subtitles.Language `json:"languages"`
	// Suggested are languages people chose to see Box Office and the
	// request site in, to start the list with.
	Suggested []string `json:"suggested"`
}

type subtitlesConfigView struct {
	subtitles.Config
	OpenSubtitles struct {
		Username    string `json:"username"`
		HasAPIKey   bool   `json:"hasApiKey"`
		HasPassword bool   `json:"hasPassword"`
	} `json:"openSubtitles"`
}

func (s *Server) getSubtitles(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := subtitles.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	st, err := s.subtitler().State(ctx, c)
	if err != nil {
		return err
	}
	picked, err := s.Store.PickedLanguages(ctx)
	if err != nil {
		return err
	}
	suggested := []string{}
	for _, l := range picked {
		if code := subtitles.Code(l); code != "" && !slices.Contains(suggested, code) {
			suggested = append(suggested, code)
		}
	}
	v := subtitlesView{Status: st, Languages: subtitles.Languages(), Suggested: suggested}
	v.Config.Config = c
	o := c.OpenSubtitles
	v.Config.OpenSubtitles.Username, v.Config.OpenSubtitles.HasAPIKey, v.Config.OpenSubtitles.HasPassword = o.Username, o.APIKey != "", o.Password != ""
	return writeJSON(w, http.StatusOK, v)
}

// subtitlesInput is the settings as the page sends them: an empty API key
// or password keeps the saved one (the password only while the username
// stays the same), and Forget removes the OpenSubtitles login.
type subtitlesInput struct {
	subtitles.Config
	Forget bool `json:"forgetOpenSubtitles"`
}

func (in subtitlesInput) merge(old subtitles.Config) subtitles.Config {
	c := in.Config
	c.Normalize()
	switch {
	case in.Forget:
		c.OpenSubtitles = subtitles.OpenSubtitlesLogin{}
	default:
		o := &c.OpenSubtitles
		if o.APIKey == "" {
			o.APIKey = old.OpenSubtitles.APIKey
		}
		if o.Password == "" && o.Username == old.OpenSubtitles.Username {
			o.Password = old.OpenSubtitles.Password
		}
		if o.Username == "" {
			o.Password = ""
		}
	}
	return c
}

func (s *Server) putSubtitles(w http.ResponseWriter, r *http.Request) error {
	var in subtitlesInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	old, err := subtitles.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	c := in.merge(old)
	if err := c.Check(); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if err := subtitles.SaveConfig(ctx, s.Store, c); err != nil {
		return err
	}
	s.Log.Info("subtitle settings saved", "by", currentUser(r), "on", c.On, "languages", strings.Join(c.Languages, ","))
	if c.On {
		s.subtitler().Wake()
	}
	return s.getSubtitles(w, r)
}

// testSubtitles signs in to OpenSubtitles with the login in the request,
// or the saved one, to say whether it works.
func (s *Server) testSubtitles(w http.ResponseWriter, r *http.Request) error {
	var in subtitlesInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	old, err := subtitles.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	k := in.merge(old).OpenSubtitles
	if k.APIKey == "" {
		return errorf(http.StatusBadRequest, "enter the OpenSubtitles API key first")
	}
	err = subtitles.NewOpenSubtitles(k, "BoxOffice/"+s.Version).Check(ctx)
	switch {
	case errors.Is(err, subtitles.ErrBadKey), errors.Is(err, subtitles.ErrBadLogin):
		return errorf(http.StatusBadRequest, err.Error())
	case err != nil:
		return errorf(http.StatusBadGateway, "OpenSubtitles couldn't be reached: "+err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// subtitlesAgain looks for every missing subtitle again now (within the
// day's limit).
func (s *Server) subtitlesAgain(w http.ResponseWriter, r *http.Request) error {
	if err := s.Store.RetrySubtitles(r.Context()); err != nil {
		return err
	}
	s.subtitler().Wake()
	return s.getSubtitles(w, r)
}

// movieSubtitles looks for a movie's missing subtitles now.
func (s *Server) movieSubtitles(w http.ResponseWriter, r *http.Request) error {
	return s.findSubtitles(w, r, "movie", nil)
}

// seriesSubtitles looks for a show's missing subtitles now: its episodes
// listed, or all of them.
func (s *Server) seriesSubtitles(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		EpisodeIDs []int64 `json:"episodeIds"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &in); err != nil {
			return err
		}
	}
	return s.findSubtitles(w, r, "series", in.EpisodeIDs)
}

func (s *Server) findSubtitles(w http.ResponseWriter, r *http.Request, kind string, episodes []int64) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	c, err := subtitles.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	if !c.On {
		return errorf(http.StatusBadRequest, "subtitles are turned off; turn them on in Settings → Subtitles")
	}
	files, err := s.Store.SubtitleFilesOf(ctx, kind, id, episodes...)
	if err != nil {
		return err
	}
	var ids []int64
	for _, f := range files {
		if !f.Locked {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 {
		return errorf(http.StatusBadRequest, "there's no file to find subtitles for")
	}
	if err := s.subtitler().FindNow(ctx, ids...); err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]int{"files": len(ids)})
}

// fillSubtitles adds each file's subtitles, in the languages wanted, when
// they're turned on.
func (s *Server) fillSubtitles(ctx context.Context, files ...*store.File) error {
	c, err := subtitles.LoadConfig(ctx, s.Store)
	if err != nil || !c.On || len(c.Languages) == 0 {
		return err
	}
	var ids []int64
	for _, f := range files {
		if f != nil {
			ids = append(ids, f.ID)
		}
	}
	known, err := s.Store.SubtitlesOf(ctx, ids...)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f != nil {
			f.Subtitles = subtitles.For(c, known[f.ID])
		}
	}
	return nil
}
