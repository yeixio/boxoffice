package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/importer"
)

// Files can be uploaded into the imports folder, or copied in from a
// folder on the server, and taken out again.
func TestImportPages(t *testing.T) {
	s, hs, _ := monitorServer(t)
	if code := call(t, hs, "GET", "/api/v1/import", "", nil); code != 409 {
		t.Fatalf("no importer: %d", code)
	}
	s.Imports = &importer.Service{Store: s.Store, Downloads: &downloader.Engine{Store: s.Store, Dir: t.TempDir()}, Log: s.Log}

	req, _ := http.NewRequest("PUT", hs.URL+"/api/v1/import/upload?name="+url.QueryEscape("Notes/read me.txt"), strings.NewReader("hello"))
	req.Header.Set("X-Api-Key", "key")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("upload: %v %v", res.StatusCode, err)
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.bin"), []byte("abc"), 0o644)
	var listing struct {
		Path    string        `json:"path"`
		Entries []importEntry `json:"entries"`
	}
	if code := call(t, hs, "GET", "/api/v1/import/browse?path="+url.QueryEscape(src), "", &listing); code != 200 || len(listing.Entries) != 1 || listing.Entries[0].Size != 3 {
		t.Fatalf("browse %d %+v", code, listing)
	}
	if code := call(t, hs, "POST", "/api/v1/import/copy", `{"paths":["`+filepath.Join(src, "a.bin")+`"]}`, nil); code != 202 {
		t.Fatalf("copy %d", code)
	}
	var st importer.State
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		call(t, hs, "GET", "/api/v1/import", "", &st)
		if len(st.Items) == 2 && !st.Copying.Running && st.Items[0].Status != importer.Guessing && st.Items[1].Status != importer.Guessing {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(st.Items) != 2 || st.Items[0].Name != "a.bin" || st.Items[1].Name != "Notes" || st.Items[1].Files != 1 {
		t.Fatalf("%+v", st.Items)
	}
	if code := call(t, hs, "POST", "/api/v1/import/run", `{"paths":[]}`, nil); code != 400 {
		t.Fatalf("ran with nothing ready: %d", code)
	}
	if code := call(t, hs, "DELETE", "/api/v1/import/item?path="+url.QueryEscape("imports/Notes"), "", nil); code != 204 {
		t.Fatalf("remove %d", code)
	}
	if _, err := os.Stat(filepath.Join(src, "a.bin")); err != nil {
		t.Fatal("the original went")
	}
}
