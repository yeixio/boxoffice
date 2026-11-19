package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yeixio/boxoffice/internal/notify"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/push"
	"github.com/yeixio/boxoffice/internal/push/pushtest"
	"github.com/yeixio/boxoffice/internal/store"
)

// pushServer is a server whose notifications go to devices as they would
// when it's running, but before the call that caused them returns.
func pushServer(t *testing.T) (*Server, func(method, path, body string, out any) int, *visitor) {
	t.Helper()
	s, hs := portalServer(t)
	ctx := context.Background()
	s.Push = &push.Service{Store: s.Store, AnyAddress: true}
	notes := &notify.Recorder{Store: s.Store, After: func(n store.Notification) { s.Push.Notify(ctx, n) }}
	s.Portal.Notify = notes
	s.Portal.TellMember = func(ctx context.Context, memberID int64, title, body string) {
		s.Push.Member(ctx, memberID, push.Message{Title: title, Body: body, URL: push.MemberPage})
	}
	admin := func(method, path, body string, out any) int { return call(t, hs, method, path, body, out) }
	if code := admin("PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true,"host":"requests.example.com"}`, nil); code != 200 {
		t.Fatalf("turn the request site on: %d", code)
	}
	if code := admin("POST", "/api/v1/portal/members", `{"username":"alice","displayName":"Alice","password":"password1"}`, nil); code != 201 {
		t.Fatalf("add member: %d", code)
	}
	alice := newVisitor(t, hs)
	if code := alice.do("POST", "/api/portal/login", `{"username":"alice","password":"password1"}`, nil); code != 200 {
		t.Fatalf("sign in: %d", code)
	}
	return s, admin, alice
}

type pushInfo struct {
	PublicKey string
	Topics    []string
	Devices   []store.PushDevice
}

func TestAdminPushDevices(t *testing.T) {
	_, admin, alice := pushServer(t)
	var info pushInfo
	if code := admin("GET", "/api/v1/push", "", &info); code != 200 || len(info.PublicKey) != 87 || len(info.Topics) != 3 || len(info.Devices) != 0 {
		t.Fatalf("info: %d %+v", code, info)
	}
	phone := pushtest.New(t)
	sub := strings.TrimSuffix(phone.JSON(), "}") + `,"topics":["requests","problems"]}`
	var d store.PushDevice
	if code := admin("POST", "/api/v1/push/devices", sub, &d); code != 200 || d.ID == 0 || len(d.Topics) != 2 {
		t.Fatalf("add: %d %+v", code, d)
	}
	if code := admin("POST", "/api/v1/push/devices", `{"endpoint":"https://push.example/x","keys":{"p256dh":"AAAA","auth":"AAAA"}}`, nil); code != 400 {
		t.Fatalf("bad keys: %d", code)
	}
	// Signed in to the request site isn't enough for the admins' devices.
	if code := alice.do("GET", "/api/v1/push", "", nil); code != 401 {
		t.Fatalf("member reading admin devices: %d", code)
	}
	if code := admin("POST", fmt.Sprintf("/api/v1/push/devices/%d/test", d.ID), "", nil); code != 204 {
		t.Fatalf("test: %d", code)
	}
	if got := phone.Got(); len(got) != 1 || !strings.Contains(got[0]["body"], "working") {
		t.Fatalf("test message: %v %v", got, phone.Problems())
	}
	// A new request reaches the phone, and opens the Requests page.
	if code := alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":603}`, nil); code != 201 {
		t.Fatalf("ask: %d", code)
	}
	if got := phone.Got(); len(got) != 2 || got[1]["title"] != "Alice asked for The Matrix (1999)" || got[1]["url"] != "/requests" {
		t.Fatalf("after a request: %v", got)
	}
	// The browser dropped it: testing says so, and it leaves the list.
	phone.Answer(410)
	var e struct{ Error string }
	if code := admin("POST", fmt.Sprintf("/api/v1/push/devices/%d/test", d.ID), "", &e); code != 410 || !strings.Contains(e.Error, "Turn them on again") {
		t.Fatalf("gone: %d %q", code, e.Error)
	}
	admin("GET", "/api/v1/push", "", &info)
	if len(info.Devices) != 0 {
		t.Fatalf("still listed: %+v", info.Devices)
	}
	if code := admin("DELETE", fmt.Sprintf("/api/v1/push/devices/%d", d.ID), "", nil); code != 404 {
		t.Fatalf("remove a removed one: %d", code)
	}
}

