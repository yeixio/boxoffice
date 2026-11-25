package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/zone"
)

type watchedEnv struct {
	s    *Server
	ctx  context.Context
	root string
	t    *testing.T
}

// show adds a show with a special and n episodes of season 1, each with a
// 1000-byte file, on the profile given.
func (e watchedEnv) show(tmdb int64, name string, n, profile int) store.Series {
	e.t.Helper()
	show, err := e.s.Store.AddSeries(e.ctx, store.Series{TMDBID: tmdb, Title: name, ProfileID: profile, RootFolder: e.root, Path: filepath.Join(e.root, name),
		Monitored: true, Seasons: []store.Season{{Number: 0, Monitored: true}, {Number: 1, Monitored: true}}})
	if err != nil {
		e.t.Fatal(err)
	}
	e.s.Store.UpsertEpisode(e.ctx, store.Episode{SeriesID: show.ID, Season: 0, Number: 1, Monitored: true})
	for i := 1; i <= n; i++ {
		e.s.Store.UpsertEpisode(e.ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: i, Monitored: true})
	}
	eps, _ := e.s.Store.Episodes(e.ctx, show.ID)
	for _, ep := range eps {
		touch(e.t, e.path(show, ep.Season, ep.Number), 1000)
		if _, err := e.s.Store.AddFile(e.ctx, store.File{Kind: "episode", OwnerID: ep.ID, Path: e.path(show, ep.Season, ep.Number), Size: 1000}); err != nil {
			e.t.Fatal(err)
		}
	}
	return show
}

func (e watchedEnv) path(show store.Series, season, number int) string {
	return filepath.Join(show.Path, fmt.Sprintf("Season %02d", season), fmt.Sprintf("%s - S%02dE%02d.mkv", show.Title, season, number))
}

// finished is Matinee saying everyone watching each show has finished up
// to that episode of season 1.
func (e watchedEnv) finished(upTo map[int64]int) {
	e.t.Helper()
	var rows []store.Watched
	for id, n := range upTo {
		rows = append(rows, store.Watched{TitleID: id, Viewers: 2, FurthestSeason: 1, FurthestEpisode: n + 1, AllFinishedSeason: 1, AllFinishedEpisode: n})
	}
	if err := e.s.Store.ReplaceWatched(e.ctx, "series", 2, rows); err != nil {
		e.t.Fatal(err)
	}
}

