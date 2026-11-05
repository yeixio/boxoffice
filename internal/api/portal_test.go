package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func portalServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	db := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	cat := &catalog.Service{Store: db, Jobs: jobs.New(db, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	ctx := context.Background()
	for _, kind := range []string{"movie", "series"} {
		if _, err := db.AddRootFolder(ctx, kind, filepath.Join(t.TempDir(), kind)); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Store: db, Catalog: cat, Log: log, Version: "test", EnvAPIKey: "key",
		Portal: &portal.Service{Store: db, Catalog: cat, Log: log},
		Web:    fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html><head><title>Box Office</title></head><body></body></html>")}}}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

// visitor is a browser on the request site, with its own cookies.
type visitor struct {
	t  *testing.T
	hs *httptest.Server
	c  *http.Client
}

func newVisitor(t *testing.T, hs *httptest.Server) *visitor {
	jar, _ := cookiejar.New(nil)
	return &visitor{t, hs, &http.Client{Jar: jar}}
}

func (v *visitor) do(method, path, body string, out any) int {
	v.t.Helper()
	req, _ := http.NewRequest(method, v.hs.URL+path, strings.NewReader(body))
	req.Header.Set("X-Requested-With", "boxoffice")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := v.c.Do(req)
	if err != nil {
		v.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestRequestSite(t *testing.T) {
	s, hs := portalServer(t)
	ctx := context.Background()
	alice := newVisitor(t, hs)

	// Off until an admin turns it on.
	var site map[string]any
	alice.do("GET", "/api/portal/site", "", &site)
	if site["enabled"] != false || site["name"] != "Requests" {
		t.Fatalf("site %+v", site)
	}
	if code := alice.do("GET", "/api/portal/discover", "", nil); code != 404 {
		t.Fatalf("discover while off: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"  ","localSignIn":true}`, nil); code != 400 {
		t.Fatalf("no name: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","accent":"#2266aa","localSignIn":true,"quotaMovies":1,"host":"requests.example.com"}`, nil); code != 200 {
		t.Fatalf("turn on: %d", code)
	}

	// Admins make accounts; nobody can sign up on their own unless allowed.
	if code := alice.do("POST", "/api/portal/signup", `{"username":"mallory","password":"password1"}`, nil); code != 400 {
		t.Fatalf("sign up while not allowed: %d", code)
	}
	var made store.Member
	if code := call(t, hs, "POST", "/api/v1/portal/members", `{"username":"Alice","displayName":"Alice A","password":"password1"}`, &made); code != 201 || made.Username != "alice" {
		t.Fatalf("add member: %d %+v", code, made)
	}
	if code := call(t, hs, "POST", "/api/v1/portal/members", `{"username":"alice","password":"password1"}`, nil); code != 400 {
		t.Fatalf("same name twice: %d", code)
	}
	if code := alice.do("POST", "/api/portal/login", `{"username":"alice","password":"wrong-one"}`, nil); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code := alice.do("POST", "/api/portal/login", `{"username":"ALICE","password":"password1"}`, nil); code != 200 {
		t.Fatalf("sign in: %d", code)
	}
	alice.do("GET", "/api/portal/site", "", &site)
	if m, _ := site["member"].(map[string]any); m["name"] != "Alice A" || site["accent"] != "#2266aa" {
		t.Fatalf("signed in site %+v", site)
	}
	// A member's session doesn't open the admin API.
	if code := alice.do("GET", "/api/v1/movies", "", nil); code != 401 {
		t.Fatalf("member on the admin API: %d", code)
	}

	// Asking: waits for an admin, once per title, within the weekly limit.
	var v portal.View
	if code := alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":603,"note":"for Friday"}`, &v); code != 201 || v.State != portal.StatePending || v.Title != "The Matrix" {
		t.Fatalf("ask: %d %+v", code, v)
	}
	if code := alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":603}`, nil); code != 400 {
		t.Fatalf("ask twice: %d", code)
	}
	var e struct{ Error string }
	if code := alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":1001}`, &e); code != 400 || !strings.Contains(e.Error, "1 movies a week") {
		t.Fatalf("over the limit: %d %q", code, e.Error)
	}
	var counts map[string]any
	call(t, hs, "GET", "/api/v1/requests/counts", "", &counts)
	if counts["requests"] != 1.0 {
		t.Fatalf("counts %+v", counts)
	}
	var pending []portal.View
	call(t, hs, "GET", "/api/v1/requests?status=pending", "", &pending)
	if len(pending) != 1 || pending[0].Member != "Alice A" || pending[0].Note != "for Friday" {
		t.Fatalf("pending %+v", pending)
	}
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/requests/%d/approve", pending[0].ID), `{}`, &v); code != 200 || v.Status != store.RequestApproved || v.State != portal.StateSearching {
		t.Fatalf("approve: %d %+v", code, v)
	}
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/requests/%d/decline", pending[0].ID), `{}`, nil); code != 400 {
		t.Fatalf("decline after approving: %d", code)
	}
	m, err := s.Store.MovieByTMDB(ctx, 603)
	if err != nil || !m.Monitored {
		t.Fatalf("approved movie not on the list: %v %+v", err, m)
	}
	var mine []portal.View
	alice.do("GET", "/api/portal/requests", "", &mine)
	if len(mine) != 1 || mine[0].State != portal.StateSearching || mine[0].TitleID != m.ID {
		t.Fatalf("my requests %+v", mine)
	}

	// Problems can be reported once it's in the library.
	if code := alice.do("POST", "/api/portal/issues", `{"kind":"movie","tmdbId":603,"problem":"sound"}`, nil); code != 400 {
		t.Fatalf("report before there's a file: %d", code)
	}
	file := filepath.Join(m.Path, "The Matrix (1999).mkv")
	touch(t, file, 100)
	s.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: file, Size: 100, Quality: 4})
	alice.do("GET", "/api/portal/requests", "", &mine)
	if mine[0].State != portal.StateAvailable {
		t.Fatalf("on disk: %+v", mine[0])
	}
	if code := alice.do("POST", "/api/portal/issues", `{"kind":"movie","tmdbId":603,"problem":"nope"}`, nil); code != 400 {
		t.Fatalf("unknown problem: %d", code)
	}
	var issue store.Issue
	if code := alice.do("POST", "/api/portal/issues", `{"kind":"movie","tmdbId":603,"problem":"sound","message":"out of sync"}`, &issue); code != 201 || issue.Status != store.IssueOpen {
		t.Fatalf("report: %d %+v", code, issue)
	}
	var issues []issueRow
	call(t, hs, "GET", "/api/v1/issues?status=open", "", &issues)
	if len(issues) != 1 || issues[0].TitleID != m.ID || issues[0].Member != "Alice A" {
		t.Fatalf("issues %+v", issues)
	}
	call(t, hs, "PATCH", fmt.Sprintf("/api/v1/issues/%d", issue.ID), `{"resolved":true,"reply":"Swapped the file"}`, nil)
	var myIssues []store.Issue
	alice.do("GET", "/api/portal/issues", "", &myIssues)
	if len(myIssues) != 1 || myIssues[0].Status != store.IssueResolved || myIssues[0].Reply != "Swapped the file" {
		t.Fatalf("my issues %+v", myIssues)
	}

	// Approval can be automatic for one person.
	var bob store.Member
	call(t, hs, "POST", "/api/v1/portal/members", `{"username":"bob","password":"password2"}`, &bob)
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/portal/members/%d", bob.ID), `{"autoApprove":"on"}`, nil); code != 200 {
		t.Fatalf("auto approve bob: %d", code)
	}
	bv := newVisitor(t, hs)
	bv.do("POST", "/api/portal/login", `{"username":"bob","password":"password2"}`, nil)
	if code := bv.do("POST", "/api/portal/requests", `{"kind":"series","tmdbId":95396}`, &v); code != 201 || v.Status != store.RequestApproved {
		t.Fatalf("bob asks: %d %+v", code, v)
	}
	if _, err := s.Store.SeriesByTMDB(ctx, 95396); err != nil {
		t.Fatal("bob's show wasn't added")
	}
	// Bob can't see or cancel Alice's requests.
	if code := bv.do("DELETE", fmt.Sprintf("/api/portal/requests/%d", pending[0].ID), "", nil); code != 404 {
		t.Fatalf("cancel someone else's: %d", code)
	}
	// Turned off, bob is signed out.
	call(t, hs, "PATCH", fmt.Sprintf("/api/v1/portal/members/%d", bob.ID), `{"disabled":true}`, nil)
	if code := bv.do("GET", "/api/portal/requests", "", nil); code != 401 {
		t.Fatalf("turned-off member: %d", code)
	}

	// On its own host name the page says it's the request site.
	req, _ := http.NewRequest("GET", hs.URL+"/", nil)
	req.Host = "requests.example.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), `name="boxoffice-portal"`) {
		t.Fatalf("portal host page: %s", page)
	}
	res, _ = http.Get(hs.URL + "/request")
	page, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(page), `name="boxoffice-portal"`) || !strings.Contains(string(page), "<title>Box Office</title>") {
		t.Fatalf("plain page: %s", page)
	}

	// Removing someone removes what they asked for.
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/portal/members/%d", made.ID), "", nil); code != 204 {
		t.Fatalf("remove alice: %d", code)
	}
	if code := alice.do("GET", "/api/portal/requests", "", nil); code != 401 {
		t.Fatalf("removed member: %d", code)
	}
	all, _ := s.Store.Requests(ctx, "", 10)
	if len(all) != 1 {
		t.Fatalf("requests left %+v", all)
	}
}

