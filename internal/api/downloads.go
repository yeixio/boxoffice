package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
)

// ---- downloads ----

func (s *Server) downloadRoutes() []route {
	act := func(fn func(*http.Request, int64) error) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			id, err := pathID(r, "id")
			if err != nil {
				return err
			}
			if err := fn(r, id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}
	}
	return []route{
		{"GET /api/v1/downloads", true, s.downloads},
		{"POST /api/v1/downloads", true, s.addNZB},
		{"PUT /api/v1/downloads/settings", true, s.saveDownloadSettings},
		{"DELETE /api/v1/downloads/history", true, func(w http.ResponseWriter, r *http.Request) error {
			if err := s.Downloads.ClearHistory(r.Context()); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}},
		{"POST /api/v1/downloads/{id}/pause", true, act(func(r *http.Request, id int64) error { return badRequest(s.Downloads.Pause(r.Context(), id)) })},
		{"POST /api/v1/downloads/{id}/resume", true, act(func(r *http.Request, id int64) error { return s.Downloads.Resume(r.Context(), id) })},
		{"POST /api/v1/downloads/{id}/retry", true, act(func(r *http.Request, id int64) error { return badRequest(s.Downloads.Retry(r.Context(), id)) })},
		{"POST /api/v1/downloads/{id}/import", true, act(func(r *http.Request, id int64) error { return badRequest(s.Downloads.Import(r.Context(), id)) })},
		{"POST /api/v1/downloads/{id}/reprocess", true, act(func(r *http.Request, id int64) error { return badRequest(s.Downloads.Reprocess(r.Context(), id)) })},
		{"POST /api/v1/downloads/{id}/top", true, act(func(r *http.Request, id int64) error { return s.Downloads.MoveToTop(r.Context(), id) })},
		{"DELETE /api/v1/downloads/{id}", true, act(func(r *http.Request, id int64) error {
			return s.Downloads.Remove(r.Context(), id, r.URL.Query().Get("deleteFiles") == "true")
		})},

		{"GET /api/v1/movies/{id}/releases", true, s.releases(func(r *http.Request, id int64) (search.Results, error) { return s.Search.Movie(r.Context(), id) })},
		{"GET /api/v1/episodes/{id}/releases", true, s.releases(func(r *http.Request, id int64) (search.Results, error) { return s.Search.Episode(r.Context(), id) })},
		{"POST /api/v1/releases/{key}/grab", true, s.grabRelease},

		{"GET /api/v1/search/auto", true, s.autoSearch},
		{"PUT /api/v1/search/auto", true, s.saveAutoSearch},
		{"POST /api/v1/search/auto/run", true, s.runAutoSearch},
	}
}

// badRequest turns a plain error (a download can't be paused now, say)
// into a 400 with its message; not found stays not found.
func badRequest(err error) error {
	if err == nil || errors.Is(err, store.ErrNotFound) {
		return err
	}
	return errorf(http.StatusBadRequest, err.Error())
}

func (s *Server) downloads(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	set, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil {
		return err
	}
	queue, err := s.Store.DownloadQueue(ctx)
	if err != nil {
		return err
	}
	history, err := s.Store.DownloadHistory(ctx, 100)
	if err != nil {
		return err
	}
	for i, d := range history {
		history[i].CanReprocess = s.Downloads.CanReprocess(d)
		history[i].CanImport = s.Downloads.CanImport(d)
	}
	live := s.Downloads.Live()
	for i, d := range queue {
		if p, ok := live.Progress[d.ID]; ok {
			queue[i].DoneBytes, queue[i].FailedBytes = p.DoneBytes, p.FailedBytes
			if p.Size > 0 {
				queue[i].Size = p.Size
			}
			if p.Stage != "" {
				queue[i].Message = p.Stage
			}
			queue[i].StepDone, queue[i].StepTotal = p.StepDone, p.StepTotal
		}
	}
	servers, err := s.Store.UsenetServers(ctx)
	if err != nil {
		return err
	}
	enabled := 0
	for _, u := range servers {
		if u.Enabled {
			enabled++
		}
	}
	folder := map[string]any{"path": s.Downloads.Dir, "freeSpace": arrapi.FreeSpace(s.Downloads.Dir)}
	if err := s.Downloads.CheckFolder(ctx); err != nil {
		folder["problem"] = err.Error()
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"settings": set, "queue": queue, "history": history, "speed": live.Speed, "servers": live.Servers,
		"newsServers": enabled, "folder": folder,
	})
}

func (s *Server) saveDownloadSettings(w http.ResponseWriter, r *http.Request) error {
	var in downloader.Settings
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if in.SpeedLimit < 0 {
		in.SpeedLimit = 0
	}
	cur, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil {
		return err
	}
	if in.Enabled && !cur.Enabled {
		if err := s.Downloads.CheckFolder(ctx); err != nil {
			return errorf(http.StatusBadRequest, err.Error())
		}
		servers, err := s.Store.UsenetServers(ctx)
		if err != nil {
			return err
		}
		if !hasEnabled(servers) {
			return errorf(http.StatusBadRequest, "add a news server first (Settings, News servers): downloading needs one to download from")
		}
	}
	if err := downloader.SaveSettings(ctx, s.Store, in); err != nil {
		return err
	}
	s.Downloads.Wake()
	return s.downloads(w, r)
}

func hasEnabled(list []store.UsenetServer) bool {
	for _, u := range list {
		if u.Enabled {
			return true
		}
	}
	return false
}

// addNZB queues an uploaded NZB file.
func (s *Server) addNZB(w http.ResponseWriter, r *http.Request) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("name")), ".nzb")
	d, err := s.Downloads.Add(r.Context(), store.Download{Name: name, Source: "upload"}, data)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusCreated, d)
}

func (s *Server) releases(fn func(*http.Request, int64) (search.Results, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		res, err := fn(r, id)
		if errors.Is(err, search.ErrNoIndexers) {
			return errorf(http.StatusBadRequest, err.Error())
		}
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, res)
	}
}

func (s *Server) grabRelease(w http.ResponseWriter, r *http.Request) error {
	d, err := s.Search.Grab(r.Context(), r.PathValue("key"))
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusCreated, d)
}
