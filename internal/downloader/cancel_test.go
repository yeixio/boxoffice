package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// Taking a show off the list cancels its downloads and deletes what they
// fetched: queued, paused, and finished but waiting for the library. One
// being unpacked goes once that's done. Quarantined and failed ones, ones
// already in the library, and other titles' downloads stay.
func TestCancelFor(t *testing.T) {
	e, st := fileEngine(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	addShow := func(tmdb int64, name string) store.Series {
		s, err := st.AddSeries(ctx, store.Series{TMDBID: tmdb, Title: name, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, name)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	gone, other := addShow(1, "Gone"), addShow(2, "Other")
	add := func(show store.Series, folder, status string) store.Download {
		d, err := st.AddDownload(ctx, store.Download{Name: folder, Kind: "series", TitleID: show.ID})
		if err != nil {
			t.Fatal(err)
		}
		if folder != "" {
			write(t, filepath.Join(e.Dir, folder, "part.rar"), 5000)
			d.Folder = folder
			st.SaveDownloadProgress(ctx, d)
		}
		st.SetDownloadStatus(ctx, d.ID, status, "")
		return d
	}
	queued := add(gone, "", store.DownloadQueued)
	paused := add(gone, "Gone.S01E01", store.DownloadPaused)
	waiting := add(gone, "Gone.S01E02", store.DownloadCompleted)
	unpacking := add(gone, "Gone.S01E03", store.DownloadUnpacking)
	failed := add(gone, "Gone.S01E04", store.DownloadFailed)
	imported := add(gone, "Gone.S01E05", store.DownloadCompleted)
	st.SetDownloadImported(ctx, imported.ID, filepath.Join(lib, "Gone"))
	quarantined := add(gone, "Gone.S01E06", store.DownloadCompleted)
	st.SetDownloadQuarantined(ctx, quarantined.ID, "_quarantine/Gone.S01E06")
	others := add(other, "Other.S01E01", store.DownloadPaused)

	if err := st.DeleteSeries(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	removed, later, err := e.CancelFor(ctx, []string{"series"}, []int64{gone.ID})
	if err != nil || removed != 3 || later != 1 {
		t.Fatalf("removed %d, later %d: %v", removed, later, err)
	}
	isGone := func(d store.Download) bool {
		_, err := st.Download(ctx, d.ID)
		return err == store.ErrNotFound
	}
	folderThere := func(d store.Download) bool {
		_, err := os.Stat(filepath.Join(e.Dir, d.Folder))
		return err == nil
	}
	for _, d := range []store.Download{queued, paused, waiting} {
		if !isGone(d) || (d.Folder != "" && folderThere(d)) {
			t.Fatalf("%q wasn't cancelled, or its files are still there", d.Name)
		}
	}
	for _, d := range []store.Download{unpacking, failed, imported, quarantined, others} {
		if isGone(d) || !folderThere(d) {
			t.Fatalf("%q went", d.Name)
		}
	}

	// The one unpacking goes once it's done.
	st.SetDownloadStatus(ctx, unpacking.ID, store.DownloadCompleted, "")
	e.dropOrphan(ctx, unpacking.ID)
	if !isGone(unpacking) || folderThere(unpacking) {
		t.Fatal("the one that was unpacking stayed")
	}
	// Other titles' downloads never do.
	e.dropOrphan(ctx, others.ID)
	if isGone(others) {
		t.Fatal("another show's download went")
	}
}

// A download under way stops when its show is taken off the list, and
// what it fetched is deleted; a queued one whose title went is never
// started.
func TestCancelWhileDownloading(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 20}) // slow enough to catch
	ctx := context.Background()
	profiles, _ := st.Profiles(ctx)
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1, Title: "Gone", ProfileID: profiles[0].ID, RootFolder: t.TempDir(), Path: "/x/Gone"})
	if err != nil {
		t.Fatal(err)
	}
	run(t, e)
	d, err := e.Add(ctx, store.Download{Name: "Gone.S01", Kind: "series", TitleID: show.ID}, data)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadDownloading)
	if err := st.DeleteSeries(ctx, show.ID); err != nil {
		t.Fatal(err)
	}
	if removed, _, err := e.CancelFor(ctx, []string{"series"}, []int64{show.ID}); err != nil || removed != 1 {
		t.Fatalf("removed %d: %v", removed, err)
	}
	if _, err := st.Download(ctx, d.ID); err != store.ErrNotFound {
		t.Fatalf("still listed: %v", err)
	}
	// Nothing's left but the (now empty) folder NZBs are kept in.
	entries, _ := os.ReadDir(e.Dir)
	for _, x := range entries {
		if x.Name() != nzbDir {
			t.Fatalf("%s left in the downloads folder", x.Name())
		}
	}
	if nzbs, _ := os.ReadDir(filepath.Join(e.Dir, nzbDir)); len(nzbs) > 0 {
		t.Fatalf("its NZB is still there: %s", nzbs[0].Name())
	}

	// One left queued for a title that's gone (added just as it went) is
	// dropped, not fetched.
	enable(t, st, Settings{})
	late, err := st.AddDownload(ctx, store.Download{Name: "Gone.S02", Kind: "series", TitleID: show.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.Wake()
	for range 200 {
		if _, err := st.Download(ctx, late.ID); err == store.ErrNotFound {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := st.Download(ctx, late.ID); err != store.ErrNotFound {
		t.Fatal("a download for a removed show stayed queued")
	}
	if n := srv.Fetches.Load(); n == 0 {
		t.Fatal("never fetched anything")
	}
}

// aMovie puts a movie on the list for a download to be for: one for a
// title that isn't there is dropped before it starts.
func aMovie(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 99, Title: "Some Movie", ProfileID: profiles[0].ID, RootFolder: t.TempDir(), Path: "/x/Some Movie"})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}
