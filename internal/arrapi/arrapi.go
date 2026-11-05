// Package arrapi answers the parts of Radarr's and Sonarr's v3 APIs that
// Matinee, and tools like it, call: status, quality profiles, root folders,
// lookup, add, edit, the calendar, searches and replacing a file. Box Office
// serves it under /radarr and /sonarr, so anything that talks to those apps
// can talk to Box Office by changing only the address and the API key.
//
// It translates; the wanted list itself is the catalog's.
package arrapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// App is which app's API a handler speaks.
type App string

const (
	Radarr App = "radarr"
	Sonarr App = "sonarr"
)

func (a App) name() string {
	if a == Radarr {
		return "Radarr"
	}
	return "Sonarr"
}

// Handler serves one app's API.
type Handler struct {
	App     App
	Catalog *catalog.Service
	Log     *slog.Logger
	// Key returns the API key requests must carry.
	Key     func(ctx context.Context) string
	Version string
	mux     *http.ServeMux
}

// New makes the handler for an app.
func New(app App, c *catalog.Service, key func(context.Context) string, version string, log *slog.Logger) *Handler {
	h := &Handler{App: app, Catalog: c, Key: key, Version: version, Log: log, mux: http.NewServeMux()}
	h.route("GET /api/v3/system/status", h.status)
	h.route("GET /api/v3/qualityprofile", h.profiles)
	h.route("GET /api/v3/qualityprofile/{id}", h.profile)
	h.route("GET /api/v3/rootfolder", h.rootFolders)
	h.route("GET /api/v3/calendar", h.calendar)
	h.route("POST /api/v3/command", h.command)
	h.route("GET /api/v3/boxoffice/notifications", h.notifications)
	h.route("GET /api/v3/tag", func(w http.ResponseWriter, r *http.Request) error { return write(w, http.StatusOK, []any{}) })
	if app == Radarr {
		h.route("GET /api/v3/movie", h.movies)
		h.route("POST /api/v3/movie", h.addMovie)
		h.route("GET /api/v3/movie/lookup", h.lookupMovies)
		h.route("GET /api/v3/movie/lookup/tmdb", h.lookupMovieTMDB)
		h.route("GET /api/v3/movie/lookup/imdb", h.lookupMovieIMDB)
		h.route("GET /api/v3/movie/{id}", h.movie)
		h.route("PUT /api/v3/movie/{id}", h.editMovie)
		h.route("DELETE /api/v3/movie/{id}", h.deleteMovie)
		h.route("DELETE /api/v3/moviefile/{id}", h.deleteFile)
	} else {
		h.route("GET /api/v3/series", h.seriesList)
		h.route("POST /api/v3/series", h.addSeries)
		h.route("GET /api/v3/series/lookup", h.lookupSeries)
		h.route("GET /api/v3/series/{id}", h.series)
		h.route("PUT /api/v3/series/{id}", h.editSeries)
		h.route("DELETE /api/v3/series/{id}", h.deleteSeries)
		h.route("GET /api/v3/episode", h.episodes)
		h.route("GET /api/v3/episode/{id}", h.episode)
		h.route("DELETE /api/v3/episodefile/{id}", h.deleteFile)
		// Sonarr v3 had language profiles; v4, like Box Office, has none.
		h.route("GET /api/v3/languageprofile", func(w http.ResponseWriter, r *http.Request) error { return write(w, http.StatusOK, []any{}) })
	}
	return h
}

// apiError is an answer other than 200, in the shape the apps use.
type apiError struct {
	status int
	body   any
}

func (e *apiError) Error() string { return fmt.Sprint(e.body) }

func fail(status int, msg string) error {
	return &apiError{status, map[string]string{"message": msg}}
}

// invalid is a validation failure, which the apps answer as a list.
func invalid(property, msg string) error {
	return &apiError{http.StatusBadRequest, []map[string]string{{"propertyName": property, "errorMessage": msg}}}
}

var errNotFound = fail(http.StatusNotFound, "NotFound")

