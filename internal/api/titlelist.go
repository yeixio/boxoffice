package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yeixio/boxoffice/internal/titlelist"
)

// The list of titles as a file (Settings → Library → Titles as a file):
// saved as JSON or CSV, and loaded into another Box Office, which adds
// what it's missing (see internal/titlelist).
func (s *Server) titleListRoutes() []route {
	return []route{
		{"GET /api/v1/titles/export", true, s.exportTitles},
		{"POST /api/v1/titles/import", true, s.importTitles},
	}
}

// titleAdder is what adds a loaded file's titles: the one main.go made, or
// one made here.
func (s *Server) titleAdder() *titlelist.Service {
	s.titlesOnce.Do(func() {
		if s.Titles == nil {
			s.Titles = &titlelist.Service{Store: s.Store, Catalog: s.Catalog, Music: s.Music, Books: s.Books, Jobs: s.Catalog.Jobs}
			s.Titles.Register()
		}
	})
	return s.Titles
}

func (s *Server) exportTitles(w http.ResponseWriter, r *http.Request) error {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		return errorf(http.StatusBadRequest, "the list is saved as json or csv")
	}
	f, err := titlelist.Export(r.Context(), s.Store)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("boxoffice-titles-%s.%s", time.Now().Format("2006-01-02"), format)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		return f.WriteCSV(w)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	return f.WriteJSON(w)
}

type titleImportView struct {
	titlelist.Plan
	// Total is how many titles the file holds.
	Total int `json:"total"`
	// CantAdd is how many can't be added; Skipped lists the first of them.
	CantAdd int `json:"cantAdd"`
	// Queued says the new ones were lined up to be added; otherwise this
	// was only a look.
	Queued bool `json:"queued"`
}

// importTitles takes the file itself as the request's body. With ?look=1
// it only says what loading it would do; with ?search=1 each title is
// searched for once it's added.
func (s *Server) importTitles(w http.ResponseWriter, r *http.Request) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, titlelist.MaxBytes))
	if err != nil {
		return errorf(http.StatusRequestEntityTooLarge, "the file is too big to be a list of titles")
	}
	f, err := titlelist.Read(data)
	var bad *titlelist.Error
	if errors.As(err, &bad) {
		return errorf(http.StatusBadRequest, bad.Message)
	}
	if err != nil {
		return err
	}
	plan, err := titlelist.MakePlan(r.Context(), s.Store, f)
	if err != nil {
		return err
	}
	out := titleImportView{Plan: plan, Total: len(f.Titles), CantAdd: len(plan.Skipped)}
	if r.URL.Query().Get("look") == "" && len(plan.New) > 0 {
		if s.Catalog == nil || s.Catalog.Jobs == nil {
			return errorf(http.StatusServiceUnavailable, "Box Office isn't ready to add titles yet")
		}
		if err := s.titleAdder().Queue(r.Context(), plan, r.URL.Query().Get("search") != ""); err != nil {
			return err
		}
		out.Queued = true
		s.Log.Info("titles loaded from a file", "new", len(plan.New), "already here", plan.Have, "skipped", len(plan.Skipped))
	}
	// A long list of what can't be added is cut for the screen.
	if len(out.Skipped) > 200 {
		out.Skipped = out.Skipped[:200]
	}
	return writeJSON(w, http.StatusOK, out)
}
