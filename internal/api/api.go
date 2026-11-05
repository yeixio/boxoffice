// Package api is Box Office's own HTTP API, which its web UI uses, plus the
// health checks and the web UI itself. The Radarr- and Sonarr-compatible
// APIs are mounted next to it, under /radarr and /sonarr.
//
// Everything under /api/v1 needs an admin: a browser signed in with a
// session cookie, or any client with the API key in X-Api-Key.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/migrate"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// SettingAPIKey is where the API key is kept.
const SettingAPIKey = "api_key"

const (
	cookieName = "boxoffice_session"
	sessionTTL = 30 * 24 * time.Hour
)

// Server is the whole HTTP side of Box Office.
type Server struct {
	Store   *store.Store
	Catalog *catalog.Service
	// Music is the music list, used while music is turned on.
	Music *music.Service
	// Books is the list of books, for ebooks and audiobooks.
	Books    *books.Service
	Backups  *backup.Manager
	Migrator *migrate.Migrator
	// Downloads is the downloader, and Search finds releases for it.
	Downloads *downloader.Engine
	Search    *search.Service
	// LibScan scans root folders for what's on disk but not on the list.
	LibScan *libscan.Service
	// Portal is the request site, for the people who ask for titles.
	Portal   *portal.Service
	Log      *slog.Logger
	Version  string
	Database string // sqlite or postgres, for the System page
	DataDir  string
	// EnvAPIKey, when set, is the API key, and can't be changed in the UI:
	// it comes from the deployment, such as a Kubernetes Secret.
	EnvAPIKey string
	// Web is the built web UI; nil serves a short page saying so.
	Web fs.FS
	// Monitoring is the Health page's server section and the alerts (see
	// monitor.go); nil makes one with no log lines.
	Monitoring *Monitoring

	started     time.Time
	failures    sync.Map // username -> *failure
	healthCount healthCount
}

type failure struct {
	mu    sync.Mutex
	count int
	until time.Time
}

// APIKey is the key the API and the Radarr and Sonarr APIs take. One is
// made on first start.
func (s *Server) APIKey(ctx context.Context) string {
	if s.EnvAPIKey != "" {
		return s.EnvAPIKey
	}
	k, _ := s.Store.Setting(ctx, SettingAPIKey)
	return k
}

// EnsureAPIKey makes the API key if there isn't one.
func (s *Server) EnsureAPIKey(ctx context.Context) error {
	if s.APIKey(ctx) != "" {
		return nil
	}
	return s.Store.SetSetting(ctx, SettingAPIKey, store.NewToken())
}

type route struct {
	pattern string
	auth    bool
	fn      func(http.ResponseWriter, *http.Request) error
}