// Someone with a Matinee account signs in with it: Box Office checks the
// password with Matinee, signs straight back out there, and makes them an
// account here the first time.
func TestRequestSiteMatineeSignIn(t *testing.T) {
	s, hs := portalServer(t)
	var logouts atomic.Int32
	matinee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["password"] != "letmein1" || in["deviceName"] != "Box Office request site" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": 7, "username": in["username"]}, "token": "t0k"})
		case "/api/v1/auth/logout":
			if r.Header.Get("Authorization") == "Bearer t0k" {
				logouts.Add(1)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer matinee.Close()
	body := fmt.Sprintf(`{"enabled":true,"name":"Requests","localSignIn":false,"matineeUrl":"%s/"}`, matinee.URL)
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", body, nil); code != 200 {
		t.Fatalf("settings: %d", code)
	}
	v := newVisitor(t, hs)
	if code := v.do("POST", "/api/portal/login", `{"username":"Ellen","password":"nope","matinee":true}`, nil); code != 401 {
		t.Fatalf("wrong Matinee password: %d", code)
	}
	if code := v.do("POST", "/api/portal/login", `{"username":"Ellen","password":"letmein1"}`, nil); code != 400 {
		t.Fatalf("local sign-in while it's off: %d", code)
	}
	var out struct{ Member memberView }
	if code := v.do("POST", "/api/portal/login", `{"username":"Ellen","password":"letmein1","matinee":true}`, &out); code != 200 || out.Member.Source != "matinee" || out.Member.Name != "Ellen" {
		t.Fatalf("Matinee sign-in: %d %+v", code, out)
	}
	if logouts.Load() != 1 {
		t.Fatalf("signed out of Matinee %d times", logouts.Load())
	}
	v2 := newVisitor(t, hs)
	v2.do("POST", "/api/portal/login", `{"username":"ellen","password":"letmein1","matinee":true}`, nil)
	ms, _ := s.Store.Members(context.Background())
	if len(ms) != 1 {
		t.Fatalf("members %+v", ms)
	}
	// Their password lives in Matinee.
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/portal/members/%d", ms[0].ID), `{"password":"password9"}`, nil); code != 400 {
		t.Fatalf("set a Matinee account's password: %d", code)
	}
}
