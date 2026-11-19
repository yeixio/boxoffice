package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressed(t *testing.T) {
	big := strings.Repeat(`{"title":"A Trip to the Moon"},`, 200)
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, big[:100])
		io.WriteString(w, big[100:])
	})
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, big)
	})
	mux.HandleFunc("/none", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "starting\n")
		http.NewResponseController(w).Flush()
		io.WriteString(w, big)
	})
	hs := httptest.NewServer(compressed(mux))
	defer hs.Close()
	// The transport would undo gzip by itself; ask by hand to see it.
	get := func(path, accept string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", hs.URL+path, nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		res, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body io.Reader = res.Body
		if res.Header.Get("Content-Encoding") == "gzip" {
			z, err := gzip.NewReader(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			body = z
		}
		b, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		return res, string(b)
	}
	res, body := get("/json", "gzip, br")
	if res.Header.Get("Content-Encoding") != "gzip" || body != big || res.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("json: %v, %d bytes", res.Header, len(body))
	}
	if res, body := get("/json", ""); res.Header.Get("Content-Encoding") != "" || body != big {
		t.Fatalf("json, not asked for: %v", res.Header)
	}
	if res, body := get("/json", "gzip;q=0"); res.Header.Get("Content-Encoding") != "" || body != big {
		t.Fatalf("json, gzip refused: %v", res.Header)
	}
	if res, body := get("/short", "gzip"); res.Header.Get("Content-Encoding") != "" || body != `{"ok":true}` || res.StatusCode != http.StatusTeapot {
		t.Fatalf("short: %d %v %q", res.StatusCode, res.Header, body)
	}
	if res, body := get("/file", "gzip"); res.Header.Get("Content-Encoding") != "" || body != big {
		t.Fatalf("file: %v", res.Header)
	}
	if res, body := get("/none", "gzip"); res.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("no content: %d %q", res.StatusCode, body)
	}
	if res, body := get("/stream", "gzip"); body != "starting\n"+big {
		t.Fatalf("stream: %v, %d bytes", res.Header, len(body))
	}
}