func (h *Handler) route(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
	h.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		var ce *catalog.Error
		switch {
		case errors.As(err, &ae):
			write(w, ae.status, ae.body)
		case errors.Is(err, store.ErrNotFound):
			write(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
		case errors.As(err, &ce):
			write(w, http.StatusBadRequest, []map[string]string{{"errorMessage": ce.Message}})
		case errors.Is(err, tmdb.ErrNoKey):
			write(w, http.StatusServiceUnavailable, map[string]string{"message": err.Error()})
		default:
			h.Log.Warn("arr API request failed", "app", h.App, "path", r.URL.Path, "err", err)
			write(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		}
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		key = r.URL.Query().Get("apikey")
	}
	if want := h.Key(r.Context()); want == "" || !equal(key, want) {
		write(w, http.StatusUnauthorized, map[string]string{"message": "Unauthorized"})
		return
	}
	h.mux.ServeHTTP(w, r)
}

func write(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func read(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v); err != nil {
		return fail(http.StatusBadRequest, "the request body isn't valid JSON")
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errNotFound
	}
	return id, nil
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	version := "5.0.0.0"
	if h.App == Sonarr {
		version = "4.0.0.0"
	}
	id, err := h.Catalog.Store.InstanceID(r.Context())
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, map[string]any{
		"appName": h.App.name(), "instanceName": "Box Office", "version": version,
		"boxOffice": h.Version, "boxOfficeId": id, "boxOfficeFeatures": []string{"notifications"}, "urlBase": "/" + string(h.App), "isProduction": true,
		"startTime": started.Format(time.RFC3339), "osName": "linux", "runtimeName": "go", "authentication": "external",
	})
}

var started = time.Now().UTC()

// ---- quality profiles ----

// ids translates quality profile ids between Box Office and an app's API:
// profiles imported from Radarr or Sonarr keep the ids they had there, so
// Matinee's saved choice of profile still means the same profile.
type ids struct {
	in  map[int64]int64 // the app's id -> Box Office's
	out map[int64]int64 // Box Office's -> the app's
}

// offset moves Box Office's own ids clear of the imported ones.
const offset = 1000

func (h *Handler) profileIDs(ctx context.Context) (ids, error) {
	legacy, err := h.Catalog.Store.LegacyIDs(ctx, string(h.App), "profile")
	if err != nil {
		return ids{}, err
	}
	m := ids{in: legacy, out: map[int64]int64{}}
	for l, local := range legacy {
		m.out[local] = l
	}
	return m, nil
}

func (m ids) toApp(local int) int {
	if l, ok := m.out[int64(local)]; ok {
		return int(l)
	}
	if _, clash := m.in[int64(local)]; clash || local >= offset {
		return local + offset
	}
	return local
}

func (m ids) fromApp(id int) int {
	if local, ok := m.in[int64(id)]; ok {
		return int(local)
	}
	if id > offset {
		return id - offset
	}
	return id
}

func (h *Handler) profileJSON(p quality.Profile, m ids) map[string]any {
	items := []map[string]any{}
	for _, q := range quality.All {
		if q.ID == 0 {
			continue
		}
		items = append(items, map[string]any{
			"quality": map[string]any{"id": q.ID, "name": q.Name, "source": q.Source, "resolution": q.Resolution},
			"items":   []any{}, "allowed": p.Accepts(q.ID),
		})
	}
	return map[string]any{"id": m.toApp(p.ID), "name": p.Name, "upgradeAllowed": p.UpgradeOK, "cutoff": p.Cutoff, "items": items}
}

func (h *Handler) profiles(w http.ResponseWriter, r *http.Request) error {
	ps, err := h.Catalog.Store.Profiles(r.Context())
	if err != nil {
		return err
	}
	m, err := h.profileIDs(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, h.profileJSON(p, m))
	}
	return write(w, http.StatusOK, out)
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	m, err := h.profileIDs(r.Context())
	if err != nil {
		return err
	}
	p, err := h.Catalog.Store.Profile(r.Context(), m.fromApp(int(id)))
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, h.profileJSON(p, m))
}

// ---- root folders ----

func (h *Handler) kind() string {
	if h.App == Radarr {
		return "movie"
	}
	return "series"
}

