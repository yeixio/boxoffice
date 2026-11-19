package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/sso/ssotest"
	"github.com/yeixio/boxoffice/internal/store"
)

// browser follows a sign-in from Box Office to the provider and back, with
// its own cookies, and says where it ended up.
type browser struct {
	t  *testing.T
	hs *httptest.Server
	c  *http.Client
	// host, when set, is the host name the browser asks Box Office by.
	host string
}

func newBrowser(t *testing.T, hs *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, hs: hs}
	b.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return b
}

// visit opens a page and follows redirects; it gives the last status and
// the path and query it ended on.
func (b *browser) visit(path string) (int, string) {
	b.t.Helper()
	at := b.hs.URL + path
	for range 10 {
		req, _ := http.NewRequest("GET", at, nil)
		if b.host != "" && strings.HasPrefix(at, b.hs.URL) {
			req.Host = b.host
		}
		res, err := b.c.Do(req)
		if err != nil {
			b.t.Fatal(err)
		}
		res.Body.Close()
		loc := res.Header.Get("Location")
		if res.StatusCode < 300 || res.StatusCode >= 400 || loc == "" {
			u, _ := url.Parse(at)
			return res.StatusCode, u.RequestURI()
		}
		next, _ := url.Parse(at)
		next, _ = next.Parse(loc)
		// The provider sends the browser back to the host name it came by,
		// which in tests is only a name for the test server.
		if b.host != "" && next.Host == b.host {
			next.Host = strings.TrimPrefix(b.hs.URL, "http://")
		}
		at = next.String()
	}
	b.t.Fatal("too many redirects")
	return 0, ""
}

func (b *browser) get(path string, out any) int {
	b.t.Helper()
	v := &visitor{b.t, b.hs, b.c}
	return v.do("GET", path, "", out)
}

