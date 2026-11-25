package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// The home page: rows of titles from TMDB to add, marked with where each
// stands here (see catalog.Home). Like the rest of /api/v1 it takes the
// API key too, so Matinee can show the same rows.

func (s *Server) discoverRoutes() []route {
	return []route{
		{"GET /api/v1/discover", true, s.discoverHome},
		{"GET /api/v1/discover/rows/{id}", true, s.discoverRow},
		{"GET /api/v1/discover/watch/{kind}/{tmdbId}", true, s.discoverWatch},
	}
}

func (s *Server) discoverHome(w http.ResponseWriter, r *http.Request) error {
	home, err := s.Catalog.Home(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, home)
}

// discoverRow is a page (?page=, from 1) of one row.
func (s *Server) discoverRow(w http.ResponseWriter, r *http.Request) error {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	p, err := s.Catalog.Row(r.Context(), r.PathValue("id"), page)
	if errors.Is(err, catalog.ErrNoRow) {
		return errorf(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

// discoverWatch is where a movie or show ({kind} movie or series) can be
// watched: the networks it's made for, and streaming services and shops
// in a country (?country=US; Box Office's own when left out).
func (s *Server) discoverWatch(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("tmdbId"), 10, 64)
	if err != nil || id <= 0 {
		return errorf(http.StatusBadRequest, "tmdbId is a TMDB id")
	}
	ww, err := s.Catalog.Watch(r.Context(), r.PathValue("kind"), id, r.URL.Query().Get("country"))
	if errors.Is(err, catalog.ErrNoKind) {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, tmdb.ErrNotFound) {
		return errorf(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ww)
}
