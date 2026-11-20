package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/otp"
	"github.com/yeixio/boxoffice/internal/passkey/passkeytest"
)

// byName gives the test server as a browser reaches it by name, since
// passkeys aren't made for number addresses.
func byName(hs *httptest.Server) (*httptest.Server, string) {
	named := &httptest.Server{URL: strings.Replace(hs.URL, "127.0.0.1", "localhost", 1)}
	return named, named.URL
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// addPasskey makes a passkey on a new authenticator for whoever v is
// signed in as, at base (/api/v1 or /api/portal).
func addPasskey(t *testing.T, v *visitor, base, origin, name string) *passkeytest.Key {
	t.Helper()
	var start struct {
		Challenge string
		RP        struct{ ID, Name string }
		User      struct{ ID, Name string }
		Exclude   []string
	}
	if code := v.do("POST", base+"/passkeys/start", "{}", &start); code != 200 || start.Challenge == "" || start.RP.ID != "localhost" {
		t.Fatalf("start: %d %+v", code, start)
	}
	k := passkeytest.New("localhost")
	reg := k.Register(start.Challenge, origin)
	body := asJSON(map[string]any{"id": reg.ID, "clientDataJSON": reg.ClientData, "authenticatorData": reg.AuthData, "publicKey": reg.PublicKey, "publicKeyAlgorithm": reg.Alg, "name": name})
	if code := v.do("POST", base+"/passkeys", body, nil); code != 200 {
		t.Fatalf("add: %d", code)
	}
	// The answer is good once.
	if code := v.do("POST", base+"/passkeys", body, nil); code != 400 {
		t.Fatalf("the same answer again: %d", code)
	}
	return k
}

// passkeySignIn signs v in with a passkey and gives the status.
func passkeySignIn(t *testing.T, v *visitor, base, origin string, k *passkeytest.Key) int {
	t.Helper()
	var c struct{ Challenge, RpId string }
	if code := v.do("POST", base+"/passkeys/challenge", "{}", &c); code != 200 || c.Challenge == "" {
		t.Fatalf("challenge: %d", code)
	}
	return v.do("POST", base+"/passkeys/login", asJSON(k.Assert(c.Challenge, origin)), nil)
}

func TestAdminPasskey(t *testing.T) {
	_, hs := portalServer(t)
	named, origin := byName(hs)
	admin := newVisitor(t, named)
	if code := admin.do("POST", "/api/v1/setup", `{"username":"admin","password":"password1"}`, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	// Not by a number address, and not with the API key alone.
	byNumber := newVisitor(t, hs)
	byNumber.do("POST", "/api/v1/login", `{"username":"admin","password":"password1"}`, nil)
	if code := byNumber.do("POST", "/api/v1/passkeys/start", "{}", nil); code != 400 {
		t.Fatalf("by a number address: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/passkeys/start", "{}", nil); code != 403 {
		t.Fatalf("with the API key: %d", code)
	}
	var status map[string]any
	newVisitor(t, named).do("GET", "/api/v1/status", "", &status)
	if status["passkeys"] != nil {
		t.Fatalf("offered before there is one: %+v", status)
	}

	k := addPasskey(t, admin, "/api/v1", origin, "Phone")
	var list struct {
		Passkeys []struct {
			ID   int64
			Name string
			Host string
		}
		Host string
	}
	if code := admin.do("GET", "/api/v1/passkeys", "", &list); code != 200 || len(list.Passkeys) != 1 || list.Passkeys[0].Name != "Phone" || list.Passkeys[0].Host != "localhost" {
		t.Fatalf("list: %d %+v", code, list)
	}
	// A second start names the one that's there, so it isn't made twice.
	var again struct{ Exclude []string }
	admin.do("POST", "/api/v1/passkeys/start", "{}", &again)
	if len(again.Exclude) != 1 {
		t.Fatalf("exclude: %+v", again)
	}

	guest := newVisitor(t, named)
	guest.do("GET", "/api/v1/status", "", &status)
	if status["passkeys"] != true {
		t.Fatalf("not offered: %+v", status)
	}
	if code := guest.do("GET", "/api/v1/movies", "", nil); code != 401 {
		t.Fatalf("before signing in: %d", code)
	}
	// Someone else's passkey, and the right one on the request site, don't do.
	if code := passkeySignIn(t, guest, "/api/v1", origin, passkeytest.New("localhost")); code != 401 {
		t.Fatalf("an unknown passkey: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true}`, nil); code != 200 {
		t.Fatalf("request site on: %d", code)
	}
	if code := passkeySignIn(t, guest, "/api/portal", origin, k); code != 401 {
		t.Fatalf("the admin's passkey on the request site: %d", code)
	}
	if code := guest.do("POST", "/api/v1/passkeys/login", asJSON(k.Assert("made-up", origin)), nil); code != 400 {
		t.Fatalf("a challenge nobody handed out: %d", code)
	}
	if code := passkeySignIn(t, guest, "/api/v1", origin, k); code != 200 {
		t.Fatalf("sign in: %d", code)
	}
	if code := guest.do("GET", "/api/v1/movies", "", nil); code != 200 {
		t.Fatalf("after signing in: %d", code)
	}
	admin.do("GET", "/api/v1/passkeys", "", &list)

	// Removed, it no longer signs in and isn't offered.
	if code := admin.do("DELETE", "/api/v1/passkeys/"+asJSON(list.Passkeys[0].ID), "", nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	late := newVisitor(t, named)
	if code := passkeySignIn(t, late, "/api/v1", origin, k); code != 401 {
		t.Fatalf("after removing it: %d", code)
	}
	status = nil
	late.do("GET", "/api/v1/status", "", &status)
	if status["passkeys"] != nil {
		t.Fatalf("still offered: %+v", status)
	}
}

func TestMemberPasskey(t *testing.T) {
	s, hs := portalServer(t)
	ctx := context.Background()
	named, origin := byName(hs)
	if code := call(t, hs, "PUT", "/api/v1/portal/settings", `{"enabled":true,"name":"Movie Night","localSignIn":true,"signUp":true}`, nil); code != 200 {
		t.Fatalf("request site on: %d", code)
	}
	alice := newVisitor(t, named)
	if code := alice.do("POST", "/api/portal/signup", `{"username":"alice","password":"password1"}`, nil); code != 200 {
		t.Fatalf("sign up: %d", code)
	}
	k := addPasskey(t, alice, "/api/portal", origin, "")
	var site map[string]any
	guest := newVisitor(t, named)
	guest.do("GET", "/api/portal/site", "", &site)
	if site["passkeys"] != true || site["member"] != nil {
		t.Fatalf("site: %+v", site)
	}
	// It's a member's: it doesn't open Box Office itself.
	if code := passkeySignIn(t, guest, "/api/v1", origin, k); code != 401 {
		t.Fatalf("a member's passkey as the admin: %d", code)
	}
	if code := passkeySignIn(t, guest, "/api/portal", origin, k); code != 200 {
		t.Fatalf("sign in: %d", code)
	}
	site = nil
	guest.do("GET", "/api/portal/site", "", &site)
	if m, _ := site["member"].(map[string]any); m["username"] != "alice" {
		t.Fatalf("signed in as %+v", site["member"])
	}
	var list struct{ Passkeys []struct{ ID int64 } }
	if code := guest.do("GET", "/api/portal/passkeys", "", &list); code != 200 || len(list.Passkeys) != 1 {
		t.Fatalf("list: %d %+v", code, list)
	}

	// A second passkey, removed again.
	k2 := addPasskey(t, alice, "/api/portal", origin, "Laptop")
	alice.do("GET", "/api/portal/passkeys", "", &list)
	if len(list.Passkeys) != 2 {
		t.Fatalf("two passkeys: %+v", list)
	}
	if code := alice.do("POST", "/api/portal/passkeys/"+asJSON(list.Passkeys[1].ID)+"/remove", "{}", nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code := passkeySignIn(t, newVisitor(t, named), "/api/portal", origin, k2); code != 401 {
		t.Fatalf("the removed one: %d", code)
	}

	// Turned off by an admin, the passkey stops working; removed, it goes.
	m, err := s.Store.MemberByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	m.Disabled = true
	if err := s.Store.UpdateMember(ctx, m); err != nil {
		t.Fatal(err)
	}
	if code := passkeySignIn(t, newVisitor(t, named), "/api/portal", origin, k); code != 401 {
		t.Fatalf("a turned-off account: %d", code)
	}
	if err := s.Store.DeleteMember(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Store.Passkeys(ctx, m.ID); len(left) != 0 {
		t.Fatalf("passkeys left behind: %+v", left)
	}
}

func TestSignInCode(t *testing.T) {
	s, hs := portalServer(t)
	admin := newVisitor(t, hs)
	if code := admin.do("POST", "/api/v1/setup", `{"username":"admin","password":"password1"}`, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code := call(t, hs, "POST", "/api/v1/code/start", "{}", nil); code != 403 {
		t.Fatalf("with the API key: %d", code)
	}
	var start struct{ Secret, URI string }
	if code := admin.do("POST", "/api/v1/code/start", "{}", &start); code != 200 || start.Secret == "" || !strings.HasPrefix(start.URI, "otpauth://totp/Box%20Office:admin?") {
		t.Fatalf("start: %d %+v", code, start)
	}
	login := func(extra string) (int, map[string]any) {
		var out map[string]any
		code := newVisitor(t, hs).do("POST", "/api/v1/login", `{"username":"admin","password":"password1"`+extra+`}`, &out)
		return code, out
	}
	// Until the app has shown it has the key, nothing is asked for.
	if code, _ := login(""); code != 200 {
		t.Fatalf("before it's confirmed: %d", code)
	}
	if code := admin.do("POST", "/api/v1/code/confirm", `{"code":"000000"}`, nil); code != 400 {
		t.Fatalf("a wrong code: %d", code)
	}
	now := otp.Counter(time.Now())
	first, _ := otp.At(start.Secret, now)
	var confirmed struct{ Recovery []string }
	if code := admin.do("POST", "/api/v1/code/confirm", asJSON(map[string]string{"code": first}), &confirmed); code != 200 || len(confirmed.Recovery) != 10 {
		t.Fatalf("confirm: %d %+v", code, confirmed)
	}
	var state struct {
		On           bool
		RecoveryLeft int
	}
	admin.do("GET", "/api/v1/code", "", &state)
	if !state.On || state.RecoveryLeft != 10 {
		t.Fatalf("state: %+v", state)
	}

	if code, out := login(""); code != 401 || out["needsCode"] != true {
		t.Fatalf("no code: %d %+v", code, out)
	}
	if code, out := login(`,"code":"000000"`); code != 401 || out["needsCode"] != true {
		t.Fatalf("a wrong code: %d %+v", code, out)
	}
	// A code works once: the one that confirmed it is spent.
	if code, _ := login(`,"code":"` + first + `"`); code != 401 {
		t.Fatalf("a used code: %d", code)
	}
	next, _ := otp.At(start.Secret, now+1)
	if code, _ := login(`,"code":"` + next[:3] + " " + next[3:] + `"`); code != 200 {
		t.Fatalf("the right code: %d", code)
	}
	// A wrong password never gets as far as the code.
	var out map[string]any
	if code := newVisitor(t, hs).do("POST", "/api/v1/login", `{"username":"admin","password":"wrong-one","code":"`+next+`"}`, &out); code != 401 || out["needsCode"] != nil {
		t.Fatalf("wrong password: %d %+v", code, out)
	}
	// A recovery code works, once.
	rc := `,"code":"` + strings.ToUpper(confirmed.Recovery[3]) + `"`
	if code, _ := login(rc); code != 200 {
		t.Fatalf("a recovery code: %d", code)
	}
	if code, _ := login(rc); code != 401 {
		t.Fatalf("a recovery code twice: %d", code)
	}
	admin.do("GET", "/api/v1/code", "", &state)
	if state.RecoveryLeft != 9 {
		t.Fatalf("recovery codes left: %d", state.RecoveryLeft)
	}

	// The way back in when everything's lost.
	s.SkipCode = true
	if code, _ := login(""); code != 200 {
		t.Fatalf("with the code skipped: %d", code)
	}
	s.SkipCode = false

	// Turning it off takes the password.
	if code := admin.do("POST", "/api/v1/code/off", `{"password":"nope-nope"}`, nil); code != 403 {
		t.Fatalf("off with a wrong password: %d", code)
	}
	if code := admin.do("POST", "/api/v1/code/off", `{"password":"password1"}`, nil); code != 204 {
		t.Fatalf("off: %d", code)
	}
	if code, _ := login(""); code != 200 {
		t.Fatalf("after turning it off: %d", code)
	}
}