func ssoServer(t *testing.T) (*Server, *httptest.Server, *ssotest.Provider) {
	s, hs := portalServer(t)
	ctx := context.Background()
	if _, err := s.Store.CreateUser(ctx, "admin", "x"); err != nil {
		t.Fatal(err)
	}
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true,"host":"requests.example.com"}`, nil); code != 200 {
		t.Fatalf("turn the request site on: %d", code)
	}
	p := ssotest.New(t)
	p.SignIn(map[string]any{"sub": "u-1", "email": "ellen@example.com", "email_verified": true, "preferred_username": "Ellen", "name": "Ellen R", "groups": []string{"family"}})
	return s, hs, p
}

func TestSingleSignOnSettings(t *testing.T) {
	_, hs, p := ssoServer(t)
	var v struct {
		Enabled   bool
		Name      string
		Issuer    string
		HasSecret bool
		Admins    []string
		// The secret never comes back.
		ClientSecret string
		PortalHost   string
	}
	if code := call(t, hs, "GET", "/api/v1/sso", "", &v); code != 200 || v.Enabled || v.Admins == nil || v.PortalHost != "requests.example.com" {
		t.Fatalf("at first: %d %+v", code, v)
	}
	var e struct{ Error string }
	if code := call(t, hs, "PUT", "/api/v1/sso", `{"enabled":true,"issuer":"`+hs.URL+`","clientId":"boxoffice"}`, &e); code != 400 || !strings.Contains(e.Error, "OpenID Connect") && !strings.Contains(e.Error, "couldn't read") {
		t.Fatalf("an address that isn't a provider: %d %q", code, e.Error)
	}
	body := `{"enabled":true,"name":"Authentik","issuer":"` + p.URL + `/","clientId":"boxoffice","clientSecret":"s3cret","admins":["ellen@example.com"]}`
	if code := call(t, hs, "PUT", "/api/v1/sso", body, &v); code != 200 || !v.Enabled || !v.HasSecret || v.ClientSecret != "" || v.Issuer != p.URL {
		t.Fatalf("save: %d %+v", code, v)
	}
	// Saving again without the secret keeps it.
	if code := call(t, hs, "PUT", "/api/v1/sso", strings.Replace(body, `"clientSecret":"s3cret",`, "", 1), &v); code != 200 || !v.HasSecret {
		t.Fatalf("save without the secret: %d %+v", code, v)
	}
	// The sign-in page learns the name, and nothing else, without signing in.
	res, err := http.Get(hs.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw := new(strings.Builder)
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	raw.Write(buf[:n])
	if !strings.Contains(raw.String(), `"sso":"Authentik"`) || strings.Contains(raw.String(), "s3cret") || strings.Contains(raw.String(), "ellen") {
		t.Fatalf("status: %s", raw)
	}
	// Settings aren't for people who aren't signed in.
	if res, _ := http.Get(hs.URL + "/api/v1/sso"); res.StatusCode != 401 {
		t.Fatalf("settings signed out: %d", res.StatusCode)
	}
}

func saveSSO(t *testing.T, hs *httptest.Server, p *ssotest.Provider, more string) {
	t.Helper()
	body := `{"enabled":true,"name":"Authentik","issuer":"` + p.URL + `","clientId":"boxoffice","clientSecret":"s3cret"` + more + `}`
	if code := call(t, hs, "PUT", "/api/v1/sso", body, nil); code != 200 {
		t.Fatalf("save single sign-on: %d", code)
	}
}

func TestAdminSingleSignOn(t *testing.T) {
	_, hs, p := ssoServer(t)
	b := newBrowser(t, hs)
	// Off: there's nothing to start.
	if code, _ := b.visit("/api/sso/start?for=admin"); code != 404 {
		t.Fatalf("while off: %d", code)
	}
	saveSSO(t, hs, p, `,"admins":["ellen@example.com"]`)
	if code := b.get("/api/v1/movies", nil); code != 401 {
		t.Fatalf("before signing in: %d", code)
	}
	if code, at := b.visit("/api/sso/start?for=admin"); code != 200 || at != "/" {
		t.Fatalf("signing in ended at %d %s", code, at)
	}
	if code := b.get("/api/v1/movies", nil); code != 200 {
		t.Fatalf("after signing in: %d", code)
	}
	var status struct{ User store.User }
	b.get("/api/v1/status", &status)
	if status.User.Username != "admin" {
		t.Fatalf("signed in as %+v", status.User)
	}

	// Someone else at the provider isn't let in, and isn't signed in.
	p.SignIn(map[string]any{"sub": "u-2", "email": "ash@example.com", "preferred_username": "ash"})
	other := newBrowser(t, hs)
	if code, at := other.visit("/api/sso/start?for=admin"); code != 200 || at != "/?sso=notallowed" {
		t.Fatalf("someone else ended at %d %s", code, at)
	}
	if code := other.get("/api/v1/movies", nil); code != 401 {
		t.Fatalf("someone else: %d", code)
	}
	// Nor is someone who says no at the provider.
	p.Refuse(true)
	if code, at := other.visit("/api/sso/start?for=admin"); code != 200 || at != "/?sso=refused" {
		t.Fatalf("refused ended at %d %s", code, at)
	}
	p.Refuse(false)

	// An answer nobody asked for, or to another browser's question, is
	// turned away.
	if code, at := newBrowser(t, hs).visit("/api/sso/callback?code=abc&state=xyz"); code != 200 || at != "/?sso=failed" {
		t.Fatalf("an answer out of nowhere: %d %s", code, at)
	}
	// Box Office's own sign-in isn't on the request site's host name.
	onPortal := newBrowser(t, hs)
	onPortal.host = "requests.example.com"
	if code, _ := onPortal.visit("/api/sso/start?for=admin"); code != 404 {
		t.Fatalf("admin sign-in on the request site's host: %d", code)
	}
}

func TestMemberSingleSignOn(t *testing.T) {
	s, hs, p := ssoServer(t)
	ctx := context.Background()
	b := newBrowser(t, hs)
	saveSSO(t, hs, p, `,"admins":["boss@example.com"]`)
	// Not turned on for the request site yet.
	if code, _ := b.visit("/api/sso/start?for=member"); code != 404 {
		t.Fatalf("members while off: %d", code)
	}
	saveSSO(t, hs, p, `,"admins":["boss@example.com"],"members":true,"memberGroup":"family"`)
	var site struct {
		SSO    string
		Member *memberView
	}
	b.get("/api/portal/site", &site)
	if site.SSO != "Authentik" || site.Member != nil {
		t.Fatalf("site: %+v", site)
	}
	// A local account has Ellen's username already; she gets her own.
	if _, err := s.Store.CreateMember(ctx, store.Member{Username: "ellen", QuotaMovies: -1, QuotaShows: -1}, "x"); err != nil {
		t.Fatal(err)
	}
	if code, at := b.visit("/api/sso/start?for=member"); code != 200 || at != "/request" {
		t.Fatalf("signing in ended at %d %s", code, at)
	}
	b.get("/api/portal/site", &site)
	if site.Member == nil || site.Member.Username != "ellen2" || site.Member.Name != "Ellen R" || site.Member.Source != "sso" {
		t.Fatalf("signed in as %+v", site.Member)
	}
	// A member isn't an admin.
	if code := b.get("/api/v1/movies", nil); code != 401 {
		t.Fatalf("member on the admin API: %d", code)
	}
	if code, at := newBrowser(t, hs).visit("/api/sso/start?for=admin"); code != 200 || at != "/?sso=notallowed" {
		t.Fatalf("member signing in as admin: %d %s", code, at)
	}
	// The same person again is the same account, whatever they're called now.
	p.SignIn(map[string]any{"sub": "u-1", "preferred_username": "ellen-ripley", "groups": []string{"family"}})
	again := newBrowser(t, hs)
	again.visit("/api/sso/start?for=member")
	again.get("/api/portal/site", &site)
	if site.Member == nil || site.Member.Username != "ellen2" {
		t.Fatalf("second time: %+v", site.Member)
	}
	if all, _ := s.Store.Members(ctx); len(all) != 2 {
		t.Fatalf("members: %+v", all)
	}
	// Not in the group: not let in.
	p.SignIn(map[string]any{"sub": "u-3", "preferred_username": "burke", "groups": []string{"company"}})
	out := newBrowser(t, hs)
	if code, at := out.visit("/api/sso/start?for=member"); code != 200 || at != "/request?sso=notallowed" {
		t.Fatalf("outside the group: %d %s", code, at)
	}
	// Turned off by an admin: not let in either.
	m, _ := s.Store.MemberByName(ctx, "ellen2")
	m.Disabled = true
	if err := s.Store.UpdateMember(ctx, m); err != nil {
		t.Fatal(err)
	}
	p.SignIn(map[string]any{"sub": "u-1", "preferred_username": "ellen", "groups": []string{"family"}})
	if code, at := newBrowser(t, hs).visit("/api/sso/start?for=member"); code != 200 || at != "/request?sso=notallowed" {
		t.Fatalf("turned off: %d %s", code, at)
	}
	// On the request site's own host name it comes back to /.
	p.SignIn(map[string]any{"sub": "u-4", "preferred_username": "newt", "groups": []string{"family"}})
	onPortal := newBrowser(t, hs)
	onPortal.host = "requests.example.com"
	if code, at := onPortal.visit("/api/sso/start?for=member"); code != 200 || at != "/" {
		t.Fatalf("on its own host: %d %s", code, at)
	}
}

func TestSingleSignOnTrial(t *testing.T) {
	s, hs, p := ssoServer(t)
	ctx := context.Background()
	// Saved but not turned on: an admin can still try it.
	body := `{"enabled":false,"issuer":"` + p.URL + `","clientId":"boxoffice","clientSecret":"s3cret","admins":["ellen"]}`
	if code := call(t, hs, "PUT", "/api/v1/sso", body, nil); code != 200 {
		t.Fatalf("save: %d", code)
	}
	// Someone who isn't signed in can't.
	if code, _ := newBrowser(t, hs).visit("/api/sso/start?for=test"); code != 401 {
		t.Fatalf("trial signed out: %d", code)
	}
	u, _ := s.Store.FirstUser(ctx)
	tok, _ := s.Store.CreateSession(ctx, u.ID, sessionTTL)
	b := newBrowser(t, hs)
	at, _ := url.Parse(hs.URL)
	b.c.Jar.SetCookies(at, []*http.Cookie{{Name: cookieName, Value: tok, Path: "/"}})
	code, end := b.visit("/api/sso/start?for=test")
	if code != 200 || !strings.HasPrefix(end, "/settings?sso_test=") {
		t.Fatalf("trial ended at %d %s", code, end)
	}
	state := strings.TrimPrefix(end, "/settings?sso_test=")
	var found struct {
		Identity struct {
			Subject, Email, Username string
			Groups                   []string
		}
		Admin, Member bool
		Error         string
	}
	if code := newBrowser(t, hs).get("/api/v1/sso/test/"+state, nil); code != 401 {
		t.Fatalf("findings signed out: %d", code)
	}
	if code := b.get("/api/v1/sso/test/"+state, &found); code != 200 || found.Identity.Subject != "u-1" || found.Identity.Username != "Ellen" || !found.Admin || found.Member || len(found.Identity.Groups) != 1 {
		t.Fatalf("findings: %d %+v", code, found)
	}
	// They're given once.
	if code := b.get("/api/v1/sso/test/"+state, nil); code != 404 {
		t.Fatalf("findings twice: %d", code)
	}
	// A trial signs nobody in anywhere new.
	if all, _ := s.Store.Members(ctx); len(all) != 0 {
		t.Fatalf("a trial made a member: %+v", all)
	}
	// What went wrong is said, such as a wrong secret.
	call(t, hs, "PUT", "/api/v1/sso", strings.Replace(body, "s3cret", "wrong", 1), nil)
	_, end = b.visit("/api/sso/start?for=test")
	b.get("/api/v1/sso/test/"+strings.TrimPrefix(end, "/settings?sso_test="), &found)
	if !strings.Contains(found.Error, "secret") {
		t.Fatalf("wrong secret: %+v", found)
	}
}
