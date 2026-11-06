package api

import (
	"net/http"

	"github.com/yeixio/boxoffice/internal/qualitycheck"
	"github.com/yeixio/boxoffice/internal/store"
)

// Quality check: what's inside movie and episode files, and the qualities
// it corrected. See package qualitycheck.
func (s *Server) qualityCheckRoutes() []route {
	return []route{
		{"GET /api/v1/quality-check", true, s.qualityCheck},
		{"POST /api/v1/quality-check/again", true, s.qualityCheckAgain},
	}
}

type qualityCheckResponse struct {
	Status      qualitycheck.Status `json:"status"`
	Corrections []store.Correction  `json:"corrections"`
	// More is how many corrections there are beyond those listed.
	More int `json:"more"`
}

// maxCorrections is how many corrections the page lists.
const maxCorrections = 200

func (s *Server) qualityCheck(w http.ResponseWriter, r *http.Request) error {
	if s.QualityCheck == nil {
		return errorf(http.StatusNotFound, "the quality check isn't running")
	}
	st, err := s.QualityCheck.State(r.Context())
	if err != nil {
		return err
	}
	fixes, err := s.Store.Corrections(r.Context())
	if err != nil {
		return err
	}
	more := 0
	if len(fixes) > maxCorrections {
		more = len(fixes) - maxCorrections
		fixes = fixes[:maxCorrections]
	}
	return writeJSON(w, http.StatusOK, qualityCheckResponse{st, fixes, more})
}

// qualityCheckAgain looks inside every file again, from the start.
func (s *Server) qualityCheckAgain(w http.ResponseWriter, r *http.Request) error {
	if s.QualityCheck == nil {
		return errorf(http.StatusNotFound, "the quality check isn't running")
	}
	if err := s.Store.CheckAgain(r.Context()); err != nil {
		return err
	}
	s.QualityCheck.Wake()
	return s.qualityCheck(w, r)
}
