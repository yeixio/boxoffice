package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/yeixio/boxoffice/internal/downloader"
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
	_, used, err := s.Search.UpgradesLeft(ctx, set)
	if err != nil {
		return err
	}
	regrabs, _, err := s.Search.RegrabsToday(ctx)
	if err != nil {
		return err
	}
	out := map[string]any{"settings": set, "missing": missing, "upgrades": upgrades, "upgradesThisMonth": used, "regrabsToday": regrabs}
	if waiting, err := s.Store.RegrabsWaiting(ctx); err != nil {
		return err
	} else if len(waiting) > 0 {
		out["regrabsWaiting"] = waiting
	}
	if ok, why := s.Search.AutoDue(ctx); !ok {
		out["paused"] = why
	}
	if ok, why := s.Search.RSSDue(ctx); !ok {
		out["rssPaused"] = why
	}
	if l := s.Search.LastRSS(); l != nil {
		out["lastRss"] = l
	}
	if held, err := s.Store.HeldReleases(ctx); err != nil {
		return err
	} else if len(held) > 0 {
		out["held"] = held
	}
	if c, err := downloader.LoadCap(ctx, s.Store); err != nil {
		return err
	} else if c.Level != downloader.CapOK {
		out["cap"] = c.Why()
	}
	if j, err := s.Store.LastJob(ctx, search.JobSearchWanted); err == nil {
		out["last"] = j
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveAutoSearch(w http.ResponseWriter, r *http.Request) error {
	// Fields left out keep what's saved.
	in, err := search.LoadAutoSettings(r.Context(), s.Store)
	if err != nil {
		return err
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.PerDay < 1 {
		return errorf(http.StatusBadRequest, "make it at least 1 search a day, or turn automatic searches off")
	}
	if in.UpgradesPerMonth < 0 {
		return errorf(http.StatusBadRequest, "make the most upgrades a month 0 for no limit, or more")
	}
	if in.RSSMinutes < search.MinRSSMinutes {
		return errorf(http.StatusBadRequest, fmt.Sprintf("RSS sync can run every %d minutes at most often", search.MinRSSMinutes))
	}
	if in.MinAgeMinutes < 0 || in.MinAgeMinutes > search.MaxMinAgeMinutes {
		return errorf(http.StatusBadRequest, fmt.Sprintf("make the minimum release age between 0 (grab at once) and %d minutes", search.MaxMinAgeMinutes))
	}
	if in.RegrabsPerDay < 0 || in.RegrabsPerDay > search.MaxRegrabsPerDay {
		return errorf(http.StatusBadRequest, fmt.Sprintf("make the most replacements a day between 0 (no limit) and %d", search.MaxRegrabsPerDay))
	}
	if err := search.SaveAutoSettings(r.Context(), s.Store, in); err != nil {
		return err
	}
	return s.autoSearch(w, r)
}

// syncRSS reads the indexers' new releases now.
func (s *Server) syncRSS(w http.ResponseWriter, r *http.Request) error {
	if ok, why := s.Search.RSSDue(r.Context()); !ok {
		return errorf(http.StatusBadRequest, "it can't run: "+why)
	}
	s.Search.SyncRSS(context.WithoutCancel(r.Context()))
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
