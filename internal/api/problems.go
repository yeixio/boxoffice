package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/problems"
	"github.com/yeixio/boxoffice/internal/store"
)

// Problem files: what in the movie and TV library folders needs a hand,
// and the fixes for it. See package problems.
func (s *Server) problemRoutes() []route {
	return []route{
		{"GET /api/v1/problems", true, s.problemFiles},
		{"POST /api/v1/problems/look", true, s.lookForProblems},
		{"POST /api/v1/problems/fix", true, s.fixProblems},
		{"GET /api/v1/problems/fixes", true, s.problemFixes},
		{"POST /api/v1/problems/fixes/{id}/putback", true, s.putBackFix},
		{"POST /api/v1/problems/fixes/{id}/delete", true, s.deleteFixOriginals},
	}
}

type problemsResponse struct {
	Status problems.Status    `json:"status"`
	Report *problems.Report   `json:"report"`
	Fixes  []store.ProblemFix `json:"fixes"`
}

func (s *Server) problemFiles(w http.ResponseWriter, r *http.Request) error {
	st, rep := s.Problems.State()
	fixes, err := s.Store.ProblemFixes(r.Context(), 20)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, problemsResponse{st, rep, fixes})
}

func (s *Server) lookForProblems(w http.ResponseWriter, r *http.Request) error {
	s.Problems.Look(r.Context())
	return s.problemFiles(w, r)
}

func (s *Server) fixProblems(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs  []string          `json:"ids"`
		Keep map[string]string `json:"keep"`
		// As picks another way to fix a problem: keep_one for parts.
		As map[string]string `json:"as"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.IDs) == 0 {
		return errorf(http.StatusBadRequest, "pick at least one problem to fix")
	}
	if err := s.Problems.Fix(req.IDs, req.Keep, req.As); err != nil {
		if errors.Is(err, problems.ErrNotFound) {
			return errorf(http.StatusConflict, err.Error())
		}
		return errorf(http.StatusBadRequest, err.Error())
	}
	return s.problemFiles(w, r)
}

func (s *Server) problemFixes(w http.ResponseWriter, r *http.Request) error {
	fixes, err := s.Store.ProblemFixes(r.Context(), 20)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, fixes)
}

func fixID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errorf(http.StatusBadRequest, "that isn't a fix")
	}
	return id, nil
}

func fixError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errorf(http.StatusNotFound, "there's no such fix")
	}
	return errorf(http.StatusConflict, err.Error())
}

func (s *Server) putBackFix(w http.ResponseWriter, r *http.Request) error {
	id, err := fixID(r)
	if err != nil {
		return err
	}
	if err := problems.PutBack(r.Context(), s.Store, id); err != nil {
		return fixError(err)
	}
	s.Log.Info("problem files: put back", "fix", id)
	return s.problemFiles(w, r)
}

func (s *Server) deleteFixOriginals(w http.ResponseWriter, r *http.Request) error {
	id, err := fixID(r)
	if err != nil {
		return err
	}
	freed, err := problems.DeleteForGood(r.Context(), s.Store, id)
	if err != nil {
		return fixError(err)
	}
	if freed > 0 {
		s.Log.Info("problem files: deleted originals", "fix", id, "bytes", freed)
		s.Store.RecordJob(r.Context(), JobDeleteFiles, "Problem files", "done", "Deleted the originals of a fix, freeing "+library.Bytes(freed)+".")
	}
	return s.problemFiles(w, r)
}
