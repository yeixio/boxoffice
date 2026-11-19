package push

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlyPublicAddresses(t *testing.T) {
	for address, ok := range map[string]bool{
		"142.250.74.10:443":              true,
		"[2a00:1450:4001:82a::200a]:443": true,
		"127.0.0.1:443":                  false,
		"[::1]:443":                      false,
		"10.0.0.5:443":                   false,
		"192.168.1.20:7272":              false,
		"172.16.4.4:443":                 false,
		"169.254.169.254:80":             false,
		"100.100.100.100:443":            false,
		"[fd00::1]:443":                  false,
		"0.0.0.0:443":                    false,
	} {
		if err := publicOnly("tcp", address, nil); (err == nil) != ok {
			t.Errorf("%s: %v", address, err)
		}
	}
}

// A member can hand in any address as their browser's; Box Office doesn't
// call ones on this machine or the home network for them.
func TestWontCallInside(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
	called := false
	inside := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer inside.Close()
	res, err := newClient(true).Get(inside.URL)
	if err == nil {
		res.Body.Close()
	}
	if err == nil || called || !strings.Contains(err.Error(), "isn't a push service's address") {
		t.Fatalf("called %v, %v", called, err)
	}
}
