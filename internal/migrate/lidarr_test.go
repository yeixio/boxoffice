package migrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

const (
	radioheadMBID = "a74b1b7f-71a5-4011-9441-d0b5e4122711"
	okcMBID       = "b1392450-e666-3926-a536-22c65f834433"
	kidAMBID      = "1f3d1b2a-1c3a-3b5d-a4b2-2a1b3c4d5e6f"
	liveMBID      = "2a4e2c3b-2d4b-4c6e-b5c3-3b2c4d5e6f70"
)

// fakeLidarr is a Lidarr with one artist and three albums: OK Computer,
// whose tracks it lists on two discs; Kid A, which it lists no tracks for
// but whose folder is there; and a live album it doesn't watch for.
func fakeLidarr(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	j := func(v any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(v) }
	}
	mux.HandleFunc("GET /api/v1/system/status", j(map[string]any{"appName": "Lidarr", "version": "2.5.3"}))
	mux.HandleFunc("GET /api/v1/rootfolder", j([]map[string]any{{"path": "/music"}}))
	mux.HandleFunc("GET /api/v1/artist", j([]map[string]any{
		{"id": 1, "artistName": "Radiohead", "foreignArtistId": radioheadMBID, "sortName": "radiohead", "artistType": "Group",
			"path": "/music/Radiohead", "rootFolderPath": "/music", "monitored": true, "statistics": map[string]any{"trackFileCount": 3}},
		{"id": 2, "artistName": "Home Tapes", "path": "/music/Home Tapes"},
	}))
	mux.HandleFunc("GET /api/v1/album", j([]map[string]any{
		{"id": 10, "artistId": 1, "title": "OK Computer", "foreignAlbumId": okcMBID, "albumType": "Album",
			"secondaryTypes": []map[string]any{{"id": 0, "name": "Studio"}}, "releaseDate": "1997-05-21T00:00:00Z", "monitored": true},
		{"id": 11, "artistId": 1, "title": "Kid A", "foreignAlbumId": kidAMBID, "albumType": "Album",
			"secondaryTypes": []map[string]any{}, "releaseDate": "2000-10-02T00:00:00Z", "monitored": true},
		{"id": 12, "artistId": 1, "title": "I Might Be Wrong", "foreignAlbumId": liveMBID, "albumType": "Album",
			"secondaryTypes": []map[string]any{{"id": 6, "name": "Live"}}, "releaseDate": "2001-11-12T00:00:00Z", "monitored": false},
	}))
	mux.HandleFunc("GET /api/v1/trackfile", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("artistId") != "1" {
			t.Errorf("track files asked for artist %q", r.URL.Query().Get("artistId"))
		}
		j([]map[string]any{
			{"albumId": 10, "path": "/music/Radiohead/OK Computer (1997)/CD1/01 Airbag.flac", "size": 100},
			{"albumId": 10, "path": "/music/Radiohead/OK Computer (1997)/CD2/01 Lull.flac", "size": 50},
			{"albumId": 12, "path": "/music/Radiohead/01 Loose.mp3", "size": 7},
		})(w, r)
	})
	mux.HandleFunc("GET /api/v1/indexer", j([]map[string]any{}))
	mux.HandleFunc("GET /api/v1/downloadclient", j([]map[string]any{{
		"id": 3, "name": "SABnzbd", "implementation": "Sabnzbd", "protocol": "usenet", "priority": 1, "enable": true,
		"fields": []map[string]any{{"name": "host", "value": "sab"}, {"name": "port", "value": 8080},
			{"name": "apiKey", "value": "plain-key"}, {"name": "musicCategory", "value": "music"}},
	}}))
	mux.HandleFunc("GET /api/v1/blocklist", j(map[string]any{"totalRecords": 1, "records": []map[string]any{
		{"albumIds": []int{11}, "sourceTitle": "Radiohead-Kid_A-WEB-2000", "quality": map[string]any{"quality": map[string]any{"name": "MP3-128"}},
			"date": "2024-01-02T03:04:05Z", "protocol": "usenet", "indexer": "NZBgeek", "message": "bad"}}}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != legacyKey {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			t.Errorf("the migration changed something in Lidarr: %s %s", r.Method, r.URL.Path)
		}
		if strings.HasPrefix(r.URL.Path, "/api/v3/") {
			t.Errorf("asked Lidarr's v3 API: %s", r.URL.Path)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestMigrateLidarr(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	music := filepath.Join(w.media, "music")
	artist := filepath.Join(music, "Radiohead")
	files := []string{"OK Computer (1997)/CD1/01 Airbag.flac", "OK Computer (1997)/CD2/01 Lull.flac", "2000 - Kid A/01 Everything.flac", "01 Loose.mp3"}
	for _, f := range files {
		p := filepath.Join(artist, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("audio"), 0o644)
	}
	ts := fakeLidarr(t)
	req := Request{App: "lidarr", URL: ts.URL, APIKey: legacyKey, PathMap: []PathMap{{From: "/music", To: music}}}

	if _, err := w.m.Preview(ctx, req); err == nil || !strings.Contains(err.Error(), "Music") {
		t.Fatalf("with music off: %v", err)
	}
	if err := mediatypes.SetEnabled(ctx, w.st, mediatypes.Music, true); err != nil {
		t.Fatal(err)
	}
	plan, err := w.m.Preview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Titles.Total != 2 || plan.Titles.New != 1 || plan.Titles.Skipped != 1 || len(plan.Profiles) != 0 {
		t.Fatalf("plan: %+v %+v", plan.Titles, plan.Profiles)
	}
	if len(plan.RootFolders) != 1 || plan.RootFolders[0].Path != music || !plan.RootFolders[0].Exists {
		t.Fatalf("root folders: %+v", plan.RootFolders)
	}
	if len(plan.Sample) != 1 || plan.Sample[0].Title != "Radiohead" || !plan.Sample[0].HasFile {
		t.Fatalf("sample: %+v", plan.Sample)
	}

	id, err := w.m.Run(ctx, plan.Token, Options{})
	if err != nil {
		t.Fatal(err)
	}
	pr := waitDone(t, w.m, id)
	if pr.Status != "done" {
		t.Fatalf("migration: %+v", pr)
	}
	if pr.Imported.New != 1 || pr.Imported.Skipped != 1 || pr.Files != 2 {
		t.Fatalf("report: %+v", pr)
	}
	if folders, _ := w.st.RootFolders(ctx, mediatypes.Music); len(folders) != 1 || folders[0].Path != music {
		t.Fatalf("music root folders: %+v", folders)
	}
	a, err := w.st.ArtistByMBID(ctx, radioheadMBID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != artist || a.RootFolder != music || !a.Monitored || !slices.Equal(a.AlbumTypes, []string{"Album"}) {
		t.Fatalf("artist: %+v", a)
	}
	albums, _ := w.st.Albums(ctx, a.ID)
	got := map[string]store.Album{}
	for _, b := range albums {
		got[b.Title] = b
	}
	if b := got["OK Computer"]; b.File == nil || b.File.Path != filepath.Join(artist, "OK Computer (1997)") || b.File.Size != 150 || b.Released != "1997-05-21" || len(b.SecondaryTypes) != 0 {
		t.Fatalf("OK Computer: %+v %+v", b, b.File)
	}
	if b := got["Kid A"]; b.File == nil || b.File.Path != filepath.Join(artist, "2000 - Kid A") || !b.Monitored {
		t.Fatalf("Kid A, found by its folder: %+v %+v", b, b.File)
	}
	if b := got["I Might Be Wrong"]; b.File != nil || b.Monitored || !slices.Equal(b.SecondaryTypes, []string{"Live"}) {
		t.Fatalf("the live album, loose in the artist's folder: %+v", b)
	}
	if bl, _ := w.st.BlockedFor(ctx, store.AlbumKind, got["Kid A"].ID); len(bl) != 1 {
		t.Fatalf("Kid A's blocklist: %+v", bl)
	}
	// Nothing on disk moved.
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(artist, f)); err != nil {
			t.Fatal(err)
		}
	}

	// Moving in again leaves the artist as it is.
	plan, err = w.m.Preview(ctx, req)
	if err != nil || plan.Titles.Already != 1 || plan.Titles.New != 0 {
		t.Fatalf("again: %+v %v", plan.Titles, err)
	}
}
