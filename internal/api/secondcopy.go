package api

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/store"
)

// A movie can keep a second copy at another quality: a 1080p one next to
// the 4K one, say, for screens and connections the big one is too much
// for. It has its own quality profile (movies.second_profile_id) and is
// searched for, upgraded and moved in like the first, to its own place:
// next to the first copy, or in a folder named like the movie's in another
// library folder (movies.second_root). The first copy never counts as the
// second or the other way round.

// editSecond takes the second copy asked for in an edit of a movie, and
// says whether it should be looked for now. Turning it off leaves the copy
// on disk and on the movie's page; deleting it is DELETE
// /movies/{id}/second.
func (s *Server) editSecond(ctx context.Context, m *store.Movie, in editInput) (look bool, err error) {
	return s.setSecond(ctx, "movie", m.ProfileID, m.RootFolder, &m.SecondProfileID, &m.SecondRoot, m.Second != nil, in)
}

// editSeriesSecond is editSecond for a show's second copies.
func (s *Server) editSeriesSecond(ctx context.Context, t *store.Series, in editInput) (look bool, err error) {
	onDisk := false
	if in.SecondRoot != nil {
		files, err := s.seriesSeconds(ctx, t.ID)
		if err != nil {
			return false, err
		}
		onDisk = len(files) > 0
	}
	return s.setSecond(ctx, "series", t.ProfileID, t.RootFolder, &t.SecondProfileID, &t.SecondRoot, onDisk, in)
}

// setSecond checks and sets a title's second copy as an edit asks:
// profile and root are the title's own, second and secondRoot what's kept
// for its second copy, and onDisk whether one is there already.
func (s *Server) setSecond(ctx context.Context, kind string, profile int, root string, second *int, secondRoot *string, onDisk bool, in editInput) (look bool, err error) {
	was, wasRoot := *second, *secondRoot
	if in.SecondProfileID != nil {
		*second = *in.SecondProfileID
	}
	if in.SecondRoot != nil {
		*secondRoot = *in.SecondRoot
	}
	if *secondRoot != "" && filepath.Clean(*secondRoot) == filepath.Clean(root) {
		*secondRoot = ""
	}
	if onDisk && *secondRoot != wasRoot {
		return false, errorf(http.StatusConflict, "there's a second copy on disk already; delete it first to keep it somewhere else")
	}
	if *second == 0 {
		return false, nil
	}
	if *second == was && *secondRoot == wasRoot && in.ProfileID == nil {
		return false, nil
	}
	p, err := s.Store.Profile(ctx, *second)
	if err != nil {
		return false, errorf(http.StatusBadRequest, "there's no such quality profile for the second copy")
	}
	if p.Locked {
		return false, errorf(http.StatusBadRequest, "a second copy needs a profile that takes downloads, not Do not touch")
	}
	if *second == profile {
		return false, errorf(http.StatusBadRequest, "pick another quality profile for the second copy than the first copy's")
	}
	if *secondRoot != "" {
		fs, err := s.Store.RootFolders(ctx, kind)
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(fs, func(f store.RootFolder) bool { return f.Path == *secondRoot }) {
			return false, errorf(http.StatusBadRequest, "the second copy's folder isn't one of the root folders")
		}
	}
	return *second != was || *secondRoot != wasRoot && !onDisk, nil
}

// seriesSeconds are the second copies a show has on disk.
func (s *Server) seriesSeconds(ctx context.Context, id int64) ([]store.File, error) {
	eps, err := s.Store.Episodes(ctx, id)
	if err != nil {
		return nil, err
	}
	var files []store.File
	for _, e := range eps {
		if e.Second != nil {
			files = append(files, *e.Second)
		}
	}
	return files, nil
}

// seriesSecondPlan is the plan for deleting all of a show's second copies.
func (s *Server) seriesSecondPlan(r *http.Request) (store.Series, []store.File, fileRemoval, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Series{}, nil, fileRemoval{}, err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return t, nil, fileRemoval{}, err
	}
	files, err := s.seriesSeconds(r.Context(), id)
	if err != nil {
		return t, nil, fileRemoval{}, err
	}
	root, folder := t.SecondFolder()
	fr := fileRemoval{Plan: library.FilesPlan(root, folder, files), Releases: []release{}, Watched: t.SecondProfileID != 0}
	if err := s.lockPlan(r.Context(), "series", t.ID, &fr.Plan); err != nil {
		return t, nil, fileRemoval{}, err
	}
	return t, files, fr, nil
}

