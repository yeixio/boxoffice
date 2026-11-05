package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/store"
)

// ---- deleting files from the library ----
//
// Removing a movie or a show can delete its folder too, and a movie's,
// a season's or an episode's files can be deleted while the title stays.
// Each has a GET …/removal that says what would go (files, size, and why
// it can't, if it can't), and the DELETE must say how many files and
// bytes the person was shown: if the disk has changed since, nothing is
// deleted. Deleting is for good; it frees the space.

// JobDeleteFiles is how deletions show on Activity.
const JobDeleteFiles = "delete-files"

// release is a download that brought a file into the library, which can
// be blocked so it isn't grabbed again.
type release struct {
	Name    string `json:"name"`
	Indexer string `json:"indexer,omitempty"`
}

// fileRemoval is the plan for deleting a title's files, with what else
// the person can choose.
type fileRemoval struct {
	library.Plan
	// Releases the files came from, for the blocklist.
	Releases []release `json:"releases"`
	// Watched says Box Office is watching for them, so it would download
	// them again unless told to stop.
	Watched bool `json:"watched"`
}

// shown is the file count and size the person was shown.
func shown(r *http.Request) (int, int64, error) {
	q := r.URL.Query()
	n, err1 := strconv.Atoi(q.Get("expectFiles"))
	size, err2 := strconv.ParseInt(q.Get("expectSize"), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, errorf(http.StatusBadRequest, "say how many files (expectFiles) and bytes (expectSize) you were shown")
	}
	return n, size, nil
}

func flag(r *http.Request, name string) bool { return r.URL.Query().Get(name) == "true" }

// carryOut deletes as planned, if the plan is what the person saw, and
// notes it on Activity and in the log.
func (s *Server) carryOut(r *http.Request, p library.Plan, subject string) error {
	n, size, err := shown(r)
	if err != nil {
		return err
	}
	if p.Problem != "" {
		return errorf(http.StatusConflict, "Nothing was deleted: "+p.Problem+".")
	}
	if !p.Matches(n, size) {
		return errorf(http.StatusConflict, fmt.Sprintf("Nothing was deleted: what's on disk changed since you looked (now %d files, %s). Take another look first.", p.Count, library.Bytes(p.Size)))
	}
	if p.Count == 0 {
		return nil
	}
	if err := library.Delete(p); err != nil {
		s.Log.Error("couldn't delete files from the library", "title", subject, "err", err)
		return errorf(http.StatusInternalServerError, "Couldn't delete all of it: "+err.Error()+". What's left is still on the list; try again.")
	}
	what := fmt.Sprintf("%d files, %s", p.Count, library.Bytes(p.Size))
	if p.Folder != "" {
		what = "the folder " + p.Folder + " (" + what + ")"
	}
	s.Log.Info("deleted files from the library", "title", subject, "folder", p.Folder, "files", p.Count, "bytes", p.Size)
	s.Store.RecordJob(context.WithoutCancel(r.Context()), JobDeleteFiles, subject, "done", "Deleted "+what+" for good.")
	return nil
}

// block puts the releases on the blocklist.
func (s *Server) block(ctx context.Context, list []store.Download) {
	for _, d := range list {
		s.Store.Block(ctx, store.Blocked{Kind: d.Kind, TitleID: d.TitleID, SourceTitle: d.Name, Quality: d.Quality, Protocol: "usenet",
			Indexer: d.Indexer, Reason: "Its files were deleted from the library", At: time.Now()})
	}
}

func releases(list []store.Download) []release {
	out := []release{}
	for _, d := range list {
		out = append(out, release{d.Name, d.Indexer})
	}
	return out
}

// latestPer keeps, for each episode, only the newest download that
// brought it: the one its file on disk came from.
func latestPer(list []store.Download, episodes []int64) []store.Download {
	var out []store.Download
	for _, e := range episodes {
		for _, d := range list {
			if slices.Contains(d.EpisodeIDs, e) {
				if !slices.ContainsFunc(out, func(x store.Download) bool { return x.ID == d.ID }) {
					out = append(out, d)
				}
				break
			}
		}
	}
	return out
}

// -- whole titles --

func (s *Server) moviePlan(r *http.Request) (store.Movie, library.Plan, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Movie{}, library.Plan{}, err
	}
	m, err := s.Store.Movie(r.Context(), id)
	if err != nil {
		return m, library.Plan{}, err
	}
	p, err := library.FolderPlan(r.Context(), s.Store, "movie", id, m.RootFolder, m.Path)
	if err == nil {
		err = s.lockPlan(r.Context(), "movie", id, &p)
	}
	return m, p, err
}

// lockPlan makes a plan for a title on Do not touch delete nothing.
func (s *Server) lockPlan(ctx context.Context, kind string, id int64, p *library.Plan) error {
	locked, err := s.Store.TitleLocked(ctx, kind, id)
	if err == nil && locked {
		p.Problem = "it's set to Do not touch, so Box Office never deletes its files (pick another quality profile for it first)"
	}
	return err
}

