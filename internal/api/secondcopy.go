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

// editSecond takes the second copy asked for in an edit, and says whether
// it should be looked for now. Turning it off leaves the copy on disk and
// on the movie's page; deleting it is DELETE /movies/{id}/second.
func (s *Server) editSecond(ctx context.Context, m *store.Movie, in editInput) (look bool, err error) {
	was, wasRoot := m.SecondProfileID, m.SecondRoot
	if in.SecondProfileID != nil {
		m.SecondProfileID = *in.SecondProfileID
	}
	if in.SecondRoot != nil {
		m.SecondRoot = *in.SecondRoot
	}
	if m.SecondRoot != "" && filepath.Clean(m.SecondRoot) == filepath.Clean(m.RootFolder) {
		m.SecondRoot = ""
	}
	if m.SecondProfileID == 0 {
		return false, nil
	}
	if m.SecondProfileID == was && m.SecondRoot == wasRoot && in.ProfileID == nil {
		return false, nil
	}
	p, err := s.Store.Profile(ctx, m.SecondProfileID)
	if err != nil {
		return false, errorf(http.StatusBadRequest, "there's no such quality profile for the second copy")
	}
	if p.Locked {
		return false, errorf(http.StatusBadRequest, "a second copy needs a profile that takes downloads, not Do not touch")
	}
	if m.SecondProfileID == m.ProfileID {
		return false, errorf(http.StatusBadRequest, "pick another quality profile for the second copy than the first copy's")
	}
	if m.SecondRoot != "" {
		fs, err := s.Store.RootFolders(ctx, "movie")
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(fs, func(f store.RootFolder) bool { return f.Path == m.SecondRoot }) {
			return false, errorf(http.StatusBadRequest, "the second copy's folder isn't one of the movie root folders")
		}
	}
	if m.Second != nil && m.SecondRoot != wasRoot {
		return false, errorf(http.StatusConflict, "the second copy is on disk already; delete it first to keep it somewhere else")
	}
	return m.Second == nil && (m.SecondProfileID != was || m.SecondRoot != wasRoot), nil
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
