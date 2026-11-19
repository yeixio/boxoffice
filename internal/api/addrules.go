package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// addRulesView is the rules for new titles, with the genres they can pick
// from. The genres are left out when TMDB can't be asked.
type addRulesView struct {
	Rules        []catalog.Rule `json:"rules"`
	MovieGenres  []tmdb.Genre   `json:"movieGenres"`
	SeriesGenres []tmdb.Genre   `json:"seriesGenres"`
}

func (s *Server) addRules(w http.ResponseWriter, r *http.Request) error {
	rules, err := s.Catalog.AddRules(r.Context())
	if err != nil {
		return err
	}
	out := addRulesView{Rules: rules, MovieGenres: []tmdb.Genre{}, SeriesGenres: []tmdb.Genre{}}
	if movies, shows, err := s.Catalog.Genres(r.Context()); err == nil {
		out.MovieGenres, out.SeriesGenres = movies, shows
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveAddRules(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Rules []catalog.Rule `json:"rules"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if _, err := s.Catalog.SaveAddRules(r.Context(), in.Rules); err != nil {
		return err
	}
	return s.addRules(w, r)
}

// addRuleFor says which rule, if any, would pick the folder and profile of
// a title about to be added, so the Add dialog can fill them in and say
// why.
func (s *Server) addRuleFor(w http.ResponseWriter, r *http.Request) error {
	kind := r.URL.Query().Get("kind")
	id, _ := strconv.ParseInt(r.URL.Query().Get("tmdbId"), 10, 64)
	if (kind != "movie" && kind != "series") || id <= 0 {
		return errorf(http.StatusBadRequest, "say which movie or show: kind and tmdbId")
	}
	out := struct {
		Rule *catalog.Rule `json:"rule"`
	}{}
	rule, ok, err := s.Catalog.RuleForTitle(r.Context(), kind, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		return writeJSON(w, http.StatusOK, out)
	}
	if err != nil {
		return err
	}
	if ok {
		out.Rule = &rule
	}
	return writeJSON(w, http.StatusOK, out)
}