func (s *Server) movieRemoval(w http.ResponseWriter, r *http.Request) error {
	_, p, err := s.moviePlan(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) seriesPlan(r *http.Request) (store.Series, library.Plan, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Series{}, library.Plan{}, err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return t, library.Plan{}, err
	}
	p, err := library.FolderPlan(r.Context(), s.Store, "series", id, t.RootFolder, t.Path)
	if err == nil {
		err = s.lockPlan(r.Context(), "series", id, &p)
	}
	return t, p, err
}

func (s *Server) seriesRemoval(w http.ResponseWriter, r *http.Request) error {
	_, p, err := s.seriesPlan(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

// -- a movie's file --

func (s *Server) movieFilePlan(r *http.Request) (store.Movie, fileRemoval, []store.Download, error) {
	m, err := s.movieByPath(r)
	if err != nil {
		return m, fileRemoval{}, nil, err
	}
	var files []store.File
	if m.File != nil {
		files = append(files, *m.File)
	}
	imports, err := s.Store.ImportsFor(r.Context(), "movie", m.ID)
	if err != nil {
		return m, fileRemoval{}, nil, err
	}
	imports = imports[:min(len(imports), 1)]
	fr := fileRemoval{Plan: library.FilesPlan(m.RootFolder, m.Path, files), Releases: releases(imports), Watched: m.Monitored}
	if err := s.lockPlan(r.Context(), "movie", m.ID, &fr.Plan); err != nil {
		return m, fileRemoval{}, nil, err
	}
	return m, fr, imports, nil
}

func (s *Server) movieByPath(r *http.Request) (store.Movie, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Movie{}, err
	}
	return s.Store.Movie(r.Context(), id)
}

func (s *Server) movieFileRemoval(w http.ResponseWriter, r *http.Request) error {
	_, p, _, err := s.movieFilePlan(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteMovieFile(w http.ResponseWriter, r *http.Request) error {
	m, p, imports, err := s.movieFilePlan(r)
	if err != nil {
		return err
	}
	if err := s.carryOut(r, p.Plan, m.Title); err != nil {
		return err
	}
	ctx := r.Context()
	if m.File != nil {
		if err := s.Store.DeleteFile(ctx, m.File.ID); err != nil && err != store.ErrNotFound {
			return err
		}
	}
	if flag(r, "block") {
		s.block(ctx, imports)
	}
	if flag(r, "unmonitor") && m.Monitored {
		m.Monitored = false
		if err := s.Store.UpdateMovie(ctx, m); err != nil {
			return err
		}
	}
	return s.movie(w, r)
}

// -- a season's or an episode's files --

func (s *Server) episodesPlan(ctx context.Context, t store.Series, eps []store.Episode) (fileRemoval, []store.Download, error) {
	var files []store.File
	var ids []int64
	watched := false
	for _, e := range eps {
		if e.File != nil {
			files = append(files, *e.File)
			ids = append(ids, e.ID)
		}
		watched = watched || e.Monitored
	}
	var imports []store.Download
	if len(ids) > 0 {
		all, err := s.Store.ImportsFor(ctx, "series", t.ID, ids...)
		if err != nil {
			return fileRemoval{}, nil, err
		}
		imports = latestPer(all, ids)
	}
	fr := fileRemoval{Plan: library.FilesPlan(t.RootFolder, t.Path, files), Releases: releases(imports), Watched: watched}
	if err := s.lockPlan(ctx, "series", t.ID, &fr.Plan); err != nil {
		return fileRemoval{}, nil, err
	}
	return fr, imports, nil
}

func (s *Server) seasonEpisodes(r *http.Request) (store.Series, int, []store.Episode, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Series{}, 0, nil, err
	}
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		return store.Series{}, 0, nil, errorf(http.StatusNotFound, "not found")
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return t, n, nil, err
	}
	if !slices.ContainsFunc(t.Seasons, func(se store.Season) bool { return se.Number == n }) {
		return t, n, nil, errorf(http.StatusNotFound, "there's no such season")
	}
	all, err := s.Store.Episodes(r.Context(), id)
	if err != nil {
		return t, n, nil, err
	}
	var eps []store.Episode
	for _, e := range all {
		if e.Season == n {
			eps = append(eps, e)
		}
	}
	return t, n, eps, nil
}

func (s *Server) seasonFilesRemoval(w http.ResponseWriter, r *http.Request) error {
	t, _, eps, err := s.seasonEpisodes(r)
	if err != nil {
		return err
	}
	p, _, err := s.episodesPlan(r.Context(), t, eps)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteSeasonFiles(w http.ResponseWriter, r *http.Request) error {
	t, n, eps, err := s.seasonEpisodes(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, imports, err := s.episodesPlan(ctx, t, eps)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s season %d", t.Title, n)
	if n == 0 {
		name = t.Title + " specials"
	}
	if err := s.carryOut(r, p.Plan, name); err != nil {
		return err
	}
	if err := s.forgetFiles(ctx, eps); err != nil {
		return err
	}
	if flag(r, "block") {
		s.block(ctx, imports)
	}
	if flag(r, "unmonitor") {
		if err := s.Store.SetSeason(ctx, t.ID, store.Season{Number: n, Monitored: false}); err != nil {
			return err
		}
		if err := s.Store.SetSeasonEpisodesMonitored(ctx, t.ID, n, false); err != nil {
			return err
		}
	}
	return s.series(w, r)
}

// forgetFiles takes the episodes' files off their records; the episodes
// stay on the list.
func (s *Server) forgetFiles(ctx context.Context, eps []store.Episode) error {
	for _, e := range eps {
		if e.File != nil {
			if err := s.Store.DeleteFile(ctx, e.File.ID); err != nil && err != store.ErrNotFound {
				return err
			}
		}
	}
	return nil
}

func (s *Server) episodeAndSeries(r *http.Request) (store.Series, store.Episode, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Series{}, store.Episode{}, err
	}
	e, err := s.Store.Episode(r.Context(), id)
	if err != nil {
		return store.Series{}, e, err
	}
	t, err := s.Store.Series(r.Context(), e.SeriesID)
	return t, e, err
}

func (s *Server) episodeFileRemoval(w http.ResponseWriter, r *http.Request) error {
	t, e, err := s.episodeAndSeries(r)
	if err != nil {
		return err
	}
	p, _, err := s.episodesPlan(r.Context(), t, []store.Episode{e})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteEpisodeFile(w http.ResponseWriter, r *http.Request) error {
	t, e, err := s.episodeAndSeries(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, imports, err := s.episodesPlan(ctx, t, []store.Episode{e})
	if err != nil {
		return err
	}
	if err := s.carryOut(r, p.Plan, fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number)); err != nil {
		return err
	}
	if err := s.forgetFiles(ctx, []store.Episode{e}); err != nil {
		return err
	}
	if flag(r, "block") {
		s.block(ctx, imports)
	}
	if flag(r, "unmonitor") && e.Monitored {
		if err := s.Store.SetEpisodeMonitored(ctx, e.ID, false); err != nil {
			return err
		}
	}
	e, err = s.Store.Episode(ctx, e.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, e)
}

// ---- the recycle folders ----

const settingRecycleDays = "recycle_keep_days"

// DefaultRecycleDays is how long replaced copies are kept when Settings
// doesn't say: a week, time enough to notice a replacement is worse.
const DefaultRecycleDays = 7

// RecycleDays is how many days replaced copies stay in the recycle
// folders before they're deleted; 0 keeps them until someone empties them.
func RecycleDays(ctx context.Context, st *store.Store) int {
	v, _ := st.Setting(ctx, settingRecycleDays)
	if v == "" {
		return DefaultRecycleDays
	}
	n, _ := strconv.Atoi(v)
	return max(n, 0)
}

func (s *Server) recycle(w http.ResponseWriter, r *http.Request) error {
	roots, err := s.Store.LibraryRoots(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"folders": library.RecycleUsage(roots), "keepDays": RecycleDays(r.Context(), s.Store)})
}

func (s *Server) editRecycle(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		KeepDays int `json:"keepDays"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.KeepDays < 0 || in.KeepDays > 3650 {
		return errorf(http.StatusBadRequest, "keep them between 0 (until you empty them) and 3650 days")
	}
	if err := s.Store.SetSetting(r.Context(), settingRecycleDays, strconv.Itoa(in.KeepDays)); err != nil {
		return err
	}
	return s.recycle(w, r)
}

// emptyRecycle deletes everything in the recycle folders, for good.
func (s *Server) emptyRecycle(w http.ResponseWriter, r *http.Request) error {
	roots, err := s.Store.LibraryRoots(r.Context())
	if err != nil {
		return err
	}
	var freed int64
	for _, root := range roots {
		n, err := library.EmptyRecycle(root, time.Time{})
		freed += n
		if err != nil {
			return errorf(http.StatusInternalServerError, fmt.Sprintf("Freed %s, but couldn't empty %s: %v", library.Bytes(freed), root, err))
		}
	}
	if freed > 0 {
		s.Log.Info("emptied the recycle folders", "bytes", freed)
		s.Store.RecordJob(context.WithoutCancel(r.Context()), JobDeleteFiles, "Recycle folders", "done", "Emptied the recycle folders, freeing "+library.Bytes(freed)+".")
	}
	return s.recycle(w, r)
}

// RecycleSweepEvery deletes replaced copies older than the setting says,
// every few hours.
func RecycleSweepEvery(ctx context.Context, st *store.Store, log interface {
	Info(string, ...any)
	Warn(string, ...any)
}) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		if days := RecycleDays(ctx, st); days > 0 {
			roots, _ := st.LibraryRoots(ctx)
			var freed int64
			for _, root := range roots {
				n, err := library.EmptyRecycle(root, time.Now().AddDate(0, 0, -days))
				freed += n
				if err != nil {
					log.Warn("couldn't clear old copies from a recycle folder", "root", root, "err", err)
				}
			}
			if freed > 0 {
				log.Info("cleared old copies from the recycle folders", "bytes", freed, "days", days)
				st.RecordJob(ctx, JobDeleteFiles, "Recycle folders", "done", fmt.Sprintf("Deleted copies replaced more than %d days ago, freeing %s.", days, library.Bytes(freed)))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
