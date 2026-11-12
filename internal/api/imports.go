package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/importer"
)

// The Import page: files uploaded from a browser, or copied from a folder
// on the server, wait in the imports folder; Box Office works out what
// each is and files it in the library. See package importer.
func (s *Server) importRoutes() []route {
	return []route{
		{"GET /api/v1/import", true, s.importState},
		{"PUT /api/v1/import/upload", true, s.importUpload},
		{"POST /api/v1/import/copy", true, s.importCopy},
		{"GET /api/v1/import/browse", true, s.importBrowse},
		{"PATCH /api/v1/import/item", true, s.importSet},
		{"DELETE /api/v1/import/item", true, s.importRemove},
		{"POST /api/v1/import/run", true, s.importRun},
		{"DELETE /api/v1/import/done", true, s.importClear},
	}
}

func (s *Server) imports() (*importer.Service, error) {
	if s.Imports == nil {
		return nil, errorf(http.StatusConflict, "importing isn't set up")
	}
	return s.Imports, nil
}

func importErr(err error) error {
	if errors.Is(err, importer.ErrBusy) {
		return errorf(http.StatusConflict, err.Error())
	}
	return err
}

func (s *Server) importState(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	st, err := im.State(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

// importUpload takes one file as the request body; name says what it's
// called, with the folders it was in when a folder was sent.
func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("X-Requested-With") == "" && user(r).ID != 0 {
		return errorf(http.StatusForbidden, "send the file from Box Office's own page")
	}
	im, err := s.imports()
	if err != nil {
		return err
	}
	if err := im.Upload(r.Context(), r.URL.Query().Get("name"), r.ContentLength, r.Body); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) importCopy(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if err := im.CopyIn(r.Context(), body.Paths); err != nil {
		return importErr(err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

type importEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"`
}

// importBrowse lists a folder's folders and files, for picking what to
// copy in. With no path it starts at /media when that's there.
func (s *Server) importBrowse(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
		if st, err := os.Stat("/media"); err == nil && st.IsDir() {
			p = "/media"
		}
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return errorf(http.StatusBadRequest, "give a full path")
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return errorf(http.StatusNotFound, "can't open "+p)
	}
	list := []importEntry{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		st, err := os.Stat(filepath.Join(p, e.Name()))
		if err != nil {
			continue
		}
		switch {
		case st.IsDir():
			list = append(list, importEntry{Name: e.Name(), Dir: true})
		case st.Mode().IsRegular():
			list = append(list, importEntry{Name: e.Name(), Size: st.Size()})
		}
	}
	slices.SortFunc(list, func(a, b importEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	return writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "entries": list})
}

func (s *Server) importSet(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	var body struct {
		importer.Pick
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	it, err := im.Set(r.Context(), body.Path, body.Pick)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, it)
}

func (s *Server) importRemove(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	if err := im.Remove(r.Context(), r.URL.Query().Get("path")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) importRun(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if err := im.Run(r.Context(), body.Paths); err != nil {
		return importErr(err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (s *Server) importClear(w http.ResponseWriter, r *http.Request) error {
	im, err := s.imports()
	if err != nil {
		return err
	}
	im.Clear()
	w.WriteHeader(http.StatusNoContent)
	return nil
}
