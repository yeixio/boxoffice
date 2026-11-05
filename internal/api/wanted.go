package api

import (
	"errors"
	"net/http"

	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
)

// autoSearch is what Settings shows about automatic searches: the
// settings, how much is wanted, and how the last one went.
func (s *Server) autoSearch(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	set, err := search.LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return err
	}
	list, err := s.Search.WantedList(ctx)
	if err != nil {
		return err
	}
	missing, upgrades := 0, 0
	for _, w := range list {
		if w.Upgrade {
			upgrades++
		} else {
			missing++
		}
	}
	out := map[string]any{"settings": set, "missing": missing, "upgrades": upgrades}
	if ok, why := s.Search.AutoDue(ctx); !ok {
		out["paused"] = why
	}
	if j, err := s.Store.LastJob(ctx, search.JobSearchWanted); err == nil {
		out["last"] = j
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveAutoSearch(w http.ResponseWriter, r *http.Request) error {
	var in search.AutoSettings
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.PerDay < 1 {
		return errorf(http.StatusBadRequest, "make it at least 1 search a day, or turn automatic searches off")
	}
	if err := search.SaveAutoSettings(r.Context(), s.Store, in); err != nil {
		return err
	}
	return s.autoSearch(w, r)
}

// runAutoSearch starts an automatic search now, rather than at the top of
// the next hour.
func (s *Server) runAutoSearch(w http.ResponseWriter, r *http.Request) error {
	if ok, why := s.Search.AutoDue(r.Context()); !ok {
		return errorf(http.StatusBadRequest, "it can't run: "+why)
	}
	if _, err := s.Catalog.Jobs.Queue(r.Context(), search.JobSearchWanted, "Missing titles and upgrades", "{}"); err != nil {
		return err
	}
	return s.autoSearch(w, r)
}
