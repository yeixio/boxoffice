package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/zone"
)

// Removing watched episodes, the other half of space saver: episodes
// that everyone watching a show has finished leave the library. It is off
// unless someone turns it on, for every show (Settings → Library → Space
// saver) or for one (the show's page, which wins over Settings).
//
// Matinee says how far everyone has got: for each show, the latest
// episode that everyone who has played any of it has finished (see
// store.Watched.AllFinishedEpisode). Someone who hasn't started the show
// isn't counted, which is why this is never on by itself.
//
// The safety rules, in one place:
//   - turning it on shows what would go first (episodes, files, size),
//     and only happens if that is still what's on disk;
//   - episodes are moved to the library's recycle folder, not deleted, so
//     they can be put back until the recycle folder is emptied;
//   - only regular episodes with a file Box Office tracks go: specials
//     and second copies stay;
//   - shows on Do not touch, and shows being moved, are left alone;
//   - nothing goes while Matinee's report is older than a day;
//   - a file that isn't there (a share that dropped out) removes nothing,
//     and its episode stays as it is;
//   - by itself, Box Office removes at most so many episodes a day;
//   - a removed episode is no longer watched for, so it isn't fetched
//     again.

const (
	settingRemoveWatched      = "remove_watched"       // "on", or "" for off
	settingRemoveWatchedToday = "remove_watched_today" // "2026-11-25:12": episodes removed without asking that day
	// RemoveWatchedDailyCap is how many watched episodes a day Box Office
	// removes by itself. Turning it on removes what was shown, whatever
	// its size.
	RemoveWatchedDailyCap = 30
)

func (s *Server) watchedRemovalRoutes() []route {
	return []route{
		{"GET /api/v1/space-saver", true, s.getSpaceSaver},
		{"GET /api/v1/space-saver/removal", true, s.spaceSaverRemoval},
		{"PUT /api/v1/space-saver", true, s.putSpaceSaver},
		{"GET /api/v1/series/{id}/watched/removal", true, s.seriesWatchedRemoval},
	}
}

// removeWatchedDefault is what Settings says for shows that don't say.
func (s *Server) removeWatchedDefault(ctx context.Context) (bool, error) {
	v, err := s.Store.Setting(ctx, settingRemoveWatched)
	return v == "on", err
}

// removesWatched says whether a show's watched episodes are removed.
func removesWatched(t store.Series, def bool) bool {
	return t.RemoveWatched == "on" || t.RemoveWatched == "" && def
}

// watchedRemoval is what removing watched episodes would take, for one
// show or for several.
type watchedRemoval struct {
	Shows    int   `json:"shows"`
	Episodes int   `json:"episodes"`
	Files    int   `json:"files"`
	Size     int64 `json:"size"`
	// Reported says Matinee has sent a watch report, and Stale that the
	// last one is too old to go by: nothing is removed until a new one.
	Reported bool `json:"reported"`
	Stale    bool `json:"stale"`
	// Season and Episode are the latest episode everyone watching the
	// show has finished (one show only).
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
	// Problem says why nothing of the show can be removed right now.
	Problem string `json:"problem,omitempty"`
	// KeepDays is how long the recycle folders keep what's moved there; 0
	// keeps it until someone empties them.
	KeepDays int `json:"keepDays"`
	// DailyCap is how many episodes a day go without asking.
	DailyCap int `json:"dailyCap"`

	parts []watchedPart
}

// watchedPart is one show's part of a removal.
type watchedPart struct {
	show store.Series
	eps  []store.Episode
	plan library.Plan
}

// watchedEpisodes picks, from a show's episodes in order, the ones
// everyone watching has finished whose file is on disk: at most limit of
// them, or all when limit is negative.
func watchedEpisodes(eps []store.Episode, w store.Watched, limit int) []store.Episode {
	var out []store.Episode
	if w.AllFinishedSeason <= 0 || w.AllFinishedEpisode <= 0 {
		return nil
	}
	for _, e := range eps {
		if limit >= 0 && len(out) >= limit {
			break
		}
		if e.Season <= 0 || e.File == nil {
			continue
		}
		if e.Season > w.AllFinishedSeason || e.Season == w.AllFinishedSeason && e.Number > w.AllFinishedEpisode {
			continue
		}
		if info, err := os.Lstat(e.File.Path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, e)
	}
	return out
}

