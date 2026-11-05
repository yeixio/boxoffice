package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/store"
)

// Root folder scans: what's on disk in a root folder but not on the list,
// matched and ready to add. See package libscan.
func (s *Server) libScanRoutes() []route {
	return []route{
		{"POST /api/v1/rootfolders/{id}/scan", true, s.startLibScan},
		{"GET /api/v1/rootfolders/{id}/scan", true, s.libScan},
		{"DELETE /api/v1/rootfolders/{id}/scan", true, s.dismissLibScan},
		{"POST /api/v1/rootfolders/{id}/scan/add", true, s.addFromLibScan},
	}
}

func rootID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		return 0, errorf(http.StatusBadRequest, "that isn't a root folder")
	}
	return id, nil
}

func libScanError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errorf(http.StatusNotFound, "there's no such root folder, or it hasn't been scanned")
	case errors.Is(err, libscan.ErrBusy):
		return errorf(http.StatusConflict, err.Error())
	case errors.Is(err, music.ErrOff):
		return errorf(http.StatusConflict, err.Error())
	}
	return err
}

func (s *Server) startLibScan(w http.ResponseWriter, r *http.Request) error {
	id, err := rootID(r)
	if err != nil {
		return err
	}
	sc, err := s.LibScan.Start(r.Context(), id)
	if err != nil {
		return libScanError(err)
	}
	return writeJSON(w, http.StatusAccepted, sc)
}

// libScan is the root folder's scan, or null if it has none.
func (s *Server) libScan(w http.ResponseWriter, r *http.Request) error {
	id, err := rootID(r)
	if err != nil {
		return err
	}
	if sc, ok := s.LibScan.Get(id); ok {
		return writeJSON(w, http.StatusOK, sc)
	}
	return writeJSON(w, http.StatusOK, nil)
}

func (s *Server) dismissLibScan(w http.ResponseWriter, r *http.Request) error {
	id, err := rootID(r)
	if err != nil {
		return err
	}
	if err := s.LibScan.Dismiss(id); err != nil {
		return libScanError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) addFromLibScan(w http.ResponseWriter, r *http.Request) error {
	id, err := rootID(r)
	if err != nil {
		return err
	}
	var in libscan.AddRequest
	if err := readJSON(r, &in); err != nil {
		return err
	}
	sc, err := s.LibScan.Add(r.Context(), id, in)
	if err != nil {
		return libScanError(err)
	}
	return writeJSON(w, http.StatusAccepted, sc)
}
