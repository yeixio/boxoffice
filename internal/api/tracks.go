package api

import (
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/subtitles"
	"github.com/yeixio/boxoffice/internal/tracks"
)

// Unwanted tracks (Settings → Library): audio and subtitle tracks in
// languages nobody here wants are dropped from each movie and episode as
// it comes in (see internal/tracks). Off until turned on.
func (s *Server) trackRoutes() []route {
	return []route{
		{"GET /api/v1/tracks", true, s.getTracks},
		{"PUT /api/v1/tracks", true, s.putTracks},
	}
}

// tracker is the one main.go made, or one made here that only answers.
func (s *Server) tracker() *tracks.Service {
	if s.Tracks != nil {
		return s.Tracks
	}
	return &tracks.Service{Store: s.Store, Log: s.Log}
}

// tracksView is GET /api/v1/tracks.
type tracksView struct {
	Config tracks.Config `json:"config"`
	// Saved is what dropping tracks has saved so far.
	Saved tracks.Saved `json:"saved"`
	// Ready is false when ffmpeg or ffprobe isn't installed, so nothing
	// can be dropped.
	Ready     bool                 `json:"ready"`
	Languages []subtitles.Language `json:"languages"`
	// Suggested are the languages to start the list with: the ones
	// subtitles are fetched in.
	Suggested []string `json:"suggested"`
}

func (s *Server) getTracks(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := tracks.LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	saved, err := s.tracker().Saved(ctx)
	if err != nil {
		return err
	}
	v := tracksView{Config: c, Saved: saved, Ready: s.tracker().Ready(), Languages: subtitles.Languages(), Suggested: []string{}}
	if sc, err := subtitles.LoadConfig(ctx, s.Store); err == nil && len(sc.Languages) > 0 {
		v.Suggested = sc.Languages
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) putTracks(w http.ResponseWriter, r *http.Request) error {
	var c tracks.Config
	if err := readJSON(r, &c); err != nil {
		return err
	}
	c.Normalize()
	if err := c.Check(); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if err := tracks.SaveConfig(r.Context(), s.Store, c); err != nil {
		return err
	}
	s.Log.Info("unwanted tracks settings saved", "by", currentUser(r), "on", c.On, "languages", strings.Join(c.Languages, ","))
	return s.getTracks(w, r)
}