// Handler is every route.
func (s *Server) Handler() http.Handler {
	s.started = time.Now()
	mux := http.NewServeMux()
	routes := []route{
		{"GET /api/v1/status", false, s.status},
		{"POST /api/v1/setup", false, s.setup},
		{"POST /api/v1/login", false, s.login},
		{"POST /api/v1/logout", false, s.logout},
		{"PUT /api/v1/me/password", true, s.changePassword},

		{"GET /api/v1/system", true, s.system},
		{"GET /api/v1/settings", true, s.settings},
		{"PUT /api/v1/settings", true, s.saveSettings},
		{"POST /api/v1/settings/apikey", true, s.newAPIKey},
		{"GET /api/v1/qualities", true, s.qualities},
		{"GET /api/v1/profiles", true, s.profiles},
		{"POST /api/v1/profiles", true, s.createProfile},
		{"PUT /api/v1/profiles/{id}", true, s.updateProfile},
		{"DELETE /api/v1/profiles/{id}", true, s.deleteProfile},
		{"GET /api/v1/rootfolders", true, s.rootFolders},
		{"POST /api/v1/rootfolders", true, s.addRootFolder},
		{"DELETE /api/v1/rootfolders/{id}", true, s.deleteRootFolder},
		{"GET /api/v1/fs", true, s.browse},

		{"GET /api/v1/lookup/artists", true, s.lookupArtists},
		{"GET /api/v1/artists", true, s.artists},
		{"POST /api/v1/artists", true, s.addArtist},
		{"GET /api/v1/artists/albumtypes", true, s.albumTypes},
		{"GET /api/v1/artists/{id}", true, s.artist},
		{"PATCH /api/v1/artists/{id}", true, s.editArtist},
		{"DELETE /api/v1/artists/{id}", true, s.deleteArtist},
		{"POST /api/v1/artists/{id}/refresh", true, s.refreshArtist},
		{"PATCH /api/v1/albums/{id}", true, s.editAlbum},
		{"GET /api/v1/lookup/authors", true, s.lookupAuthors},
		{"GET /api/v1/authors", true, s.authors},
		{"POST /api/v1/authors", true, s.addAuthor},
		{"GET /api/v1/authors/{id}", true, s.author},
		{"PATCH /api/v1/authors/{id}", true, s.editAuthor},
		{"DELETE /api/v1/authors/{id}", true, s.deleteAuthor},
		{"POST /api/v1/authors/{id}/refresh", true, s.refreshAuthor},
		{"PUT /api/v1/authors/{id}/wanted", true, s.wantAllBooks},
		{"PUT /api/v1/books/{id}/wanted", true, s.wantBook},
		{"GET /api/v1/books", true, s.allBooks},
		{"GET /api/v1/books/{id}", true, s.book},
		{"GET /api/v1/books/{id}/releases", true, s.bookReleases},
		{"POST /api/v1/books/{id}/search", true, s.searchBook},

		{"GET /api/v1/lookup/movies", true, s.lookupMovies},
		{"GET /api/v1/lookup/series", true, s.lookupSeries},
		{"GET /api/v1/movies", true, s.movies},
		{"POST /api/v1/movies", true, s.addMovie},
		{"GET /api/v1/movies/{id}", true, s.movie},
		{"POST /api/v1/movies/bulk", true, s.bulkMovies},
		{"PATCH /api/v1/movies/{id}", true, s.editMovie},
		{"DELETE /api/v1/movies/{id}", true, s.deleteMovie},
		{"GET /api/v1/movies/{id}/removal", true, s.movieRemoval},
		{"GET /api/v1/movies/{id}/file/removal", true, s.movieFileRemoval},
		{"DELETE /api/v1/movies/{id}/file", true, s.deleteMovieFile},
		{"POST /api/v1/movies/{id}/search", true, s.searchMovie},
		{"POST /api/v1/movies/{id}/refresh", true, s.refreshMovie},
		{"GET /api/v1/series", true, s.seriesList},
		{"POST /api/v1/series", true, s.addSeries},
		{"GET /api/v1/series/defaults", true, s.showDefaults},
		{"PUT /api/v1/series/defaults", true, s.saveShowDefaults},
		{"GET /api/v1/series/{id}", true, s.series},
		{"POST /api/v1/series/bulk", true, s.bulkSeries},
		{"PATCH /api/v1/series/{id}", true, s.editSeries},
		{"DELETE /api/v1/series/{id}", true, s.deleteSeries},
		{"GET /api/v1/series/{id}/removal", true, s.seriesRemoval},
		{"GET /api/v1/series/{id}/seasons/{number}/files/removal", true, s.seasonFilesRemoval},
		{"DELETE /api/v1/series/{id}/seasons/{number}/files", true, s.deleteSeasonFiles},
		{"POST /api/v1/series/{id}/search", true, s.searchSeries},
		{"POST /api/v1/series/{id}/refresh", true, s.refreshSeries},
		{"PATCH /api/v1/series/{id}/seasons/{number}", true, s.editSeason},
		{"PATCH /api/v1/episodes/{id}", true, s.editEpisode},
		{"POST /api/v1/episodes/{id}/search", true, s.searchEpisode},
		{"GET /api/v1/episodes/{id}/file/removal", true, s.episodeFileRemoval},
		{"DELETE /api/v1/episodes/{id}/file", true, s.deleteEpisodeFile},
		{"GET /api/v1/recycle", true, s.recycle},
		{"PUT /api/v1/recycle", true, s.editRecycle},
		{"POST /api/v1/recycle/empty", true, s.emptyRecycle},
		{"GET /api/v1/calendar", true, s.calendar},
		{"GET /api/v1/activity", true, s.activity},

		{"GET /api/v1/backups", true, s.backups},
		{"POST /api/v1/backups", true, s.makeBackup},
		{"PUT /api/v1/backups/settings", true, s.saveBackupSettings},
		{"POST /api/v1/backups/test", true, s.testBackupRemote},
		{"POST /api/v1/backups/restore", true, s.restoreUpload},
		{"GET /api/v1/backups/{name}", true, s.downloadBackup},
		{"POST /api/v1/backups/{name}/restore", true, s.restoreBackup},

		{"POST /api/v1/migrations/preview", true, s.previewMigration},
		{"GET /api/v1/health", true, s.health},
		{"GET /api/v1/stats", true, s.getStats},
		{"GET /api/v1/migrations/find", true, s.findApp},
		{"PUT /api/v1/migrations/done", true, s.setMovedIn(true)},
		{"DELETE /api/v1/migrations/done", true, s.setMovedIn(false)},
		{"POST /api/v1/migrations/scans", true, s.startScan},
		{"GET /api/v1/migrations/scans", true, s.scans},
		{"GET /api/v1/migrations/scans/{id}", true, s.scan},
		{"DELETE /api/v1/migrations/scans/{id}", true, s.dismissScan},
		{"POST /api/v1/migrations", true, s.runMigration},
		{"GET /api/v1/migrations", true, s.migrations},
		{"GET /api/v1/migrations/{id}", true, s.migration},
		{"POST /api/v1/matinee/switch", true, s.switchMatinee},

		{"GET /api/v1/connections", true, s.connections},
		{"DELETE /api/v1/indexers/{id}", true, s.deleteConnection(func(r *http.Request, id int64) error { return s.Store.DeleteIndexer(r.Context(), id) })},
		{"DELETE /api/v1/downloadclients/{id}", true, s.deleteConnection(func(r *http.Request, id int64) error { return s.Store.DeleteDownloadClient(r.Context(), id) })},
		{"DELETE /api/v1/usenetservers/{id}", true, s.deleteConnection(func(r *http.Request, id int64) error { return s.Store.DeleteUsenetServer(r.Context(), id) })},
		{"GET /api/v1/blocklist", true, s.blocklist},
		{"GET /api/v1/imported", true, s.importedSettings},

		{"GET /api/v1/admin/monitor", true, s.getMonitor},
		{"GET /api/v1/admin/logs", true, s.getLogs},
		{"GET /api/v1/admin/logs/download", true, s.downloadLogs},
		{"PUT /api/v1/admin/debug", true, s.putDebug},
		{"GET /api/v1/admin/alerts", true, s.getAlerts},
		{"PUT /api/v1/admin/alerts", true, s.putAlerts},
		{"POST /api/v1/admin/alerts/test", true, s.testAlerts},
		{"GET /api/v1/admin/metrics", true, s.getMetricsSettings},
		{"POST /api/v1/admin/metrics/token", true, s.newMetricsToken},
		{"DELETE /api/v1/admin/metrics/token", true, s.deleteMetricsToken},
	}
	routes = append(routes, s.discoverRoutes()...)
	routes = append(routes, s.downloadRoutes()...)
	routes = append(routes, s.downloadFileRoutes()...)
	routes = append(routes, s.quarantineRoutes()...)
	routes = append(routes, s.connectionRoutes()...)
	routes = append(routes, s.libScanRoutes()...)
	routes = append(routes, s.portalRoutes()...)
	for _, r := range routes {
		mux.Handle(r.pattern, s.wrap(r))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "there's no such API")
	})
	key := s.APIKey
	pair := s.pairing()
	mux.Handle("/radarr/", http.StripPrefix("/radarr", arrapi.NewPaired(arrapi.Radarr, s.Catalog, key, s.Version, s.Log, pair)))
	mux.Handle("/sonarr/", http.StripPrefix("/sonarr", arrapi.NewPaired(arrapi.Sonarr, s.Catalog, key, s.Version, s.Log, pair)))
	// Prometheus, with the token from Health → Prometheus (see alerts.go).
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Store.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.Handle("/", s.web())
	return s.counted(securityHeaders(mux))
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		// Posters come from TMDB, or for titles moved in from Sonarr or
		// Radarr, from where those got them (TheTVDB, fanart.tv); everything
		// else is Box Office's own.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: "+imageSources+"; style-src 'self' 'unsafe-inline'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

