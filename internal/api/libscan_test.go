package api

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/music"
)

// A music root folder's scan finds artists' folders and adds them where
// they are.
func TestScanMusicRoot(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Catalog = &catalog.Service{Store: s.Store, Log: s.Log}
	s.Music = &music.Service{Store: s.Store, MusicBrainz: fakeMusicBrainz(t)}
	s.LibScan = &libscan.Service{Store: s.Store, Catalog: s.Catalog, Music: s.Music, Log: s.Log}
	call(t, hs, "PUT", "/api/v1/settings", `{"mediaTypes":{"music":true}}`, nil)
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Radiohead", "OK Computer (1997)", "01 - Airbag.flac"), 300)
	var root struct{ ID int }
	if code := call(t, hs, "POST", "/api/v1/rootfolders", `{"kind":"music","path":"`+dir+`"}`, &root); code != 201 {
		t.Fatal(code)
	}
	base := fmt.Sprintf("/api/v1/rootfolders/%d/scan", root.ID)
	var sc libscan.Scan
	if code := call(t, hs, "GET", base, "", &sc); code != 200 || sc.Status != "" {
		t.Fatalf("before scanning: %d %+v", code, sc)
	}
	if code := call(t, hs, "POST", base, "", &sc); code != 202 {
		t.Fatal(code)
	}
	poll := func(status string) {
		t.Helper()
		for range 500 {
			call(t, hs, "GET", base, "", &sc)
			if sc.Status == status {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("stuck at %+v", sc)
	}
	poll(libscan.Ready)
	if len(sc.Found) != 1 || sc.Found[0].Match == nil || sc.Found[0].Match.MBID != artistID || !sc.Found[0].Sure || sc.Found[0].Files != 1 {
		t.Fatalf("found %+v", sc.Found)
	}
	body := fmt.Sprintf(`{"picks":[{"path":%q,"mbid":%q}],"monitored":true}`, sc.Found[0].Path, artistID)
	if code := call(t, hs, "POST", base+"/add", body, &sc); code != 202 {
		t.Fatal(code)
	}
	poll(libscan.Done)
	var list []artistOut
	call(t, hs, "GET", "/api/v1/artists", "", &list)
	if len(list) != 1 || list[0].Path != filepath.Join(dir, "Radiohead") {
		t.Fatalf("artists %+v", list)
	}
	if code := call(t, hs, "DELETE", base, "", nil); code != 204 {
		t.Fatal(code)
	}
}
