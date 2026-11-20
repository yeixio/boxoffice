package passkey_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/passkey"
	"github.com/yeixio/boxoffice/internal/passkey/passkeytest"
)

const (
	site   = "box.example.com"
	origin = "https://box.example.com"
)

func TestMakesAndSignsIn(t *testing.T) {
	for name, k := range map[string]*passkeytest.Key{"ec": passkeytest.New(site), "rsa": passkeytest.NewRSA(site)} {
		t.Run(name, func(t *testing.T) {
			c := passkey.NewChallenge()
			reg := k.Register(c, origin)
			if got := passkey.Challenge(reg.ClientData); got != c {
				t.Fatalf("challenge read as %q", got)
			}
			cred, err := passkey.Register(reg, c, origin, site)
			if err != nil {
				t.Fatal(err)
			}
			if cred.ID != reg.ID || cred.RPID != site || len(cred.PublicKey) == 0 {
				t.Fatalf("kept %+v", cred)
			}
			c2 := passkey.NewChallenge()
			if _, err := passkey.Verify(k.Assert(c2, origin), cred, c2, origin); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewPasskeysThatArentBelieved(t *testing.T) {
	c := passkey.NewChallenge()
	cases := map[string]struct {
		change func(k *passkeytest.Key, reg *passkey.Registration)
		origin string
		says   string
	}{
		"another challenge":   {func(k *passkeytest.Key, reg *passkey.Registration) { *reg = k.Register("other", origin) }, origin, "another try"},
		"another site's page": {nil, "https://evil.example.com", "not at"},
		"for another site": {func(k *passkeytest.Key, reg *passkey.Registration) {
			k.RPID = "evil.example.com"
			*reg = k.Register(c, origin)
		}, origin, "another site"},
		"a sign-in instead": {func(k *passkeytest.Key, reg *passkey.Registration) {
			k.Type = "webauthn.get"
			*reg = k.Register(c, origin)
		}, origin, "something else"},
		"inside another site": {func(k *passkeytest.Key, reg *passkey.Registration) {
			k.CrossOrigin = true
			*reg = k.Register(c, origin)
		}, origin, "inside another site"},
		"nobody checked":    {func(k *passkeytest.Key, reg *passkey.Registration) { k.Flags = 0x01; *reg = k.Register(c, origin) }, origin, "didn't check"},
		"another id":        {func(k *passkeytest.Key, reg *passkey.Registration) { reg.ID = "AAAA" }, origin, "doesn't name"},
		"no key":            {func(k *passkeytest.Key, reg *passkey.Registration) { reg.PublicKey = "AAAA" }, origin, "key can't be read"},
		"wrong kind of key": {func(k *passkeytest.Key, reg *passkey.Registration) { reg.Alg = passkey.RS256 }, origin, "kind of key"},
		"cut short":         {func(k *passkeytest.Key, reg *passkey.Registration) { reg.AuthData = reg.AuthData[:40] }, origin, "too short"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			k := passkeytest.New(site)
			reg := k.Register(c, tc.origin)
			if tc.change != nil {
				tc.change(k, &reg)
			}
			_, err := passkey.Register(reg, c, origin, site)
			var pe *passkey.Error
			if !errors.As(err, &pe) || !strings.Contains(pe.Message, tc.says) {
				t.Fatalf("got %v, want one that says %q", err, tc.says)
			}
		})
	}
}

func TestSignInsThatArentBelieved(t *testing.T) {
	k := passkeytest.New(site)
	c := passkey.NewChallenge()
	cred, err := passkey.Register(k.Register(c, origin), c, origin, site)
	if err != nil {
		t.Fatal(err)
	}
	other := passkeytest.New(site)
	other.ID = k.ID
	cases := map[string]struct {
		a    func() passkey.Assertion
		says string
	}{
		"another challenge":  {func() passkey.Assertion { return k.Assert("other", origin) }, "another try"},
		"another page":       {func() passkey.Assertion { return k.Assert(c, "https://evil.example.com") }, "not at"},
		"someone else's key": {func() passkey.Assertion { return other.Assert(c, origin) }, "signature isn't right"},
		"changed after": {func() passkey.Assertion {
			a := k.Assert(c, origin)
			a.AuthData = a.AuthData[:len(a.AuthData)-2] + "_w" // a count of 255
			return a
		}, "signature isn't right"},
		"nobody checked": {func() passkey.Assertion {
			k.Flags = 0x01
			defer func() { k.Flags = 0x05 }()
			return k.Assert(c, origin)
		}, "didn't check"},
		"a new passkey instead": {func() passkey.Assertion {
			k.Type = "webauthn.create"
			defer func() { k.Type = "" }()
			return k.Assert(c, origin)
		}, "something else"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := passkey.Verify(tc.a(), cred, c, origin)
			var pe *passkey.Error
			if !errors.As(err, &pe) || !strings.Contains(pe.Message, tc.says) {
				t.Fatalf("got %v, want one that says %q", err, tc.says)
			}
		})
	}
}

func TestCountMustGoUp(t *testing.T) {
	k := passkeytest.New(site)
	k.Count = 5
	c := passkey.NewChallenge()
	cred, err := passkey.Register(k.Register(c, origin), c, origin, site)
	if err != nil {
		t.Fatal(err)
	}
	n, err := passkey.Verify(k.Assert(c, origin), cred, c, origin)
	if err != nil || n != 6 {
		t.Fatalf("count %d: %v", n, err)
	}
	cred.SignCount = 9 // the copy has been used since
	if _, err := passkey.Verify(k.Assert(c, origin), cred, c, origin); !errors.Is(err, passkey.ErrCopied) {
		t.Fatalf("got %v, want ErrCopied", err)
	}
}

func TestRPID(t *testing.T) {
	for in, want := range map[string]string{"box.example.com": "box.example.com", "Box.Example.com:7272": "box.example.com", "localhost:7272": "localhost"} {
		if got, ok := passkey.RPID(in); got != want || !ok {
			t.Errorf("RPID(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"192.168.1.5:7272", "[::1]:7272", "10.0.0.2", ""} {
		if _, ok := passkey.RPID(in); ok {
			t.Errorf("RPID(%q) should not do", in)
		}
	}
}
