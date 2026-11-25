package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The list is saved as JSON or CSV, and a file is looked at without
// anything being added.
func TestTitlesFile(t *testing.T) {
	_, hs, _ := monitorServer(t)
	fetch := func(path string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", hs.URL+path, nil)
		req.Header.Set("X-Api-Key", "key")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	res, body := fetch("/api/v1/titles/export?format=csv")
	if res.StatusCode != 200 || !strings.HasPrefix(body, "kind,title,year,tmdb_id") ||
		!strings.Contains(res.Header.Get("Content-Disposition"), ".csv") {
		t.Fatalf("csv: %d %q %q", res.StatusCode, res.Header.Get("Content-Disposition"), body)
	}
	if res, body := fetch("/api/v1/titles/export"); res.StatusCode != 200 || !strings.Contains(body, `"titles": []`) {
		t.Fatalf("json: %d %q", res.StatusCode, body)
	}
	if res, _ := fetch("/api/v1/titles/export?format=xml"); res.StatusCode != 400 {
		t.Fatalf("xml: %d", res.StatusCode)
	}

	var out titleImportView
	sheet := "kind,title,tmdb_id\nmovie,The Matrix,603\nmovie,No id,\n"
	if code := call(t, hs, "POST", "/api/v1/titles/import?look=1", sheet, &out); code != 200 {
		t.Fatalf("look: %d", code)
	}
	// No root folder for movies yet, so neither can be added; the reasons say why.
	if out.Total != 2 || out.Queued || out.CantAdd != 2 || len(out.Skipped) != 2 || !strings.Contains(out.Skipped[0].Reason, "root folder") {
		t.Fatalf("look %+v", out)
	}
	var bad struct{ Error string }
	if code := call(t, hs, "POST", "/api/v1/titles/import", "title\nThe Matrix\n", &bad); code != 400 || !strings.Contains(bad.Error, "kind") {
		t.Fatalf("no kind column: %d %+v", code, bad)
	}
}
