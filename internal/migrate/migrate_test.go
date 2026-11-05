package migrate

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

const legacyKey = "radarr-key"

type world struct {
	m     *Migrator
	st    *store.Store
	media string // where Box Office sees the media
	dir   string
}

func newWorld(t *testing.T) world {
	t.Helper()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	cat.Register()
	dir := t.TempDir()
	w := world{st: st, dir: dir, media: filepath.Join(dir, "media")}
	w.m = &Migrator{Catalog: cat, Backups: &backup.Manager{Store: st, Dir: filepath.Join(dir, "backups"), Log: log}, Log: log}
	return w
}

// legacyDB builds the app's own database, as found inside its backup,
// holding the secrets its API hides.
func legacyZip(t *testing.T, app string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), app+".db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE Indexers (Id INTEGER PRIMARY KEY, Name TEXT, Settings TEXT)`,
		`CREATE TABLE DownloadClients (Id INTEGER PRIMARY KEY, Name TEXT, Settings TEXT)`,
		`INSERT INTO Indexers VALUES (4, 'NZBgeek', '{"baseUrl":"https://api.nzbgeek.info","apiKey":"real-indexer-key","categories":[2000,2040]}')`,
		`INSERT INTO DownloadClients VALUES (2, 'SABnzbd', '{"host":"sab","port":8080,"apiKey":"real-sab-key","movieCategory":"movies"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	raw, _ := os.ReadFile(path)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create(app + ".db")
	f.Write(raw)
	zw.Close()
	return buf.Bytes()
}

// fakeRadarr is a Radarr with one profile, one movie and its file, one
// indexer and one download client, whose secrets are masked over the API.
func fakeRadarr(t *testing.T, zipData []byte) (*httptest.Server, *sync.Map) {
	t.Helper()
	calls := &sync.Map{}
	mux := http.NewServeMux()
	j := func(v any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
	}
	mux.HandleFunc("GET /api/v3/system/status", j(map[string]any{"appName": "Radarr", "version": "5.14.0"}))
	mux.HandleFunc("GET /api/v3/qualityprofile", j([]map[string]any{{
		"id": 7, "name": "Movies HD", "upgradeAllowed": true, "cutoff": 7,
		"items": []map[string]any{
			{"quality": map[string]any{"id": 3, "name": "WEBDL-1080p"}, "allowed": true},
			{"quality": map[string]any{"id": 7, "name": "Bluray-1080p"}, "allowed": true},
			{"quality": map[string]any{"id": 19, "name": "Bluray-2160p"}, "allowed": false},
		}}}))
	mux.HandleFunc("GET /api/v3/rootfolder", j([]map[string]any{{"path": "/movies"}}))
	mux.HandleFunc("GET /api/v3/movie", j([]map[string]any{
		{"id": 1, "tmdbId": 603, "title": "The Matrix", "year": 1999, "monitored": true, "qualityProfileId": 7,
			"path": "/movies/The Matrix (1999)", "rootFolderPath": "/movies", "hasFile": true,
			"movieFile": map[string]any{"path": "/movies/The Matrix (1999)/The Matrix.mkv", "size": 1234,
				"quality": map[string]any{"quality": map[string]any{"id": 7, "name": "Bluray-1080p"}}}},
		{"id": 2, "tmdbId": 0, "title": "Home Video", "year": 2003, "path": "/movies/Home Video"},
	}))
	mux.HandleFunc("GET /api/v3/indexer", j([]map[string]any{{
		"id": 4, "name": "NZBgeek", "implementation": "Newznab", "protocol": "usenet", "priority": 25, "enableRss": true, "enableAutomaticSearch": true,
		"fields": []map[string]any{{"name": "baseUrl", "value": "https://api.nzbgeek.info"}, {"name": "apiPath", "value": "/api"},
			{"name": "apiKey", "value": "********"}, {"name": "categories", "value": []int{2000, 2040}}},
	}}))
	mux.HandleFunc("GET /api/v3/downloadclient", j([]map[string]any{{
		"id": 2, "name": "SABnzbd", "implementation": "Sabnzbd", "protocol": "usenet", "priority": 1, "enable": true,
		"fields": []map[string]any{{"name": "host", "value": "sab"}, {"name": "port", "value": 8080}, {"name": "useSsl", "value": false},
			{"name": "apiKey", "value": "********"}, {"name": "movieCategory", "value": "movies"}},
	}}))
	mux.HandleFunc("GET /api/v3/config/naming", j(map[string]any{"renameMovies": true, "standardMovieFormat": "{Movie Title} ({Release Year})"}))
	mux.HandleFunc("GET /api/v3/notification", j([]map[string]any{}))
	mux.HandleFunc("GET /api/v3/blocklist", j(map[string]any{"totalRecords": 1, "records": []map[string]any{
		{"movieId": 1, "sourceTitle": "The.Matrix.1999.CAM", "quality": map[string]any{"quality": map[string]any{"id": 1, "name": "CAM"}},
			"date": "2024-01-02T03:04:05Z", "protocol": "usenet", "indexer": "NZBgeek", "message": "bad"}}}))
	mux.HandleFunc("POST /api/v3/command", func(w http.ResponseWriter, r *http.Request) {
		calls.Store("backup", true)
		j(map[string]any{"id": 99})(w, r)
	})
	mux.HandleFunc("GET /api/v3/command/99", j(map[string]any{"status": "completed"}))
	mux.HandleFunc("GET /api/v3/system/backup", j([]map[string]any{
		{"name": "radarr_backup_v5.14.0_2026.11.04.zip", "path": "/backup/manual/radarr_backup.zip", "type": "manual", "time": time.Now().UTC()}}))
	mux.HandleFunc("GET /backup/manual/radarr_backup.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zipData) })
	// Anything else, like a section this version doesn't have, is a 404.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != legacyKey {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" && r.Method != "POST" {
			t.Errorf("the migration changed something in Radarr: %s %s", r.Method, r.URL.Path)
		}
		mux.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, calls
}

func waitDone(t *testing.T, m *Migrator, id int64) Progress {
	t.Helper()
	for range 200 {
		pr, err := m.Progress(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if pr.Status != "running" {
			return pr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the migration didn't finish")
	return Progress{}
}

func TestMigrateRadarr(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	movieDir := filepath.Join(w.media, "movies", "The Matrix (1999)")
	os.MkdirAll(movieDir, 0o755)
	os.WriteFile(filepath.Join(movieDir, "The Matrix.mkv"), []byte("x"), 0o644)
	ts, calls := fakeRadarr(t, legacyZip(t, "radarr"))

	req := Request{App: "Radarr", URL: ts.URL, APIKey: legacyKey, PathMap: []PathMap{{From: "/movies", To: filepath.Join(w.media, "movies")}}}
	if _, err := w.m.Preview(ctx, Request{App: "radarr", URL: ts.URL, APIKey: "wrong"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("a wrong key: %v", err)
	}
	plan, err := w.m.Preview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Titles.Total != 2 || plan.Titles.New != 1 || plan.Titles.Skipped != 1 || plan.Files.Total != 1 || plan.Files.Missing != 0 {
		t.Fatalf("plan counts: %+v %+v", plan.Titles, plan.Files)
	}
	if plan.Settings.Indexers != 1 || plan.Settings.DownloadClients != 1 || plan.Settings.Blocklist != 1 || plan.Settings.HiddenSecrets != 2 {
		t.Fatalf("plan settings: %+v", plan.Settings)
	}
	if len(plan.Profiles) != 1 || plan.Profiles[0].Action != "create" || len(plan.Profiles[0].Qualities) != 2 {
		t.Fatalf("plan profiles: %+v", plan.Profiles)
	}
	if _, ok := calls.Load("backup"); ok {
		t.Fatal("the preview asked Radarr for a backup")
	}
	if n, _ := w.st.Movies(ctx); len(n) != 0 {
		t.Fatal("the preview added movies")
	}

	id, err := w.m.Run(ctx, plan.Token, Options{BackupBoxOffice: true, BackupLegacy: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.m.Run(ctx, plan.Token, Options{}); err == nil {
		t.Fatal("a plan ran twice")
	}
	pr := waitDone(t, w.m, id)
	if pr.Status != "done" {
		t.Fatalf("migration: %+v", pr)
	}
	for _, s := range pr.Steps {
		if s.Status != "done" {
			t.Fatalf("step %q: %s %s", s.Name, s.Status, s.Message)
		}
	}
	if pr.Imported.New != 1 || pr.Imported.Skipped != 1 || pr.Files != 1 || len(pr.Backups) != 2 {
		t.Fatalf("report: %+v", pr)
	}

	movies, _ := w.st.Movies(ctx)
	if len(movies) != 1 || movies[0].TMDBID != 603 || movies[0].Path != movieDir {
		t.Fatalf("movies: %+v", movies)
	}
	// Matinee saved Radarr's profile id 7; Box Office answers to it.
	ids, err := w.st.LegacyIDs(ctx, "radarr", "profile")
	if err != nil || ids[7] == 0 {
		t.Fatalf("legacy profile ids: %v %v", ids, err)
	}

	idx, _ := w.st.Indexers(ctx)
	if len(idx) != 1 || idx[0].APIKey != "real-indexer-key" || idx[0].MissingSecret || idx[0].URL != "https://api.nzbgeek.info" || len(idx[0].Categories) != 2 {
		t.Fatalf("indexers: %+v", idx)
	}
	dcs, _ := w.st.DownloadClients(ctx)
	if len(dcs) != 1 || dcs[0].Password != "real-sab-key" || dcs[0].Host != "sab" || dcs[0].Port != 8080 || dcs[0].Category != "movies" {
		t.Fatalf("download clients: %+v", dcs)
	}
	if n, _ := w.st.CountBlocklist(ctx); n != 1 {
		t.Fatalf("blocklist: %d", n)
	}
	sets, _ := w.st.ImportedSettings(ctx)
	have := map[string]bool{}
	for _, s := range sets {
		have[s.Section] = true
	}
	if !have["config/naming"] || !have["indexer"] || have["customformat"] {
		t.Fatalf("imported sections: %v", have)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "backups", "imported", "radarr-radarr_backup_v5.14.0_2026.11.04.zip")); err != nil {
		t.Fatalf("Radarr's backup wasn't kept: %v", err)
	}

	// Running it again changes nothing and adds nothing twice.
	plan, err = w.m.Preview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Titles.Already != 1 || plan.Titles.New != 0 || plan.Profiles[0].Action != "match" {
		t.Fatalf("second plan: %+v %+v", plan.Titles, plan.Profiles)
	}
	id, _ = w.m.Run(ctx, plan.Token, Options{})
	if pr := waitDone(t, w.m, id); pr.Status != "done" {
		t.Fatalf("second run: %+v", pr)
	}
	if idx, _ := w.st.Indexers(ctx); len(idx) != 1 || idx[0].APIKey != "real-indexer-key" {
		t.Fatalf("a second run without the backup lost the indexer's key: %+v", idx)
	}
}

func TestMigrateWithoutLegacyBackup(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ts, calls := fakeRadarr(t, legacyZip(t, "radarr"))
	plan, err := w.m.Preview(ctx, Request{App: "radarr", URL: ts.URL, APIKey: legacyKey})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := w.m.Run(ctx, plan.Token, Options{})
	pr := waitDone(t, w.m, id)
	if pr.Status != "done" || pr.Steps[1].Status != "skipped" {
		t.Fatalf("migration: %+v", pr)
	}
	if _, ok := calls.Load("backup"); ok {
		t.Fatal("Radarr was asked for a backup that was turned off")
	}
	idx, _ := w.st.Indexers(ctx)
	if len(idx) != 1 || idx[0].APIKey != "" || !idx[0].MissingSecret {
		t.Fatalf("an indexer without its key should be marked: %+v", idx)
	}
	if !slicesContainsSub(pr.Warnings, "came over blank") {
		t.Fatalf("no warning about blank secrets: %v", pr.Warnings)
	}
}

func slicesContainsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestMigrateNZBGet(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	config := []map[string]string{
		{"Name": "ControlUsername", "Value": "nzbget"}, {"Name": "ControlPassword", "Value": "tegbzn6789"},
		{"Name": "MainDir", "Value": "/downloads"}, {"Name": "DownloadRate", "Value": "5000"},
		{"Name": "Server1.Name", "Value": "Primary"}, {"Name": "Server1.Host", "Value": "news.example.com"},
		{"Name": "Server1.Port", "Value": "563"}, {"Name": "Server1.Encryption", "Value": "yes"},
		{"Name": "Server1.Username", "Value": "me"}, {"Name": "Server1.Password", "Value": "server-pass"},
		{"Name": "Server1.Connections", "Value": "30"}, {"Name": "Server1.Level", "Value": "0"}, {"Name": "Server1.Active", "Value": "yes"},
		{"Name": "Server2.Host", "Value": "fill.example.com"}, {"Name": "Server2.Port", "Value": "119"}, {"Name": "Server2.Level", "Value": "1"},
		{"Name": "Server2.Optional", "Value": "yes"}, {"Name": "Server3.Host", "Value": ""},
		{"Name": "Category1.Name", "Value": "movies"}, {"Name": "Category1.DestDir", "Value": "/downloads/movies"},
		{"Name": "Category2.Name", "Value": "tv"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if r.URL.Path != "/jsonrpc" || u != "nzbget" || p != "tegbzn6789" {
			rw.WriteHeader(401)
			return
		}
		var call struct{ Method string }
		json.NewDecoder(r.Body).Decode(&call)
		var result any
		switch call.Method {
		case "version":
			result = "24.3"
		case "config":
			result = config
		default:
			t.Errorf("the migration called NZBGet's %s", call.Method)
		}
		json.NewEncoder(rw).Encode(map[string]any{"version": "1.1", "result": result})
	}))
	t.Cleanup(ts.Close)

	if _, err := w.m.Preview(ctx, Request{App: "nzbget", URL: ts.URL, Username: "nzbget", Password: "nope"}); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	plan, err := w.m.Preview(ctx, Request{App: "nzbget", URL: ts.URL, Username: "nzbget", Password: "tegbzn6789"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.AppVersion != "24.3" || len(plan.Servers) != 2 || plan.Settings.Categories != 2 || !plan.Servers[0].HasPassword {
		t.Fatalf("plan: %+v", plan)
	}
	id, err := w.m.Run(ctx, plan.Token, Options{BackupLegacy: true})
	if err != nil {
		t.Fatal(err)
	}
	pr := waitDone(t, w.m, id)
	if pr.Status != "done" || len(pr.Steps) != 3 || pr.Steps[1].Status != "done" || pr.Steps[2].Status != "done" {
		t.Fatalf("migration: %+v", pr)
	}
	servers, _ := w.st.UsenetServers(ctx)
	if len(servers) != 2 || servers[0].Host != "news.example.com" || servers[0].Password != "server-pass" || !servers[0].UseTLS ||
		servers[0].Connections != 30 || !servers[1].Optional || servers[1].Level != 1 || servers[1].Name != "fill.example.com" {
		t.Fatalf("servers: %+v", servers)
	}
	sets, _ := w.st.ImportedSettings(ctx)
	for _, s := range sets {
		if strings.Contains(string(s.Data), "tegbzn6789") || strings.Contains(string(s.Data), "server-pass") {
			t.Fatalf("section %s kept a password it shouldn't: %s", s.Section, s.Data)
		}
	}
	if len(sets) != 2 {
		t.Fatalf("sections: %+v", sets)
	}
}

func TestSwitchMatinee(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	var saved map[string]any
	loggedOut := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(rw http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["password"] != "pw" {
			rw.WriteHeader(401)
			json.NewEncoder(rw).Encode(map[string]string{"error": "bad"})
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"token": "tok", "user": map[string]any{"admin": true}})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(rw http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				rw.WriteHeader(401)
				return
			}
			h(rw, r)
		}
	}
	mux.HandleFunc("GET /api/v1/integrations", authed(func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(map[string]any{"radarr": map[string]any{"connected": true, "url": "http://radarr:7878", "qualityProfileId": 7, "rootFolder": "/movies"}})
	}))
	mux.HandleFunc("PUT /api/v1/integrations/radarr", authed(func(rw http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&saved)
		rw.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/v1/auth/logout", authed(func(rw http.ResponseWriter, r *http.Request) { loggedOut = true }))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sr := SwitchRequest{MatineeURL: ts.URL, Username: "mike", Password: "pw", BoxOfficeURL: "http://boxoffice:7272", App: "radarr"}
	// Box Office needs a movie root folder for Matinee to save.
	if _, err := w.m.SwitchMatinee(ctx, 0, sr, "bo-key"); err == nil || !strings.Contains(err.Error(), "no movie root folder") {
		t.Fatalf("no root folder: %v", err)
	}
	// Matinee's /movies isn't one of Box Office's, so its only one is used,
	// exactly as Box Office lists it.
	if _, err := w.st.AddRootFolder(ctx, "movie", "/library/movies"); err != nil {
		t.Fatal(err)
	}
	sr.Password = "wrong"
	if _, err := w.m.SwitchMatinee(ctx, 0, sr, "bo-key"); err == nil || !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("a wrong password: %v", err)
	}
	sr.Password = "pw"
	res, err := w.m.SwitchMatinee(ctx, 0, sr, "bo-key")
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != "http://radarr:7878" || res.After != "http://boxoffice:7272/radarr" || !loggedOut {
		t.Fatalf("result: %+v, logged out %v", res, loggedOut)
	}
	if saved["url"] != "http://boxoffice:7272/radarr" || saved["apiKey"] != "bo-key" || saved["qualityProfileId"] != float64(7) || saved["rootFolder"] != "/library/movies" {
		t.Fatalf("Matinee was sent %v", saved)
	}
}

func TestMapPath(t *testing.T) {
	maps := []PathMap{{From: "/data", To: "/mnt/a"}, {From: "/data/movies/", To: "/mnt/movies"}}
	for in, want := range map[string]string{
		"/data/movies/X/x.mkv": "/mnt/movies/X/x.mkv",
		"/data/tv/Y":           "/mnt/a/tv/Y",
		"/database/z":          "/database/z",
		"/data":                "/mnt/a",
	} {
		if got := mapPath(maps, in); got != want {
			t.Errorf("mapPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatineeRoot(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for _, p := range []string{"/media/4k", "/media/movies"} {
		if _, err := w.st.AddRootFolder(ctx, "movie", p); err != nil {
			t.Fatal(err)
		}
	}
	for want, got := range map[string]string{
		"/media/movies/":     "/media/movies", // Radarr's trailing slash
		"/media/movies/Kids": "/media/movies",
		"/elsewhere":         "/media/4k", // the first
		"":                   "/media/4k",
	} {
		if r, err := w.m.matineeRoot(ctx, "radarr", want); err != nil || r != got {
			t.Errorf("%q: got %q, %v; want %q", want, r, err, got)
		}
	}
}

// A group as the cutoff: any quality in it is good enough, as in Radarr
// and Sonarr, so the cutoff is the group's lowest.
func TestMapProfileGroupCutoff(t *testing.T) {
	var lp lProfile
	if err := json.Unmarshal([]byte(`{"id": 4, "name": "HD-1080p", "upgradeAllowed": true, "cutoff": 1001, "items": [
		{"quality": {"id": 4, "name": "HDTV-720p"}, "allowed": true},
		{"id": 1001, "name": "WEB 1080p", "allowed": true, "items": [
			{"quality": {"id": 15, "name": "WEBRip-1080p"}, "allowed": true},
			{"quality": {"id": 3, "name": "WEBDL-1080p"}, "allowed": true}]},
		{"quality": {"id": 7, "name": "Bluray-1080p"}, "allowed": true}]}`), &lp); err != nil {
		t.Fatal(err)
	}
	p := mapProfile(lp)
	if want := quality.Parse("WEBRip-1080p").ID; p.Cutoff != want || !p.UpgradeOK {
		t.Fatalf("profile %+v, want cutoff %d", p, want)
	}
	if p.CutoffUnmet(quality.Parse("WEBRip-1080p").ID) || !p.CutoffUnmet(quality.Parse("HDTV-720p").ID) {
		t.Fatal("cutoff judged wrong")
	}
}