func TestMemberPushDevices(t *testing.T) {
	s, admin, alice := pushServer(t)
	ctx := context.Background()
	var info pushInfo
	if code := alice.do("GET", "/api/portal/push", "", &info); code != 200 || len(info.PublicKey) != 87 {
		t.Fatalf("key: %d %+v", code, info)
	}
	phone := pushtest.New(t)
	if code := alice.do("POST", "/api/portal/push", phone.JSON(), nil); code != 204 {
		t.Fatalf("add: %d", code)
	}
	if code := alice.do("POST", "/api/portal/push/test", phone.JSON(), nil); code != 204 || len(phone.Got()) != 1 {
		t.Fatalf("test: %d %v", code, phone.Got())
	}
	// It isn't one of the admins' devices, and admins' routes don't touch it.
	admin("GET", "/api/v1/push", "", &info)
	if len(info.Devices) != 0 {
		t.Fatalf("listed with the admins': %+v", info.Devices)
	}
	mine, _ := s.Store.PushDevices(ctx, 1)
	if len(mine) != 1 {
		t.Fatalf("alice's devices: %+v", mine)
	}
	if code := admin("DELETE", fmt.Sprintf("/api/v1/push/devices/%d", mine[0].ID), "", nil); code != 404 {
		t.Fatalf("admin route on a member's device: %d", code)
	}
	// Nobody signed in can't add one.
	if code := newVisitor(t, alice.hs).do("POST", "/api/portal/push", phone.JSON(), nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}

	// Declined, with the reason.
	alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":603}`, nil)
	var pending []portal.View
	admin("GET", "/api/v1/requests?status=pending", "", &pending)
	if code := admin("POST", fmt.Sprintf("/api/v1/requests/%d/decline", pending[0].ID), `{"reply":"We have it already"}`, nil); code != 200 {
		t.Fatalf("decline: %d", code)
	}
	got := phone.Got()
	if len(got) != 2 || got[1]["title"] != "Your request for The Matrix (1999) was declined" || got[1]["body"] != "We have it already" || got[1]["url"] != push.MemberPage {
		t.Fatalf("declined: %v", got)
	}
	// Approved, and then ready.
	alice.do("POST", "/api/portal/requests", `{"kind":"movie","tmdbId":1001}`, nil)
	admin("GET", "/api/v1/requests?status=pending", "", &pending)
	if code := admin("POST", fmt.Sprintf("/api/v1/requests/%d/approve", pending[0].ID), `{}`, nil); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	if got = phone.Got(); len(got) != 3 || !strings.HasSuffix(got[2]["title"], "was approved") {
		t.Fatalf("approved: %v", got)
	}
	m, err := s.Store.MovieByTMDB(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	(&notify.Recorder{Store: s.Store, After: func(n store.Notification) { s.Push.Notify(ctx, n) }}).Download(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "x"}, store.EventImported, "")
	if got = phone.Got(); len(got) != 4 || !strings.HasSuffix(got[3]["title"], "is ready") {
		t.Fatalf("ready: %v", got)
	}
	// Turned off on the device.
	if code := alice.do("POST", "/api/portal/push/remove", phone.JSON(), nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code := alice.do("POST", "/api/portal/push/test", phone.JSON(), nil); code != 404 {
		t.Fatalf("test after removing: %d", code)
	}
}

func TestInstallManifest(t *testing.T) {
	_, hs := portalServer(t)
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true,"host":"requests.example.com"}`, nil); code != 200 {
		t.Fatalf("turn the request site on: %d", code)
	}
	get := func(path, host string) (string, map[string]any) {
		req, _ := http.NewRequest("GET", hs.URL+path, nil)
		if host != "" {
			req.Host = host
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		return res.Header.Get("Content-Type"), m
	}
	for _, c := range []struct{ path, host, name, start string }{
		{"/manifest.webmanifest", "", "Box Office", "/"},
		{"/manifest.webmanifest?for=request", "", "Movie Night", "/request"},
		{"/manifest.webmanifest", "requests.example.com", "Movie Night", "/"},
	} {
		ct, m := get(c.path, c.host)
		icons, _ := m["icons"].([]any)
		if !strings.HasPrefix(ct, "application/manifest+json") || m["name"] != c.name || m["start_url"] != c.start || m["display"] != "standalone" || len(icons) != 3 {
			t.Errorf("%s on %q: %s %v", c.path, c.host, ct, m)
		}
	}
}

func TestDeviceName(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36":                   "Chrome on Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0":                                                        "Firefox on Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0":     "Edge on Mac",
		"curl/8.5.0": "",
	} {
		if got := deviceName(ua); got != want {
			t.Errorf("%s: %q", ua, got)
		}
	}
}

// The request site's pages install under the request site's name, wherever
// it's served; Box Office's own pages under Box Office's.
func TestPagePointsAtItsManifest(t *testing.T) {
	s, _ := portalServer(t)
	s.Web = fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html><head><link rel="manifest" href="/manifest.webmanifest" /></head><body></body></html>`)}}
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true,"host":"requests.example.com"}`, nil); code != 200 {
		t.Fatalf("turn the request site on: %d", code)
	}
	for _, c := range []struct {
		path, host string
		request    bool
	}{
		{"/", "", false},
		{"/movies/3", "", false},
		{"/requests", "", false},
		{"/request", "", true},
		{"/request/requests", "", true},
		{"/", "requests.example.com", true},
	} {
		req, _ := http.NewRequest("GET", hs.URL+c.path, nil)
		if c.host != "" {
			req.Host = c.host
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if got := strings.Contains(string(b), `href="/manifest.webmanifest?for=request"`); got != c.request || !strings.Contains(string(b), "manifest.webmanifest") {
			t.Errorf("%s on %q: %s", c.path, c.host, b)
		}
	}
}
