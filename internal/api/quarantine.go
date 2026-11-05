package api

import (
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/downloader"
)

// ---- the quarantine folder ----

func (s *Server) quarantineRoutes() []route {
	return []route{
		{"GET /api/v1/downloads/quarantine", true, s.quarantine},
		{"POST /api/v1/downloads/quarantine/delete", true, s.quarantineBulk(func(r *http.Request, p string) error { return s.Downloads.DeletePath(r.Context(), p) })},
		{"POST /api/v1/downloads/quarantine/release", true, s.quarantineBulk(func(r *http.Request, p string) error {
			_, err := s.Downloads.Release(r.Context(), p)
			return err
		})},
	}
}

func (s *Server) quarantine(w http.ResponseWriter, r *http.Request) error {
	items, err := s.Downloads.Quarantine(r.Context())
	if err != nil {
		return err
	}
	set, err := downloader.LoadSettings(r.Context(), s.Store)
	if err != nil {
		return err
	}
	var size int64
	for _, it := range items {
		size += it.Size
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": items, "size": size, "days": set.QuarantineDays})
}

// quarantineBulk does one thing to several things in quarantine, each on
// its own, and says which couldn't and why. Only what's directly in the
// quarantine folder can be named.
func (s *Server) quarantineBulk(do func(*http.Request, string) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Paths []string `json:"paths"`
		}
		if err := readJSON(r, &in); err != nil {
			return err
		}
		if len(in.Paths) == 0 || len(in.Paths) > 1000 {
			return errorf(http.StatusBadRequest, "pick between 1 and 1000 things in quarantine")
		}
		type failure struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		failed := []failure{}
		for _, p := range in.Paths {
			rest, ok := strings.CutPrefix(p, downloader.QuarantineDir+"/")
			if !ok || rest == "" || strings.ContainsAny(rest, `/\`) {
				failed = append(failed, failure{p, "that isn't in quarantine"})
				continue
			}
			if err := do(r, p); err != nil {
				failed = append(failed, failure{p, err.Error()})
			}
		}
		return writeJSON(w, http.StatusOK, map[string]any{"done": len(in.Paths) - len(failed), "failed": failed})
	}
}