func (h *Handler) rootFolders(w http.ResponseWriter, r *http.Request) error {
	fs, err := h.Catalog.Store.RootFolders(r.Context(), h.kind())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		_, statErr := os.Stat(f.Path)
		out = append(out, map[string]any{"id": f.ID, "path": f.Path, "accessible": statErr == nil, "freeSpace": FreeSpace(f.Path), "unmappedFolders": []any{}})
	}
	return write(w, http.StatusOK, out)
}

// ---- commands ----

var commandID atomic.Int64

func (h *Handler) command(w http.ResponseWriter, r *http.Request) error {
	var c struct {
		Name       string  `json:"name"`
		MovieIDs   []int64 `json:"movieIds"`
		SeriesID   int64   `json:"seriesId"`
		EpisodeIDs []int64 `json:"episodeIds"`
	}
	if err := read(r, &c); err != nil {
		return err
	}
	ctx := r.Context()
	var err error
	switch strings.ToLower(c.Name) {
	case "moviessearch":
		err = h.Catalog.SearchMovies(ctx, c.MovieIDs...)
	case "refreshmovie":
		for _, id := range c.MovieIDs {
			if m, e := h.Catalog.Store.Movie(ctx, id); e == nil {
				h.Catalog.Jobs.Queue(ctx, catalog.JobRefreshMovie, m.Title, fmt.Sprintf(`{"id":%d}`, id))
			}
		}
	case "seriessearch":
		err = h.Catalog.SearchSeries(ctx, c.SeriesID)
	case "episodesearch":
		err = h.Catalog.SearchEpisodes(ctx, c.EpisodeIDs...)
	case "refreshseries":
		if t, e := h.Catalog.Store.Series(ctx, c.SeriesID); e == nil {
			h.Catalog.Jobs.Queue(ctx, catalog.JobRefreshSeries, t.Title, fmt.Sprintf(`{"id":%d}`, c.SeriesID))
		}
	default:
		return fail(http.StatusBadRequest, "Box Office doesn't run the command "+c.Name)
	}
	if err != nil {
		return err
	}
	return write(w, http.StatusCreated, map[string]any{"id": commandID.Add(1), "name": c.Name, "status": "queued", "queued": time.Now().UTC().Format(time.RFC3339)})
}

// ---- files ----

// deleteFile takes a bad copy away so another can replace it. The file
// isn't destroyed: it moves to a .recycle folder in its root folder, on the
// same disk, where an admin can get it back.
func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	f, err := h.Catalog.Store.FileByID(ctx, id)
	if err != nil {
		return err
	}
	want := "movie"
	if h.App == Sonarr {
		want = "episode"
	}
	if f.Kind != want {
		return errNotFound
	}
	root, err := h.fileRoot(ctx, f)
	if err != nil {
		return err
	}
	if err := Recycle(root, f.Path); err != nil {
		return fail(http.StatusInternalServerError, "couldn't move the file aside: "+err.Error())
	}
	if err := h.Catalog.Store.DeleteFile(ctx, id); err != nil {
		return err
	}
	h.Log.Info("file moved to the recycle folder", "path", f.Path)
	w.WriteHeader(http.StatusOK)
	return nil
}

func (h *Handler) fileRoot(ctx context.Context, f store.File) (string, error) {
	if f.Kind == "movie" {
		m, err := h.Catalog.Store.Movie(ctx, f.OwnerID)
		return m.RootFolder, err
	}
	e, err := h.Catalog.Store.Episode(ctx, f.OwnerID)
	if err != nil {
		return "", err
	}
	t, err := h.Catalog.Store.Series(ctx, e.SeriesID)
	return t.RootFolder, err
}

// Recycle moves a file into root's .recycle folder. A file that's already
// gone is fine.
func Recycle(root, path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	dir := filepath.Join(root, ".recycle", time.Now().UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, filepath.Base(path))
	for i := 1; ; i++ {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		ext := filepath.Ext(path)
		dest = filepath.Join(dir, fmt.Sprintf("%s.%d%s", strings.TrimSuffix(filepath.Base(path), ext), i, ext))
	}
	return os.Rename(path, dest)
}

func equal(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