// planWatched plans removing the watched episodes of the shows given, as
// if it were on for each of them. limit is the most episodes it takes in
// all, or negative for no limit.
func (s *Server) planWatched(ctx context.Context, shows []store.Series, limit int) (watchedRemoval, error) {
	out := watchedRemoval{KeepDays: RecycleDays(ctx, s.Store), DailyCap: RemoveWatchedDailyCap}
	report, ok, err := s.Store.LastWatchReport(ctx, "series")
	if err != nil {
		return out, err
	}
	out.Reported = ok
	out.Stale = ok && time.Since(report.At) > cleanupReportAge
	if !ok || out.Stale || len(shows) == 0 {
		return out, nil
	}
	all, err := s.Store.WatchedTitles(ctx, "series")
	if err != nil {
		return out, err
	}
	for _, t := range shows {
		w := all[t.ID]
		if len(shows) == 1 {
			out.Season, out.Episode = w.AllFinishedSeason, w.AllFinishedEpisode
		}
		if w.AllFinishedSeason <= 0 || limit == 0 {
			continue
		}
		eps, err := s.Store.Episodes(ctx, t.ID)
		if err != nil {
			return out, err
		}
		gone := watchedEpisodes(eps, w, limit)
		if len(gone) == 0 {
			continue
		}
		fr, _, err := s.episodesPlan(ctx, t, gone)
		if err != nil {
			return out, err
		}
		if fr.Problem != "" {
			if len(shows) == 1 {
				out.Problem = fr.Problem
			}
			continue
		}
		if fr.Count == 0 {
			continue
		}
		out.Shows++
		out.Episodes += len(gone)
		out.Files += fr.Count
		out.Size += fr.Size
		out.parts = append(out.parts, watchedPart{t, gone, fr.Plan})
		if limit > 0 {
			limit -= len(gone)
		}
	}
	return out, nil
}

// removeWatched carries a plan out: each show's files go to its recycle
// folder, and the episodes they were are no longer watched for. It says
// how many episodes went. why is added to the note on Activity.
func (s *Server) removeWatched(ctx context.Context, plan watchedRemoval, why string) (int, error) {
	ctx = context.WithoutCancel(ctx)
	removed := 0
	var first error
	for _, part := range plan.parts {
		err := library.RecyclePlan(part.plan)
		if err != nil {
			s.Log.Error("couldn't move watched episodes to the recycle folder", "show", part.show.Title, "err", err)
			if first == nil {
				first = fmt.Errorf("%s: %w", part.show.Title, err)
			}
		}
		// Whatever happened, the records follow the disk: an episode
		// whose file went is forgotten and no longer watched for.
		n := 0
		for _, e := range part.eps {
			if _, err := os.Lstat(e.File.Path); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := s.Store.DeleteFile(ctx, e.File.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return removed, err
			}
			if e.Monitored {
				if err := s.Store.SetEpisodeMonitored(ctx, e.ID, false); err != nil {
					return removed, err
				}
			}
			n++
		}
		if n == 0 {
			continue
		}
		removed += n
		s.Log.Info("moved watched episodes to the recycle folder", "show", part.show.Title, "episodes", n, "files", part.plan.Count, "bytes", part.plan.Size, "why", why)
		note := fmt.Sprintf("Moved %d watched episodes (%d files, %s) to the recycle folder: everyone watching has finished them. They're no longer watched for.", n, part.plan.Count, library.Bytes(part.plan.Size))
		if why != "" {
			note += " " + why
		}
		s.Store.RecordJob(ctx, JobDeleteFiles, part.show.Title, "done", note)
	}
	return removed, first
}

func (s *Server) removedWatchedToday(ctx context.Context, now time.Time) (int, error) {
	v, err := s.Store.Setting(ctx, settingRemoveWatchedToday)
	if err != nil {
		return 0, err
	}
	day, n, _ := strings.Cut(v, ":")
	if day != zone.In(now).Format(time.DateOnly) {
		return 0, nil
	}
	count, _ := strconv.Atoi(n)
	return count, nil
}

// RemoveWatched removes the watched episodes of every show it's on for,
// up to the day's cap: Matinee has said what was watched.
func (s *Server) RemoveWatched(ctx context.Context) error {
	s.watchedMu.Lock()
	defer s.watchedMu.Unlock()
	def, err := s.removeWatchedDefault(ctx)
	if err != nil {
		return err
	}
	all, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return err
	}
	var shows []store.Series
	for _, t := range all {
		if removesWatched(t, def) {
			shows = append(shows, t)
		}
	}
	if len(shows) == 0 {
		return nil
	}
	now := time.Now()
	done, err := s.removedWatchedToday(ctx, now)
	if err != nil {
		return err
	}
	left := RemoveWatchedDailyCap - done
	if left <= 0 {
		return nil
	}
	plan, err := s.planWatched(ctx, shows, left)
	if err != nil || len(plan.parts) == 0 {
		return err
	}
	n, err := s.removeWatched(ctx, plan, "")
	if n > 0 {
		if e := s.Store.SetSetting(context.WithoutCancel(ctx), settingRemoveWatchedToday, zone.In(now).Format(time.DateOnly)+":"+strconv.Itoa(done+n)); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// removeWatchedSoon is RemoveWatched without making Matinee wait.
func (s *Server) removeWatchedSoon(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		if err := s.RemoveWatched(ctx); err != nil {
			s.Log.Warn("couldn't remove watched episodes", "err", err)
		}
	}()
}