// seriesSecondRemoval is GET /api/v1/series/{id}/second/removal: what
// deleting the show's second copies would take.
func (s *Server) seriesSecondRemoval(w http.ResponseWriter, r *http.Request) error {
	_, _, p, err := s.seriesSecondPlan(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

// deleteSeriesSecond is DELETE /api/v1/series/{id}/second: delete all of
// the show's second copies, as planned and shown (expectFiles,
// expectSize). unmonitor=true stops keeping second copies, so they aren't
// fetched again. The first copies are never touched.
func (s *Server) deleteSeriesSecond(w http.ResponseWriter, r *http.Request) error {
	t, files, p, err := s.seriesSecondPlan(r)
	if err != nil {
		return err
	}
	if err := s.carryOut(r, p.Plan, t.Title+" (second copies)"); err != nil {
		return err
	}
	ctx := r.Context()
	for _, f := range files {
		if err := s.Store.DeleteFile(ctx, f.ID); err != nil && err != store.ErrNotFound {
			return err
		}
	}
	if flag(r, "unmonitor") && t.SecondProfileID != 0 {
		t.SecondProfileID = 0
		if err := s.Store.UpdateSeries(ctx, t); err != nil {
			return err
		}
	}
	return s.series(w, r)
}

// seriesSecondKept is the folder a show's second copies are in when it's
// outside the show's folder, so removing the show with its folder leaves
// them on disk; "" when it has none there.
func (s *Server) seriesSecondKept(ctx context.Context, t store.Series) (string, error) {
	files, err := s.seriesSeconds(ctx, t.ID)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if !inFolder(t.Path, f.Path) {
			_, folder := t.SecondFolder()
			if !inFolder(folder, f.Path) {
				folder = filepath.Dir(f.Path)
			}
			return folder, nil
		}
	}
	return "", nil
}

// secondPlan is the plan for deleting a movie's second copy.
func (s *Server) secondPlan(r *http.Request) (store.Movie, fileRemoval, []store.Download, error) {
	m, err := s.movieByPath(r)
	if err != nil {
		return m, fileRemoval{}, nil, err
	}
	var files []store.File
	var imports []store.Download
	root, folder := m.SecondFolder()
	if m.Second != nil {
		files = append(files, *m.Second)
		// Where it is, should it have been put somewhere else since.
		if !inFolder(folder, m.Second.Path) {
			folder = filepath.Dir(m.Second.Path)
			if roots, err := s.Store.LibraryRoots(r.Context()); err == nil {
				for _, rt := range roots {
					if inFolder(rt, folder) {
						root = rt
					}
				}
			}
		}
		if d, err := s.Store.DownloadImportedTo(r.Context(), m.Second.Path); err == nil {
			imports = append(imports, d)
		}
	}
	fr := fileRemoval{Plan: library.FilesPlan(root, folder, files), Releases: releases(imports), Watched: m.SecondProfileID != 0}
	if err := s.lockPlan(r.Context(), "movie", m.ID, &fr.Plan); err != nil {
		return m, fileRemoval{}, nil, err
	}
	return m, fr, imports, nil
}

func inFolder(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// movieSecondRemoval is GET /api/v1/movies/{id}/second/removal: what
// deleting the movie's second copy would take.
func (s *Server) movieSecondRemoval(w http.ResponseWriter, r *http.Request) error {
	_, p, _, err := s.secondPlan(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

// deleteMovieSecond is DELETE /api/v1/movies/{id}/second: delete the
// movie's second copy, as planned and shown (expectFiles, expectSize).
// unmonitor=true stops keeping a second copy, so it isn't fetched again;
// block=true blocks the release it came from.
func (s *Server) deleteMovieSecond(w http.ResponseWriter, r *http.Request) error {
	m, p, imports, err := s.secondPlan(r)
	if err != nil {
		return err
	}
	if err := s.carryOut(r, p.Plan, m.Title+" (second copy)"); err != nil {
		return err
	}
	ctx := r.Context()
	if m.Second != nil {
		if err := s.Store.DeleteFile(ctx, m.Second.ID); err != nil && err != store.ErrNotFound {
			return err
		}
	}
	if flag(r, "block") {
		s.block(ctx, imports)
	}
	if flag(r, "unmonitor") && m.SecondProfileID != 0 {
		m.SecondProfileID = 0
		if err := s.Store.UpdateMovie(ctx, m); err != nil {
			return err
		}
	}
	return s.movie(w, r)
}

// secondKept is where a movie's second copy is when it's outside the
// movie's folder, so removing the movie with its folder leaves it on disk;
// "" when it has none there.
func secondKept(m store.Movie) string {
	if m.Second == nil || inFolder(m.Path, m.Second.Path) {
		return ""
	}
	return m.Second.Path
}
