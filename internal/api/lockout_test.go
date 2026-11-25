package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func signInFrom(t *testing.T, hs *httptest.Server, from, password string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if from != "" {
		req.Header.Set("X-Forwarded-For", from)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// Wrong passwords from one address lock that address out of the account,
// not the account's owner signing in from their own.
func TestSignInLockIsPerAddress(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.TrustedProxies, _ = ParseProxies("127.0.0.1, ::1")
	if code := call(t, hs, "POST", "/api/v1/setup", `{"username":"admin","password":"password1"}`, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	for i := 0; i < lockAfter; i++ {
		if code := signInFrom(t, hs, "203.0.113.9", "guess"); code != 401 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	// Locked there, even with the right password.
	if code := signInFrom(t, hs, "203.0.113.9", "password1"); code != 429 {
		t.Fatalf("the guesser: %d", code)
	}
	// The admin, from home, signs in.
	if code := signInFrom(t, hs, "198.51.100.7", "password1"); code != 200 {
		t.Fatalf("the admin: %d", code)
	}
	// A proxy's own hop in the list isn't the client.
	if code := signInFrom(t, hs, "203.0.113.9, 127.0.0.1", "password1"); code != 429 {
		t.Fatalf("through two proxies: %d", code)
	}
	// Without a trusted proxy the header counts for nothing: everyone is
	// the address that connected.
	s.TrustedProxies = nil
	for i := 0; i < lockAfter; i++ {
		signInFrom(t, hs, "192.0.2."+string(rune('1'+i)), "guess")
	}
	if code := signInFrom(t, hs, "192.0.2.200", "password1"); code != 429 {
		t.Fatalf("a made-up X-Forwarded-For got round the lock: %d", code)
	}
}

// Guesses sent at the same moment are counted one by one: no more than
// lockAfter of them get as far as the password.
func TestSignInLockCountsTogether(t *testing.T) {
	_, hs, _ := monitorServer(t)
	if code := call(t, hs, "POST", "/api/v1/setup", `{"username":"admin","password":"password1"}`, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code := signInFrom(t, hs, "", "guess")
			mu.Lock()
			codes[code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[401] != lockAfter || codes[429] != 12-lockAfter {
		t.Fatalf("answers %v", codes)
	}
}

// Counts that are over with are dropped, so the list doesn't grow
// without end under a guessing run.
func TestLockoutsForget(t *testing.T) {
	var l lockouts
	now := time.Now()
	old := l.entry("a", now)
	old.wrong(now.Add(-2 * lockForget))
	locked := l.entry("b", now)
	for i := 0; i < lockAfter; i++ {
		locked.wrong(now)
	}
	fresh := l.entry("c", now)
	fresh.wrong(now)
	l.entry("d", now)
	if _, ok := l.m["a"]; ok || l.len() != 3 {
		t.Fatalf("kept %d: %v", l.len(), l.m)
	}
	// Full: everything not locked right now goes.
	l.prune(now, true)
	if _, ok := l.m["b"]; !ok || l.len() != 1 {
		t.Fatalf("when full, kept %d", l.len())
	}
	if locked.until.Sub(now) != time.Minute {
		t.Fatalf("first lock %v", locked.until.Sub(now))
	}
	for i := 0; i < 40; i++ {
		locked.wrong(now)
	}
	if locked.until.Sub(now) != lockMost {
		t.Fatalf("longest lock %v", locked.until.Sub(now))
	}
}

func TestParseProxies(t *testing.T) {
	ps, err := ParseProxies("10.42.0.0/16, 192.168.1.5 ::1")
	if err != nil || len(ps) != 3 || ps[1].String() != "192.168.1.5/32" || ps[2].String() != "::1/128" {
		t.Fatalf("%v %v", ps, err)
	}
	if _, err := ParseProxies("the-proxy"); err == nil {
		t.Fatal("took a name")
	}
}