// turnOnWatchedRemoval removes what a person was shown when they turned
// removing watched episodes on, for the shows it newly covers: files and
// size are what they saw. Nothing goes, and it stays off, when that isn't
// what's there any more. The caller holds watchedMu and saves the
// setting between the check and carrying it out, through save.
func (s *Server) turnOnWatchedRemoval(ctx context.Context, shows []store.Series, files *int, size *int64, save func() error) error {
	if files == nil || size == nil {
		return errorf(http.StatusBadRequest, "say how many files (expectFiles) and bytes (expectSize) you were shown would be removed")
	}
	plan, err := s.planWatched(ctx, shows, -1)
	if err != nil {
		return err
	}
	if plan.Files != *files || plan.Size != *size {
		return errorf(http.StatusConflict, fmt.Sprintf("Nothing was removed and it's still off: what would go changed since you looked (now %d files, %s). Take another look first.", plan.Files, library.Bytes(plan.Size)))
	}
	if err := save(); err != nil {
		return err
	}
	if _, err := s.removeWatched(ctx, plan, "Removing watched episodes was just turned on."); err != nil {
		return errorf(http.StatusInternalServerError, "It's on, but not everything could be moved to the recycle folder: "+err.Error()+". What's left goes when Matinee next reports.")
	}
	return nil
}

// followingShows are the shows that go by Settings.
func (s *Server) followingShows(ctx context.Context) ([]store.Series, error) {
	all, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Series
	for _, t := range all {
		if t.RemoveWatched == "" {
			out = append(out, t)
		}
	}
	return out, nil
}

type spaceSaverView struct {
	// RemoveWatched is the choice for shows that don't have their own.
	RemoveWatched bool `json:"removeWatched"`
	// ShowsOn and ShowsOff are how many shows have their own choice.
	ShowsOn  int `json:"showsOn"`
	ShowsOff int `json:"showsOff"`
	// Reported and Stale are about Matinee's watch report (see
	// watchedRemoval).
	Reported bool `json:"reported"`
	Stale    bool `json:"stale"`
	DailyCap int  `json:"dailyCap"`
}

func (s *Server) getSpaceSaver(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	def, err := s.removeWatchedDefault(ctx)
	if err != nil {
		return err
	}
	v := spaceSaverView{RemoveWatched: def, DailyCap: RemoveWatchedDailyCap}
	all, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return err
	}
	for _, t := range all {
		switch t.RemoveWatched {
		case "on":
			v.ShowsOn++
		case "off":
			v.ShowsOff++
		}
	}
	report, ok, err := s.Store.LastWatchReport(ctx, "series")
	if err != nil {
		return err
	}
	v.Reported, v.Stale = ok, ok && time.Since(report.At) > cleanupReportAge
	return writeJSON(w, http.StatusOK, v)
}

// spaceSaverRemoval is what turning it on in Settings would remove now.
func (s *Server) spaceSaverRemoval(w http.ResponseWriter, r *http.Request) error {
	shows, err := s.followingShows(r.Context())
	if err != nil {
		return err
	}
	plan, err := s.planWatched(r.Context(), shows, -1)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, plan)
}

func (s *Server) putSpaceSaver(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RemoveWatched bool `json:"removeWatched"`
		// What the person was shown would be removed, when turning it on.
		ExpectFiles *int   `json:"expectFiles"`
		ExpectSize  *int64 `json:"expectSize"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	s.watchedMu.Lock()
	defer s.watchedMu.Unlock()
	was, err := s.removeWatchedDefault(ctx)
	if err != nil {
		return err
	}
	value := ""
	if in.RemoveWatched {
		value = "on"
	}
	save := func() error { return s.Store.SetSetting(ctx, settingRemoveWatched, value) }
	if in.RemoveWatched && !was {
		shows, err := s.followingShows(ctx)
		if err != nil {
			return err
		}
		if err := s.turnOnWatchedRemoval(ctx, shows, in.ExpectFiles, in.ExpectSize, save); err != nil {
			return err
		}
	} else if err := save(); err != nil {
		return err
	}
	return s.getSpaceSaver(w, r)
}

// seriesWatchedRemoval is what removing a show's watched episodes would
// take now.
func (s *Server) seriesWatchedRemoval(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return err
	}
	plan, err := s.planWatched(r.Context(), []store.Series{t}, -1)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, plan)
}
