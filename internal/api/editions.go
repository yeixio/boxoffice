package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
)

// A movie's page can look for an edition of it (Extended, Director's
// Cut…): which ones the indexers have, and getting one, kept next to the
// copy there or in its place. It's the same as Matinee's "Get another
// edition" (arrapi), from Box Office's own page.

// ownedEditions are the editions the library has of a movie, told the
// way its page tells them (see store.Movie.LabelEditions).
func (s *Server) ownedEditions(r *http.Request, m store.Movie) ([]string, error) {
	var err error
	if m.Editions, err = s.Store.EditionFiles(r.Context(), m.ID); err != nil {
		return nil, err
	}
	m.LabelEditions(s.Store.ReleaseNames(r.Context()))
	var owned []string
	for _, f := range m.Editions {
		if f.Edition != "" && !slices.Contains(owned, f.Edition) {
			owned = append(owned, f.Edition)
		}
	}
	if m.File != nil && m.File.Edition != "" && !slices.Contains(owned, m.File.Edition) {
		owned = append([]string{m.File.Edition}, owned...)
	}
	return owned, nil
}

// movieEditions is GET /api/v1/movies/{id}/editions: the editions the
// indexers have of a movie, and the ones the library has. It searches the
// indexers, so it takes a few seconds.
func (s *Server) movieEditions(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := s.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	owned, err := s.ownedEditions(r, m)
	if err != nil {
		return err
	}
	out := map[string]any{"owned": owned, "known": quality.Editions(), "found": []search.EditionOption{}, "errors": []string{}}
	if s.Search == nil {
		return writeJSON(w, http.StatusOK, out)
	}
	found, notes, err := s.Search.MovieEditions(r.Context(), id, owned)
	if err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	out["found"], out["errors"] = found, notes
	return writeJSON(w, http.StatusOK, out)
}

// askMovieEdition is POST /api/v1/movies/{id}/editions {edition, mode}:
// look for that edition and get the best release of it, kept next to the
// copy there (mode alongside, the default) or in its place (replace). It
// queues the search and answers at once.
func (s *Server) askMovieEdition(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Edition string `json:"edition"`
		Mode    string `json:"mode"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.Edition = strings.TrimSpace(in.Edition)
	if in.Edition == "" {
		return errorf(http.StatusBadRequest, "say which edition, like Director's Cut")
	}
	if e := quality.Edition(in.Edition); e != "" {
		in.Edition = e
	}
	if in.Mode == "" {
		in.Mode = store.ModeAlongside
	}
	if in.Mode != store.ModeAlongside && in.Mode != store.ModeReplace {
		return errorf(http.StatusBadRequest, "mode is replace or alongside")
	}
	m, err := s.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	if m.File == nil {
		in.Mode = store.ModeReplace // nothing to keep it next to
	}
	if err := s.Store.CheckUnlocked(r.Context(), "movie", id); err != nil {
		return err
	}
	if s.Catalog == nil || s.Search == nil {
		return errorf(http.StatusConflict, "searching isn't set up")
	}
	name := m.Title
	if m.Year > 0 {
		name = fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	j := search.AskedJob{Kind: "movie", ID: id, Ask: search.Ask{Edition: in.Edition, Mode: in.Mode}}
	if _, err := s.Catalog.Jobs.Queue(r.Context(), search.JobSearchAsked, fmt.Sprintf("%s (%s)", name, in.Edition), j.Payload()); err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]any{"edition": in.Edition, "mode": in.Mode, "queued": true})
}