// web serves the web UI, with index.html for any path it doesn't have, so
// the UI's own routes load on refresh.
func (s *Server) web() http.Handler {
	if s.Web == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!doctype html><title>Box Office</title><p>Box Office is running, but its web UI wasn't built into this binary. Build it with <code>make build</code>.</p>")
		})
	}
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.Web, p); err != nil {
			p = "index.html"
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		// On the request site's own host name, the page says so, and the
		// web UI shows the request site at /.
		if p == "index.html" && s.portalHost(r) {
			if page, err := fs.ReadFile(s.Web, p); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				w.Write([]byte(strings.Replace(string(page), "<head>", `<head><meta name="boxoffice-portal" content="/">`, 1)))
				return
			}
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// ---- answers ----

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errorf(status int, msg string) error { return &httpError{status, msg} }

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errorf(http.StatusUnsupportedMediaType, "send JSON")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errorf(http.StatusBadRequest, "that isn't valid JSON")
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errorf(http.StatusNotFound, "not found")
	}
	return id, nil
}

type ctxKey struct{}

// wrap checks sign-in for routes that need it, and turns errors into answers.
func (s *Server) wrap(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.auth {
			u, ok := s.authenticate(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "sign in first")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
			// A browser's cookie rides along on requests from other sites;
			// those can't send JSON or custom headers without asking first,
			// so changes from a browser must come as JSON or with the key.
			if r.Method != "GET" && r.Method != "HEAD" && u.ID != 0 && r.Header.Get("X-Requested-With") == "" &&
				!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusForbidden, "send changes as JSON")
				return
			}
		}
		err := rt.fn(w, r)
		if err == nil {
			return
		}
		var he *httpError
		var ce *catalog.Error
		switch {
		case errors.As(err, &he):
			writeError(w, he.status, he.msg)
		case errors.As(err, &ce):
			writeError(w, http.StatusBadRequest, ce.Message)
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not found")
		case errors.Is(err, store.ErrExists):
			writeError(w, http.StatusConflict, "that's there already")
		case errors.Is(err, catalog.ErrExists):
			writeError(w, http.StatusConflict, "that's on the list already")
		case errors.Is(err, store.ErrAuthorLocked):
			writeError(w, http.StatusConflict, "This author is set to Do not touch, so Box Office leaves their books alone. Turn that off on the author's page first.")
		case errors.Is(err, store.ErrLocked):
			writeError(w, http.StatusConflict, "This title is set to Do not touch, so Box Office leaves its files alone. Pick another quality profile for it first.")
		case errors.Is(err, store.ErrInUse):
			writeError(w, http.StatusConflict, "titles still use it; move them to another first")
		case errors.Is(err, tmdb.ErrNoKey):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		case errors.Is(err, context.Canceled):
		default:
			s.Log.Warn("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	})
}

// authenticate finds who's asking: an admin by session, or the API key
// (as user 0).
func (s *Server) authenticate(r *http.Request) (store.User, bool) {
	if k := r.Header.Get("X-Api-Key"); k != "" {
		want := s.APIKey(r.Context())
		return store.User{Username: "api key"}, want != "" && constEq(k, want)
	}
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	} else if c, err := r.Cookie(cookieName); err == nil {
		tok = c.Value
	}
	if tok == "" {
		return store.User{}, false
	}
	u, err := s.Store.SessionUser(r.Context(), tok)
	return u, err == nil
}

func constEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range len(a) {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func user(r *http.Request) store.User {
	u, _ := r.Context().Value(ctxKey{}).(store.User)
	return u
}

// ---- signing in ----

// status needs no sign-in. Product and id let other apps, such as Matinee
// looking for Box Office on the network, tell it's Box Office, and which.
func (s *Server) status(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		return err
	}
	id, err := s.Store.InstanceID(r.Context())
	if err != nil {
		return err
	}
	out := map[string]any{"product": "boxoffice", "id": id, "setup": n > 0, "version": s.Version}
	if u, ok := s.authenticate(r); ok {
		out["user"] = u
		v, err := s.Store.Setting(r.Context(), settingMovedIn)
		if err != nil {
			return err
		}
		out["movedIn"] = v != ""
	}
	return writeJSON(w, http.StatusOK, out)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c credentials) check() error {
	if strings.TrimSpace(c.Username) == "" {
		return errorf(http.StatusBadRequest, "enter a username")
	}
	if len(c.Password) < 8 {
		return errorf(http.StatusBadRequest, "use a password of at least 8 characters")
	}
	return nil
}

var setupMu sync.Mutex

// setup makes the first admin. It only works once.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	if err := c.check(); err != nil {
		return err
	}
	setupMu.Lock()
	defer setupMu.Unlock()
	if n, err := s.Store.CountUsers(r.Context()); err != nil {
		return err
	} else if n > 0 {
		return errorf(http.StatusConflict, "Box Office is set up already; sign in")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u, err := s.Store.CreateUser(r.Context(), strings.TrimSpace(c.Username), string(hash))
	if err != nil {
		return err
	}
	return s.signIn(w, r, u)
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request, u store.User) error {
	tok, err := s.Store.CreateSession(r.Context(), u.ID, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", MaxAge: int(sessionTTL.Seconds())})
	return writeJSON(w, http.StatusOK, map[string]any{"user": u, "token": tok})
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("box office"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	name := strings.TrimSpace(c.Username)
	fv, _ := s.failures.LoadOrStore(strings.ToLower(name), &failure{})
	f := fv.(*failure)
	f.mu.Lock()
	wait := time.Until(f.until)
	f.mu.Unlock()
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		return errorf(http.StatusTooManyRequests, "too many wrong passwords; try again in a few minutes")
	}
	u, err := s.Store.UserByName(r.Context(), name)
	hash := dummyHash
	if err == nil {
		hash = []byte(u.PasswordHash())
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(c.Password)) != nil || err != nil {
		f.mu.Lock()
		f.count++
		if f.count >= 5 {
			f.until = time.Now().Add(time.Duration(min(f.count-4, 15)) * time.Minute)
		}
		f.mu.Unlock()
		s.Log.Warn("wrong sign-in", "username", name, "from", clientIP(r))
		return errorf(http.StatusUnauthorized, "that username and password don't match")
	}
	s.failures.Delete(strings.ToLower(name))
	return s.signIn(w, r, u)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	if u.ID == 0 {
		return errorf(http.StatusForbidden, "sign in as an admin to change a password")
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash()), []byte(in.Current)) != nil {
		return errorf(http.StatusForbidden, "the current password isn't right")
	}
	if len(in.New) < 8 {
		return errorf(http.StatusBadRequest, "use a password of at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.Store.SetPassword(r.Context(), u.ID, string(hash)); err != nil {
		return err
	}
	return s.signIn(w, r, u)
}

// imageSources are where posters and backdrops are shown from.
// Album covers come from the Cover Art Archive, which sends browsers on to
// archive.org.
const imageSources = "https://image.tmdb.org https://artworks.thetvdb.com https://thetvdb.com https://assets.fanart.tv https://coverartarchive.org https://archive.org https://*.archive.org https://covers.openlibrary.org"
