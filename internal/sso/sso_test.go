package sso_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/sso"
	"github.com/yeixio/boxoffice/internal/sso/ssotest"
)

const back = "https://boxoffice.example/api/sso/callback"

func setup(t *testing.T) (*sso.Client, *ssotest.Provider, sso.Settings) {
	p := ssotest.New(t)
	st := sso.Settings{Enabled: true, Issuer: p.URL + "/", ClientID: p.ClientID, ClientSecret: p.ClientSecret, Admins: []string{" Ellen@Example.com ", "ellen@example.com"}}
	if err := st.Check(); err != nil {
		t.Fatal(err)
	}
	if st.Issuer != p.URL || len(st.Admins) != 1 || st.Name == "" {
		t.Fatalf("tidied: %+v", st)
	}
	p.SignIn(map[string]any{"sub": "u-1", "email": "ellen@example.com", "email_verified": true, "preferred_username": "ellen", "name": "Ellen R", "groups": []string{"family", "admins"}})
	return &sso.Client{}, p, st
}

// trip goes to the provider and comes back, as a browser would.
func trip(t *testing.T, c *sso.Client, st sso.Settings) (sso.Identity, error) {
	t.Helper()
	ctx := context.Background()
	to, visit, err := c.Start(ctx, st, "admin", back)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(to)
	if q.Query().Get("code_challenge") == "" || q.Query().Get("nonce") != visit.Nonce || !strings.Contains(q.Query().Get("scope"), "openid") || !strings.Contains(q.Query().Get("scope"), "groups") {
		t.Fatalf("asks the provider with %s", to)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(to)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), back) || loc.Query().Get("state") != visit.State {
		t.Fatalf("sent back to %s", loc)
	}
	if loc.Query().Get("error") != "" {
		return sso.Identity{}, sso.ErrRefused
	}
	return c.Finish(ctx, st, visit, loc.Query().Get("code"))
}

func TestSignsInAndSaysWho(t *testing.T) {
	c, p, st := setup(t)
	id, err := trip(t, c, st)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "u-1" || id.Email != "ellen@example.com" || !id.EmailVerified || id.Username != "ellen" || id.Name != "Ellen R" || len(id.Groups) != 2 {
		t.Fatalf("%+v", id)
	}
	// A proof with only the subject: the rest is asked for.
	p.Thin(true)
	id, err = trip(t, c, st)
	if err != nil || id.Email != "ellen@example.com" || id.Username != "ellen" || len(id.Groups) != 2 {
		t.Fatalf("thin proof: %+v %v", id, err)
	}
	p.Thin(false)
	p.Refuse(true)
	if _, err := trip(t, c, st); !errors.Is(err, sso.ErrRefused) {
		t.Fatalf("refused: %v", err)
	}
}

func TestProofsThatArentBelieved(t *testing.T) {
	c, p, st := setup(t)
	for name, change := range map[string]func(map[string]any){
		"for another app":    func(c map[string]any) { c["aud"] = "someone-else" },
		"another app in azp": func(c map[string]any) { c["aud"] = []string{"boxoffice", "x"}; c["azp"] = "x" },
		"another sign-in":    func(c map[string]any) { c["nonce"] = "not-ours" },
		"another provider":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"out of date":        func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"no expiry":          func(c map[string]any) { delete(c, "exp") },
		"not yet":            func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() },
		"nobody":             func(c map[string]any) { delete(c, "sub") },
	} {
		p.Change(change)
		if id, err := trip(t, c, st); err == nil {
			t.Errorf("%s: believed %+v", name, id)
		}
	}
	p.Change(nil)
	// Not signed with the provider's key.
	for name, sign := range map[string]func(string) (string, []byte){
		"unsigned": func(string) (string, []byte) { return "none", nil },
		// Signed with something anyone knows: the client secret, or the
		// provider's public key used as a shared secret.
		"shared secret": func(s string) (string, []byte) {
			m := hmac.New(sha256.New, []byte(p.ClientSecret))
			m.Write([]byte(s))
			return "HS256", m.Sum(nil)
		},
		"garbage": func(string) (string, []byte) { return "RS256", make([]byte, 256) },
	} {
		p.SignWith(sign)
		if id, err := trip(t, c, st); err == nil {
			t.Errorf("%s: believed %+v", name, id)
		}
	}
	p.SignWith(nil)
	if _, err := trip(t, c, st); err != nil {
		t.Fatalf("a good one after the bad ones: %v", err)
	}
	// The wrong secret is said as it is, for whoever is setting it up.
	st.ClientSecret = "wrong"
	var se *sso.Error
	if _, err := trip(t, c, st); !errors.As(err, &se) || !strings.Contains(se.Message, "secret") {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestWhoIsLetIn(t *testing.T) {
	ellen := sso.Identity{Subject: "u-1", Email: "Ellen@example.com", EmailVerified: true, Username: "ellen", Groups: []string{"family"}}
	for _, c := range []struct {
		name  string
		st    sso.Settings
		id    sso.Identity
		admin bool
	}{
		{"nobody listed", sso.Settings{}, ellen, false},
		{"by email", sso.Settings{Admins: []string{"ellen@EXAMPLE.com"}}, ellen, true},
		{"by username", sso.Settings{Admins: []string{"Ellen"}}, ellen, true},
		{"by id", sso.Settings{Admins: []string{"u-1"}}, ellen, true},
		{"someone else", sso.Settings{Admins: []string{"ash@example.com"}}, ellen, false},
		{"by group", sso.Settings{AdminGroup: "family"}, ellen, true},
		{"another group", sso.Settings{AdminGroup: "admins"}, ellen, false},
		// An address the provider says isn't proven is anyone's to type in.
		{"unproven email", sso.Settings{Admins: []string{"ellen@example.com"}}, sso.Identity{Subject: "u-9", Email: "ellen@example.com"}, false},
		{"empty username", sso.Settings{Admins: []string{""}}, sso.Identity{Subject: "u-9"}, false},
	} {
		if got := c.st.Admin(c.id); got != c.admin {
			t.Errorf("%s: admin %v", c.name, got)
		}
	}
	if (sso.Settings{}).Member(ellen) || !(sso.Settings{Members: true}).Member(ellen) || !(sso.Settings{Members: true, MemberGroup: "family"}).Member(ellen) || (sso.Settings{Members: true, MemberGroup: "friends"}).Member(ellen) {
		t.Error("request site members")
	}
}

func TestSettingsAreChecked(t *testing.T) {
	for name, st := range map[string]sso.Settings{
		"no address": {Enabled: true, ClientID: "x"},
		"no client":  {Enabled: true, Issuer: "https://id.example"},
		"not a url":  {Enabled: true, Issuer: "id.example", ClientID: "x"},
	} {
		var se *sso.Error
		if err := st.Check(); !errors.As(err, &se) {
			t.Errorf("%s: %v", name, err)
		}
	}
	off := sso.Settings{}
	if err := off.Check(); err != nil {
		t.Errorf("off and empty: %v", err)
	}
	// An address that isn't a provider, and one that says it's another.
	c, p, st := setup(t)
	st.Issuer = p.URL + "/nothing-here"
	var se *sso.Error
	if err := c.Check(context.Background(), st); !errors.As(err, &se) {
		t.Errorf("not a provider: %v", err)
	}
}
