package playon

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// fake is a stand-in for PlayOn Cloud: one account, its recordings, and a
// file server that wants CloudFront's cookies.
type fake struct {
	mu         sync.Mutex
	entries    []map[string]any
	queue      []map[string]any
	files      map[string]string // recording id -> file contents
	logins     int
	downloads  map[string]int
	brokenOnce map[string]bool
	srv        *httptest.Server
}

func newFake(t *testing.T) *fake {
	f := &fake{files: map[string]string{}, downloads: map[string]int{}, brokenOnce: map[string]bool{}}
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, data any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "jwt-123" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "not signed in"})
			return false
		}
		return true
	}
	mux.HandleFunc("POST /v3/login", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.logins++
		f.mu.Unlock()
		if r.URL.Query().Get("email") != "me@example.com" || r.URL.Query().Get("password") != "secret" || r.Header.Get("x-mmt-app") != "web" {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Invalid email or password"})
			return
		}
		ok(w, map[string]any{"token": "jwt-123", "auth_token": "at", "exp": time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("GET /v3/library/all", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			f.mu.Lock()
			defer f.mu.Unlock()
			ok(w, map[string]any{"total": len(f.entries), "entries": f.entries})
		}
	})
	mux.HandleFunc("GET /v3/queue", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			f.mu.Lock()
			defer f.mu.Unlock()
			ok(w, map[string]any{"entries": f.queue})
		}
	})
	mux.HandleFunc("GET /v3/library/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			ok(w, map[string]any{"url": f.srv.URL + "/files/" + r.PathValue("id") + "/video%20file.mp4",
				"data": map[string]any{"CloudFront-Expires": 1767225600, "CloudFront-Key-Pair-Id": "K1", "CloudFront-Signature": "sig"}})
		}
	})
	mux.HandleFunc("GET /files/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("CloudFront-Signature"); err != nil || c.Value != "sig" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		id := r.PathValue("id")
		f.mu.Lock()
		f.downloads[id]++
		broken := f.brokenOnce[id]
		f.brokenOnce[id] = false
		body := f.files[id]
		f.mu.Unlock()
		if broken {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		io.WriteString(w, body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) add(id int, entry map[string]any, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry["ID"] = id
	f.entries = append(f.entries, entry)
	f.files[fmt.Sprint(id)] = body
}

const layout = "2006-01-02 15:04:05"

func ready(name string) map[string]any {
	return map[string]any{"Name": name, "Created": time.Now().UTC().Add(-time.Hour).Format(layout),
		"Expires": time.Now().UTC().Add(6 * 24 * time.Hour).Format(layout + ".000"), "Quality": 2, "ProviderID": "hulu"}
}

type filed struct {
	src, name, kind string
	body            string
}

func setup(t *testing.T) (*Service, *fake, *[]filed) {
	t.Helper()
	st := storetest.Open(t)
	f := newFake(t)
	var got []filed
	dir := t.TempDir()
	s := &Service{Store: st, Dir: dir, Base: f.srv.URL + "/v3/", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		FileAway: func(ctx context.Context, src, name, kind string) (string, string, error) {
			b, err := os.ReadFile(src)
			if err != nil {
				return "", "", err
			}
			got = append(got, filed{src, name, kind, string(b)})
			os.Remove(src)
			if strings.Contains(name, "Unknown") {
				return "", "Box Office isn't sure what it is; check what it found", nil
			}
			return "/media/" + name, "", nil
		}}
	if err := SaveConfig(context.Background(), st, Config{On: true, Email: "me@example.com", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	return s, f, &got
}

func TestCheckDownloadsAndFiles(t *testing.T) {
	s, f, got := setup(t)
	ctx := context.Background()
	ep := ready("The Flash - s09e03 - Rogue Air")
	ep["Series"], ep["Season"], ep["Episode"] = "The Flash", "9", nil
	f.add(101, ep, "episode bytes")
	mv := ready("Arrival")
	mv["ReleaseYear"] = 2016
	f.add(102, mv, "movie bytes")
	f.add(103, ready("Unknown Special"), "special")
	// Still being recorded: no Created.
	f.add(104, map[string]any{"Name": "Not done", "Expires": time.Now().UTC().Add(time.Hour).Format(layout)}, "x")
	// Gone already.
	old := ready("Too late")
	old["Expires"] = time.Now().UTC().Add(-time.Hour).Format(layout)
	f.add(105, old, "x")
	f.queue = []map[string]any{{"ID": 900, "Name": "Next week"}}

	s.Check(ctx)
	if st := s.State(); st.Error != "" || st.Recording != 1 || st.Running {
		t.Fatalf("status = %+v", st)
	}
	want := map[string]filed{
		"The Flash - S09E03 - Rogue Air.mp4": {kind: "series", body: "episode bytes"},
		"Arrival (2016).mp4":                 {kind: "movie", body: "movie bytes"},
		"Unknown Special.mp4":                {kind: "auto", body: "special"},
	}
	if len(*got) != len(want) {
		t.Fatalf("filed %d, want %d: %+v", len(*got), len(want), *got)
	}
	for _, g := range *got {
		w, ok := want[g.name]
		if !ok || w.kind != g.kind || w.body != g.body {
			t.Errorf("filed %+v, want %+v", g, w)
		}
		if filepath.Dir(g.src) != filepath.Join(s.Dir, Folder) {
			t.Errorf("downloaded to %s", g.src)
		}
	}
	states := map[int64]string{}
	recs, _ := s.Store.PlayOnRecordings(ctx)
	for _, r := range recs {
		states[r.ID] = r.State
	}
	if states[101] != store.PlayOnFiled || states[102] != store.PlayOnFiled || states[103] != store.PlayOnCheck || states[105] != store.PlayOnExpired {
		t.Errorf("states = %v", states)
	}
	if _, ok := states[104]; ok {
		t.Error("a recording still being recorded was kept")
	}
	r, _ := s.Store.PlayOnRecording(ctx, 101)
	if r.FiledTo != "/media/The Flash - S09E03 - Rogue Air.mp4" || r.Size != int64(len("episode bytes")) || r.Season != 9 || r.Episode != 3 {
		t.Errorf("recording = %+v", r)
	}
	// The bytes count toward the data cap.
	use, _ := s.Store.DataUsage(ctx, store.DataDay(time.Now().AddDate(0, 0, -1)))
	if use[store.PlayOnUsage] != int64(len("episode bytes")+len("movie bytes")+len("special")) {
		t.Errorf("data usage = %v", use)
	}

	// The next check doesn't download them again.
	s.Check(ctx)
	if len(*got) != 3 {
		t.Errorf("downloaded again: %d", len(*got))
	}
	if f.logins != 1 {
		t.Errorf("signed in %d times; the token should have been kept", f.logins)
	}
}

func TestDownloadRetriesThenFails(t *testing.T) {
	s, f, got := setup(t)
	ctx := context.Background()
	f.add(7, ready("Arrival"), "movie")
	f.brokenOnce["7"] = true
	s.Check(ctx)
	r, _ := s.Store.PlayOnRecording(ctx, 7)
	if r.State != store.PlayOnNew || r.Tries != 1 || !strings.Contains(r.Note, "tried again") {
		t.Fatalf("after one failure: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, Folder, ".7.part")); !os.IsNotExist(err) {
		t.Error("the half-made file was left")
	}
	s.Check(ctx)
	if r, _ = s.Store.PlayOnRecording(ctx, 7); r.State != store.PlayOnFiled || len(*got) != 1 {
		t.Fatalf("after a retry: %+v", r)
	}

	// One that always fails gives up after MaxTries.
	f.add(8, ready("Broken"), "x")
	for range MaxTries {
		f.brokenOnce["8"] = true
		s.Check(ctx)
	}
	if r, _ = s.Store.PlayOnRecording(ctx, 8); r.State != store.PlayOnFailed {
		t.Fatalf("after %d failures: %+v", MaxTries, r)
	}
	s.Check(ctx)
	if f.downloads["8"] != MaxTries {
		t.Errorf("downloaded %d times", f.downloads["8"])
	}
	// Retry tries it again.
	if err := s.Store.RetryPlayOn(ctx, 8); err != nil {
		t.Fatal(err)
	}
	s.Check(ctx)
	if r, _ = s.Store.PlayOnRecording(ctx, 8); r.State != store.PlayOnFiled {
		t.Fatalf("after Retry: %+v", r)
	}
}

func TestSkippedAndCapFull(t *testing.T) {
	s, f, got := setup(t)
	ctx := context.Background()
	f.add(1, ready("Skip me"), "x")
	s.Store.SeePlayOnRecording(ctx, store.PlayOnRecording{ID: 1, Name: "Skip me"})
	s.Store.SetPlayOnState(ctx, 1, store.PlayOnSkipped, "", "", 0, false)
	f.add(2, ready("Wait"), "y")
	// The month's cap is used up.
	s.Store.SetSetting(ctx, "data_cap_gb", "1")
	s.Store.AddDataUsage(ctx, 1, 2e9)
	s.Check(ctx)
	if len(*got) != 0 {
		t.Fatalf("downloaded %+v", *got)
	}
	if st := s.State(); !strings.Contains(st.Paused, "data cap") {
		t.Errorf("paused = %q", st.Paused)
	}
	if r, _ := s.Store.PlayOnRecording(ctx, 1); r.State != store.PlayOnSkipped {
		t.Errorf("skipped one became %s", r.State)
	}
}

func TestOffAndBadLogin(t *testing.T) {
	s, f, _ := setup(t)
	ctx := context.Background()
	SaveConfig(ctx, s.Store, Config{Email: "me@example.com", Password: "secret"})
	s.Check(ctx)
	if f.logins != 0 {
		t.Error("checked PlayOn while it was off")
	}
	SaveConfig(ctx, s.Store, Config{On: true, Email: "me@example.com", Password: "wrong"})
	s.Check(ctx)
	if st := s.State(); st.Error != ErrBadLogin.Error() {
		t.Errorf("error = %q", st.Error)
	}
	if err := s.Client(Config{Email: "me@example.com", Password: "secret"}).Check(ctx); err != nil {
		t.Errorf("good login: %v", err)
	}
}

func TestName(t *testing.T) {
	for _, c := range []struct {
		r          store.PlayOnRecording
		name, kind string
	}{
		{store.PlayOnRecording{Name: "The Flash - Rogue Air", Series: "The Flash", Season: 9, Episode: 3}, "The Flash - S09E03 - Rogue Air", "series"},
		{store.PlayOnRecording{Name: "s01e02 - Pilot: Part 2", Series: "Lost", Season: 1, Episode: 2}, "Lost - S01E02 - Pilot Part 2", "series"},
		{store.PlayOnRecording{Name: "Behind the scenes", Series: "Lost"}, "Lost - Behind the scenes", "auto"},
		{store.PlayOnRecording{Name: "Mission: Impossible", Year: 1996}, "Mission Impossible (1996)", "movie"},
		{store.PlayOnRecording{Name: "Some/Thing"}, "Some Thing", "auto"},
		{store.PlayOnRecording{ID: 5}, "PlayOn recording 5", "auto"},
	} {
		if name, kind := Name(c.r); name != c.name || kind != c.kind {
			t.Errorf("Name(%+v) = %q, %q; want %q, %q", c.r, name, kind, c.name, c.kind)
		}
	}
}

func TestToRecording(t *testing.T) {
	r := toRecording(Recording{ID: 1, Name: "The Flash - s09e04 - Hear No Evil", Series: "None", BrowsePath: "Netflix | TV | The Flash | Season 9"})
	if r.Series != "The Flash" || r.Season != 9 || r.Episode != 4 {
		t.Errorf("toRecording = %+v", r)
	}
	if n, _ := Name(r); n != "The Flash - S09E04 - Hear No Evil" {
		t.Errorf("name = %q", n)
	}
}
