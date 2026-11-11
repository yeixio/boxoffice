package api

import (
	"errors"
	"mime"
	"net/http"
	"path"

	"github.com/yeixio/boxoffice/internal/downloader"
)

// ---- the downloads folder's file browser ----

func (s *Server) downloadFileRoutes() []route {
	return []route{
		{"GET /api/v1/downloads/files", true, s.browseDownloads},
		{"GET /api/v1/downloads/files/get", true, s.getDownloadFile},
		{"POST /api/v1/downloads/files/delete", true, s.deleteDownloadFile},
		{"POST /api/v1/downloads/files/release", true, s.releaseDownloadFile},
		{"POST /api/v1/downloads/files/import", true, s.importDownloadFile},
	}
}

// fileError turns the file browser's refusals into a 400 with their
// message.
func fileError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, downloader.ErrOutside) {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return badRequest(err)
}

func (s *Server) browseDownloads(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Downloads.Browse(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		return fileError(err)
	}
	out := map[string]any{"root": s.Downloads.Dir, "listing": list}
	if d, ok := s.diskSpace(s.Downloads.Dir); ok {
		out["disk"] = d
	}
	if err := s.Downloads.FolderProblem(r.Context()); err != nil {
		out["problem"] = err.Error()
	}
	return writeJSON(w, http.StatusOK, out)
}

// getDownloadFile hands a file over to save. It's always an attachment of
// no particular type, sandboxed, so a browser never opens or runs it.
func (s *Server) getDownloadFile(w http.ResponseWriter, r *http.Request) error {
	f, info, err := s.Downloads.OpenFile(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		return fileError(err)
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(info.Name())}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
	return nil
}

type filePath struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	TitleID int64  `json:"titleId"`
}

func (s *Server) deleteDownloadFile(w http.ResponseWriter, r *http.Request) error {
	var in filePath
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Downloads.DeletePath(r.Context(), in.Path); err != nil {
		return fileError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) releaseDownloadFile(w http.ResponseWriter, r *http.Request) error {
	var in filePath
	if err := readJSON(r, &in); err != nil {
		return err
	}
	to, err := s.Downloads.Release(r.Context(), in.Path)
	if err != nil {
		return fileError(err)
	}
	return writeJSON(w, http.StatusOK, map[string]string{"path": to})
}

func (s *Server) importDownloadFile(w http.ResponseWriter, r *http.Request) error {
	var in filePath
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.TitleID <= 0 {
		return errorf(http.StatusBadRequest, "choose the movie or show it's for")
	}
	if err := s.Downloads.ImportPath(r.Context(), in.Path, in.Kind, in.TitleID); err != nil {
		return fileError(err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}
