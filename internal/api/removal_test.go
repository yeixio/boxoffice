package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

func touch(t *testing.T, path string, size int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func there(path string) bool { _, err := os.Stat(path); return err == nil }

// Deleting a season's files frees them for good, only as shown, keeps the
// episodes on the list, and can stop watching for them and block the
// release they came from.
func TestDeleteSeasonFiles(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	root := t.TempDir()
	profiles, _ := s.Store.Profiles(ctx)
	show, err := s.Store.AddSeries(ctx, store.Series{TMDBID: 1, Title: "Show", ProfileID: profiles[0].ID, RootFolder: root, Path: filepath.Join(root, "Show"),
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}, {Number: 2, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Episode{{SeriesID: show.ID, Season: 1, Number: 1, Monitored: true}, {SeriesID: show.ID, Season: 1, Number: 2, Monitored: true},
		{SeriesID: show.ID, Season: 2, Number: 1, Monitored: true}} {
		s.Store.UpsertEpisode(ctx, e)
	}
	eps, _ := s.Store.Episodes(ctx, show.ID)
	var paths []string
	for _, e := range eps {
		p := filepath.Join(show.Path, fmt.Sprintf("Season %02d", e.Season), fmt.Sprintf("Show - S%02dE%02d.mkv", e.Season, e.Number))
		touch(t, p, 1000)
		s.Store.AddFile(ctx, store.File{Kind: "episode", OwnerID: e.ID, Path: p, Size: 1000})
		paths = append(paths, p)
	}
	touch(t, filepath.Join(show.Path, "Season 01", "Show - S01E01.en.srt"), 10)
	d, _ := s.Store.AddDownload(ctx, store.Download{Name: "Show.S01.1080p-GRP", Kind: "series", TitleID: show.ID, EpisodeIDs: []int64{eps[0].ID, eps[1].ID}, Indexer: "nzb.su"})
	s.Store.SetDownloadImported(ctx, d.ID, filepath.Join(show.Path, "Season 01"))
	s.Store.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, "")

	var plan fileRemoval
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/series/%d/seasons/1/files/removal", show.ID), "", &plan); code != 200 {
		t.Fatal(code)
	}
	if plan.Problem != "" || plan.Count != 3 || plan.Size != 2010 || !plan.Watched || len(plan.Releases) != 1 || plan.Releases[0].Name != "Show.S01.1080p-GRP" {
		t.Fatalf("plan %+v", plan)
	}

	base := fmt.Sprintf("/api/v1/series/%d/seasons/1/files?unmonitor=true&block=true", show.ID)
	if code := call(t, hs, "DELETE", base+"&expectFiles=2&expectSize=2000", "", nil); code != 409 {
		t.Fatalf("a plan the person didn't see went ahead: %d", code)
	}
	if code := call(t, hs, "DELETE", base, "", nil); code != 400 {
		t.Fatalf("no expected size: %d", code)
	}
	if !there(paths[0]) {
		t.Fatal("deleted without agreement")
	}
	if code := call(t, hs, "DELETE", base+"&expectFiles=3&expectSize=2010", "", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if there(paths[0]) || there(paths[1]) || there(filepath.Join(show.Path, "Season 01")) || !there(paths[2]) {
		t.Fatal("wrong files deleted")
	}
	after, _ := s.Store.Episodes(ctx, show.ID)
	if len(after) != 3 || after[0].File != nil || after[0].Monitored || after[2].File == nil || !after[2].Monitored {
		t.Fatalf("episodes after: %+v", after)
	}
	if t2, _ := s.Store.Series(ctx, show.ID); t2.Seasons[0].Monitored {
		t.Fatal("season still watched")
	}
	if blocked, _ := s.Store.BlockedFor(ctx, "series", show.ID); len(blocked) != 1 || blocked[0].SourceTitle != "Show.S01.1080p-GRP" {
		t.Fatalf("blocklist %v", blocked)
	}
}

// Removing a movie keeps its folder unless deleting it is asked for.
func TestRemoveMovieWithFiles(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	root := t.TempDir()
	profiles, _ := s.Store.Profiles(ctx)
	add := func(tmdb int64, name string) store.Movie {
		m, err := s.Store.AddMovie(ctx, store.Movie{TMDBID: tmdb, Title: name, ProfileID: profiles[0].ID, RootFolder: root, Path: filepath.Join(root, name)})
		if err != nil {
			t.Fatal(err)
		}
		touch(t, filepath.Join(m.Path, name+".mkv"), 500)
		return m
	}
	keep, gone := add(1, "Keep"), add(2, "Gone")

	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/movies/%d", keep.ID), "", nil); code != 204 {
		t.Fatal(code)
	}
	if !there(keep.Path) {
		t.Fatal("files deleted without being asked")
	}

	var plan struct {
		Folder string `json:"folder"`
		Count  int    `json:"count"`
		Size   int64  `json:"size"`
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/movies/%d/removal", gone.ID), "", &plan)
	if plan.Folder != gone.Path || plan.Count != 1 || plan.Size != 500 {
		t.Fatalf("plan %+v", plan)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/movies/%d?deleteFiles=true&expectFiles=1&expectSize=500", gone.ID), "", nil); code != 204 {
		t.Fatal(code)
	}
	if there(gone.Path) || !there(root) {
		t.Fatal("folder not deleted, or too much")
	}
	if _, err := s.Store.Movie(ctx, gone.ID); err == nil {
		t.Fatal("still listed")
	}
}

// Removing a show cancels its downloads, deleting what they fetched, and
// the Remove dialog's plan says how many there are; files in the library
// stay unless asked for.
func TestRemoveSeriesCancelsDownloads(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: t.TempDir(), Log: s.Log}
	root := t.TempDir()
	profiles, _ := s.Store.Profiles(ctx)
	show, err := s.Store.AddSeries(ctx, store.Series{TMDBID: 1, Title: "Gone", ProfileID: profiles[0].ID, RootFolder: root, Path: filepath.Join(root, "Gone")})
	if err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(show.Path, "Gone S01E01.mkv"), 500)
	d, err := s.Store.AddDownload(ctx, store.Download{Name: "Gone.S01E02", Kind: "series", TitleID: show.ID})
	if err != nil {
		t.Fatal(err)
	}
	d.Folder = "Gone.S01E02"
	partial := filepath.Join(s.Downloads.Dir, d.Folder, "part.rar")
	touch(t, partial, 100)
	s.Store.SaveDownloadProgress(ctx, d)
	s.Store.SetDownloadStatus(ctx, d.ID, store.DownloadPaused, "")

	var plan struct {
		Downloads int `json:"downloads"`
	}
	call(t, hs, "GET", fmt.Sprintf("/api/v1/series/%d/removal", show.ID), "", &plan)
	if plan.Downloads != 1 {
		t.Fatalf("plan says %d downloads", plan.Downloads)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/series/%d", show.ID), "", nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := s.Store.Download(ctx, d.ID); err != store.ErrNotFound {
		t.Fatal("its download is still listed")
	}
	if there(partial) {
		t.Fatal("what it fetched is still there")
	}
	if !there(show.Path) {
		t.Fatal("library files deleted without being asked")
	}
}