func (e watchedEnv) run() {
	e.t.Helper()
	if err := e.s.RemoveWatched(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

// has says which of a show's season 1 episodes still have a file on disk
// and on record, as "1100" for the first two of four.
func (e watchedEnv) has(show store.Series) string {
	e.t.Helper()
	eps, _ := e.s.Store.Episodes(e.ctx, show.ID)
	out := ""
	for _, ep := range eps {
		if ep.Season == 0 {
			continue
		}
		onDisk := there(e.path(show, ep.Season, ep.Number))
		if onDisk != (ep.File != nil) {
			e.t.Fatalf("%s S%02dE%02d: on disk %v, on record %v", show.Title, ep.Season, ep.Number, onDisk, ep.File != nil)
		}
		if onDisk {
			out += "1"
		} else {
			out += "0"
		}
	}
	return out
}

func recycled(root string) string {
	return filepath.Join(root, library.RecycleDir, time.Now().UTC().Format(time.DateOnly))
}

// A show's watched episodes are only removed once someone turns that on,
// having seen what goes; then they move to the recycle folder as everyone
// finishes them, and are no longer watched for.
func TestRemoveWatchedForAShow(t *testing.T) {
	s, hs, _ := monitorServer(t)
	e := watchedEnv{s, context.Background(), t.TempDir(), t}
	profiles, _ := s.Store.Profiles(e.ctx)
	show := e.show(1, "Show", 4, profiles[0].ID)
	touch(t, filepath.Join(show.Path, "Season 01", "Show - S01E01.en.srt"), 10)
	e.finished(map[int64]int{show.ID: 2})

	// Off: nothing goes, whatever was watched.
	e.run()
	if got := e.has(show); got != "1111" {
		t.Fatalf("removed while off: %s", got)
	}

	base := fmt.Sprintf("/api/v1/series/%d", show.ID)
	var plan watchedRemoval
	if code := call(t, hs, "GET", base+"/watched/removal", "", &plan); code != 200 {
		t.Fatal(code)
	}
	if plan.Episodes != 2 || plan.Files != 3 || plan.Size != 2010 || plan.Season != 1 || plan.Episode != 2 || !plan.Reported || plan.Stale || plan.Problem != "" || plan.KeepDays != DefaultRecycleDays {
		t.Fatalf("plan %+v", plan)
	}
	// Turning it on takes what was shown, and nothing else.
	if code := call(t, hs, "PATCH", base, `{"removeWatched":"on"}`, nil); code != 400 {
		t.Fatalf("turned on without having looked: %d", code)
	}
	if code := call(t, hs, "PATCH", base, `{"removeWatched":"on","expectFiles":2,"expectSize":2000}`, nil); code != 409 {
		t.Fatalf("turned on for something else than was shown: %d", code)
	}
	if code := call(t, hs, "PATCH", base, `{"removeWatched":"maybe"}`, nil); code != 400 {
		t.Fatalf("a choice there isn't: %d", code)
	}
	if got, _ := s.Store.Series(e.ctx, show.ID); got.RemoveWatched != "" || e.has(show) != "1111" {
		t.Fatalf("on, or removed, without agreement: %q %s", got.RemoveWatched, e.has(show))
	}
	var detail struct {
		Series  store.Series
		Watched struct{ AllSeason, AllEpisode int }
	}
	if code := call(t, hs, "PATCH", base, `{"removeWatched":"on","expectFiles":3,"expectSize":2010}`, &detail); code != 200 {
		t.Fatalf("turn on: %d", code)
	}
	if detail.Series.RemoveWatched != "on" || detail.Watched.AllSeason != 1 || detail.Watched.AllEpisode != 2 {
		t.Fatalf("after turning on: %+v", detail)
	}
	if got := e.has(show); got != "0011" {
		t.Fatalf("after turning on: %s", got)
	}
	// They went to the recycle folder, subtitles and all, and the special
	// stayed.
	for _, name := range []string{"Show - S01E01.mkv", "Show - S01E01.en.srt", "Show - S01E02.mkv"} {
		if !there(filepath.Join(recycled(e.root), name)) {
			t.Fatalf("%s isn't in the recycle folder", name)
		}
	}
	eps, _ := s.Store.Episodes(e.ctx, show.ID)
	if eps[0].Season != 0 || eps[0].File == nil || !eps[0].Monitored {
		t.Fatalf("the special: %+v", eps[0])
	}
	if eps[1].Monitored || eps[2].Monitored || !eps[3].Monitored {
		t.Fatalf("watched for: %v %v %v", eps[1].Monitored, eps[2].Monitored, eps[3].Monitored)
	}

	// Everyone gets further: the next one goes by itself.
	e.finished(map[int64]int{show.ID: 3})
	e.run()
	if got := e.has(show); got != "0001" {
		t.Fatalf("after episode 3: %s", got)
	}

	// The share drops out: nothing goes, and the episode keeps its file.
	e.finished(map[int64]int{show.ID: 4})
	away := show.Path + ".away"
	os.Rename(show.Path, away)
	e.run()
	os.Rename(away, show.Path)
	if eps, _ = s.Store.Episodes(e.ctx, show.ID); eps[4].File == nil || !eps[4].Monitored {
		t.Fatalf("forgotten while its folder was away: %+v", eps[4])
	}

	// A report from more than a day ago isn't gone by.
	old, _ := json.Marshal(store.WatchReport{At: time.Now().Add(-25 * time.Hour), People: 2, Titles: 1})
	s.Store.SetSetting(e.ctx, "watch_report_series", string(old))
	e.run()
	if got := e.has(show); got != "0001" {
		t.Fatalf("went by an old report: %s", got)
	}
	if code := call(t, hs, "GET", base+"/watched/removal", "", &plan); code != 200 || !plan.Stale || plan.Files != 0 {
		t.Fatalf("plan from an old report: %d %+v", code, plan)
	}

	// Turned off for the show: it stays, with a fresh report too.
	if code := call(t, hs, "PATCH", base, `{"removeWatched":"off"}`, nil); code != 200 {
		t.Fatal(code)
	}
	e.finished(map[int64]int{show.ID: 4})
	e.run()
	if got := e.has(show); got != "0001" {
		t.Fatalf("removed after being turned off: %s", got)
	}
}

// Settings turns it on for every show that doesn't say otherwise, after
// showing what goes; Do not touch shows are left alone, and Box Office
// only removes so many a day by itself.
func TestRemoveWatchedForEveryShow(t *testing.T) {
	s, hs, _ := monitorServer(t)
	e := watchedEnv{s, context.Background(), t.TempDir(), t}
	profiles, _ := s.Store.Profiles(e.ctx)
	plain, locked := 0, 0
	for _, p := range profiles {
		if p.Locked {
			locked = p.ID
		} else if plain == 0 {
			plain = p.ID
		}
	}
	if locked == 0 {
		t.Fatal("no Do not touch profile")
	}
	follows := e.show(1, "Follows", 3, plain)
	off := e.show(2, "Kept", 3, plain)
	off.RemoveWatched = "off"
	s.Store.UpdateSeries(e.ctx, off)
	untouched := e.show(3, "Untouched", 3, locked)
	e.finished(map[int64]int{follows.ID: 1, off.ID: 3, untouched.ID: 3})

	var view spaceSaverView
	if code := call(t, hs, "GET", "/api/v1/space-saver", "", &view); code != 200 || view.RemoveWatched || view.ShowsOff != 1 || view.ShowsOn != 0 || !view.Reported || view.Stale {
		t.Fatalf("off to begin with: %d %+v", code, view)
	}
	var plan watchedRemoval
	if code := call(t, hs, "GET", "/api/v1/space-saver/removal", "", &plan); code != 200 || plan.Shows != 1 || plan.Episodes != 1 || plan.Files != 1 || plan.Size != 1000 {
		t.Fatalf("plan: %d %+v", code, plan)
	}
	if code := call(t, hs, "PUT", "/api/v1/space-saver", `{"removeWatched":true}`, nil); code != 400 {
		t.Fatalf("turned on without having looked: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/space-saver", `{"removeWatched":true,"expectFiles":9,"expectSize":9000}`, nil); code != 409 {
		t.Fatalf("turned on for something else than was shown: %d", code)
	}
	if e.has(follows) != "111" {
		t.Fatal("removed without agreement")
	}
	if code := call(t, hs, "PUT", "/api/v1/space-saver", `{"removeWatched":true,"expectFiles":1,"expectSize":1000}`, &view); code != 200 || !view.RemoveWatched {
		t.Fatalf("turn on: %d %+v", code, view)
	}
	if e.has(follows) != "011" || e.has(off) != "111" || e.has(untouched) != "111" {
		t.Fatalf("after turning on: %s %s %s", e.has(follows), e.has(off), e.has(untouched))
	}
	var detail struct {
		RemoveWatched bool
	}
	if code := call(t, hs, "GET", fmt.Sprintf("/api/v1/series/%d", follows.ID), "", &detail); code != 200 || !detail.RemoveWatched {
		t.Fatalf("a show's page doesn't say what Settings says: %d %+v", code, detail)
	}

	// One short of the day's cap: one more goes today, the rest waits.
	s.Store.SetSetting(e.ctx, settingRemoveWatchedToday, zone.In(time.Now()).Format(time.DateOnly)+":"+strconv.Itoa(RemoveWatchedDailyCap-1))
	e.finished(map[int64]int{follows.ID: 3, untouched.ID: 3})
	e.run()
	if got := e.has(follows); got != "001" {
		t.Fatalf("at the day's cap: %s", got)
	}
	e.run()
	if got := e.has(follows); got != "001" {
		t.Fatalf("past the day's cap: %s", got)
	}
	// A new day.
	s.Store.SetSetting(e.ctx, settingRemoveWatchedToday, "2020-01-01:30")
	e.run()
	if e.has(follows) != "000" || e.has(untouched) != "111" {
		t.Fatalf("the next day: %s %s", e.has(follows), e.has(untouched))
	}

	// Off again: nothing more goes.
	if code := call(t, hs, "PUT", "/api/v1/space-saver", `{"removeWatched":false}`, &view); code != 200 || view.RemoveWatched {
		t.Fatalf("turn off: %d %+v", code, view)
	}
}
