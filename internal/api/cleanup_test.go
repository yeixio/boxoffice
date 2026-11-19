package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

type cleanupEnv struct {
	s    *Server
	ctx  context.Context
	root string
	t    *testing.T
}

// movie adds a movie with a 500-byte file in its own folder.
func (e cleanupEnv) movie(tmdb int64, name string) store.Movie {
	e.t.Helper()
	profiles, _ := e.s.Store.Profiles(e.ctx)
	m, err := e.s.Store.AddMovie(e.ctx, store.Movie{TMDBID: tmdb, Title: name, ProfileID: profiles[0].ID, RootFolder: e.root, Path: filepath.Join(e.root, name)})
	if err != nil {
		e.t.Fatal(err)
	}
	path := filepath.Join(m.Path, name+".mkv")
	touch(e.t, path, 500)
	if _, err := e.s.Store.AddFile(e.ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: 500}); err != nil {
		e.t.Fatal(err)
	}
	return m
}

// report is Matinee saying what was watched: two people, and these plays.
func (e cleanupEnv) report(rows ...store.Watched) {
	e.t.Helper()
	if err := e.s.Store.ReplaceWatched(e.ctx, "movie", 2, rows); err != nil {
		e.t.Fatal(err)
	}
}

func (e cleanupEnv) items() []store.CleanupItem {
	e.t.Helper()
	if err := e.s.Cleanup(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	items, err := e.s.Store.CleanupItems(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func ago(days int) *time.Time {
	t := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	return &t
}

func finished(m store.Movie, days int) store.Watched {
	return store.Watched{TitleID: m.ID, LastPlayed: ago(days), Viewers: 2, FinishedBy: 2, FinishedAt: ago(days)}
}

// A rule lists what everyone has finished as leaving soon and deletes
// nothing itself; an admin deletes a listed title, only as shown, or keeps
// it.
func TestCleanupListsAndDeletesByHand(t *testing.T) {
	s, hs, _ := monitorServer(t)
	e := cleanupEnv{s, context.Background(), t.TempDir(), t}
	done, half, fresh, never, stay := e.movie(1, "Done"), e.movie(2, "Half"), e.movie(3, "Fresh"), e.movie(4, "Never"), e.movie(5, "Stay")
	rule := `{"kind":"movie","watched":"everyone","days":30,"graceDays":14}`

	// Without Matinee's report, a rule waits.
	var pre cleanupPreview
	if code := call(t, hs, "POST", "/api/v1/cleanup/preview", rule, &pre); code != 200 || pre.Waiting != store.CleanupNoReport || pre.Count != 0 {
		t.Fatalf("preview without a report: %d %+v", code, pre)
	}
	e.report(finished(done, 60), finished(stay, 60),
		store.Watched{TitleID: half.ID, LastPlayed: ago(60), Viewers: 2, FinishedBy: 1, FinishedAt: ago(60)},
		finished(fresh, 3))
	if code := call(t, hs, "POST", fmt.Sprintf("/api/v1/cleanup/items/%d/keep", 0), "", nil); code != 404 {
		t.Fatalf("keeping nothing: %d", code)
	}
	if err := s.Store.KeepFromCleanup(e.ctx, "movie", stay.ID); err != nil {
		t.Fatal(err)
	}
	pre = cleanupPreview{}
	if code := call(t, hs, "POST", "/api/v1/cleanup/preview", rule, &pre); code != 200 || pre.Waiting != "" || pre.Count != 1 || pre.Size != 500 || pre.Titles[0].Title != "Done" {
		t.Fatalf("preview: %d %+v", code, pre)
	}
	if items, _ := s.Store.CleanupItems(e.ctx); len(items) != 0 {
		t.Fatal("a preview listed something")
	}
	for _, bad := range []string{`{"kind":"movie","watched":"nobody","days":7}`, `{"kind":"movie","watched":"everyone","days":30,"graceDays":2,"auto":true}`, `{"kind":"album","watched":"everyone","days":30}`} {
		if code := call(t, hs, "POST", "/api/v1/cleanup/rules", bad, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if code := call(t, hs, "POST", "/api/v1/cleanup/rules", rule, nil); code != 200 {
		t.Fatalf("add: %d", code)
	}
	items := e.items()
	if len(items) != 1 || items[0].TitleID != done.ID || items[0].Files != 1 || items[0].Size != 500 {
		t.Fatalf("listed: %+v", items)
	}
	// Its days pass: the rule doesn't delete by itself.
	s.Store.RelistCleanupItem(e.ctx, items[0].ID, 1, 500, time.Now().Add(-time.Hour), "")
	if items = e.items(); len(items) != 1 || !there(done.Path) {
		t.Fatalf("a rule that only lists deleted: %+v", items)
	}
	var view cleanupView
	if code := call(t, hs, "GET", "/api/v1/cleanup", "", &view); code != 200 || len(view.Rules) != 1 || view.Rules[0].Leaving != 1 || len(view.Items) != 1 ||
		view.Items[0].About.Title != "Done" || view.Items[0].About.FinishedBy != 2 || len(view.Kept) != 1 || view.Kept[0].Title != "Stay" || !view.Reports["movie"].Fresh || view.DailyCap != DefaultCleanupCap {
		t.Fatalf("view: %d %+v", code, view)
	}

	// Deleting by hand takes what was shown, and nothing else.
	del := fmt.Sprintf("/api/v1/cleanup/items/%d", items[0].ID)
	if code := call(t, hs, "DELETE", del, "", nil); code != 400 {
		t.Fatalf("no expected size: %d", code)
	}
	touch(t, filepath.Join(done.Path, "Done.en.srt"), 10)
	if code := call(t, hs, "DELETE", del+"?expectFiles=1&expectSize=500", "", nil); code != 409 || !there(done.Path) {
		t.Fatalf("a changed folder was deleted: %d", code)
	}
	if got, _ := s.Store.CleanupItem(e.ctx, items[0].ID); got.Files != 2 || got.Size != 510 || got.LeavesAt.Before(time.Now().Add(13*24*time.Hour)) {
		t.Fatalf("not brought up to date: %+v", got)
	}
	if code := call(t, hs, "DELETE", del+"?expectFiles=2&expectSize=510", "", &view); code != 200 || len(view.Items) != 0 {
		t.Fatalf("delete: %d %+v", code, view)
	}
	if there(done.Path) {
		t.Fatal("the folder is still there")
	}
	if _, err := s.Store.Movie(e.ctx, done.ID); err != store.ErrNotFound {
		t.Fatalf("the movie is still on the list: %v", err)
	}
	for _, m := range []store.Movie{half, fresh, never, stay} {
		if !there(m.Path) {
			t.Fatalf("%s was deleted", m.Title)
		}
	}
	// A kept title can be let go again.
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/cleanup/kept/movie/%d", stay.ID), "", &view); code != 200 || len(view.Kept) != 0 {
		t.Fatalf("unkeep: %d %+v", code, view)
	}
	if items = e.items(); len(items) != 1 || items[0].TitleID != stay.ID {
		t.Fatalf("after unkeep: %+v", items)
	}
	// Someone plays it again: it comes off the list.
	e.report(finished(stay, 0))
	if items = e.items(); len(items) != 0 {
		t.Fatalf("played again but still leaving: %+v", items)
	}
}

// A rule set to delete by itself does so once a title's days are up: at
// most so many a day, only what was listed, never a folder that's gone or
// a title on Do not touch, and not while Matinee's report is old.
func TestCleanupDeletesByItself(t *testing.T) {
	s, hs, _ := monitorServer(t)
	e := cleanupEnv{s, context.Background(), t.TempDir(), t}
	a, b, c, gone, locked := e.movie(1, "A"), e.movie(2, "B"), e.movie(3, "C"), e.movie(4, "Gone"), e.movie(5, "Locked")
	profiles, _ := s.Store.Profiles(e.ctx)
	for _, p := range profiles {
		if p.Locked {
			locked.ProfileID = p.ID
		}
	}
	if locked.ProfileID == profiles[0].ID && !profiles[0].Locked {
		t.Fatal("no Do not touch profile to test with")
	}
	if err := s.Store.UpdateMovie(e.ctx, locked); err != nil {
		t.Fatal(err)
	}
	e.report(finished(a, 60), finished(b, 60), finished(c, 60), finished(gone, 60), finished(locked, 60))
	if code := call(t, hs, "PUT", "/api/v1/cleanup/settings", `{"dailyCap":1}`, nil); code != 200 {
		t.Fatalf("cap: %d", code)
	}
	rule, err := s.Store.AddCleanupRule(e.ctx, store.CleanupRule{Kind: "movie", Watched: store.CleanupEveryone, Days: 30, GraceDays: 7, Auto: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	items := e.items()
	if len(items) != 4 {
		t.Fatalf("listed: %+v", items)
	}
	for _, m := range []store.Movie{a, b, c, gone} {
		if !there(m.Path) {
			t.Fatalf("%s was deleted before its days were up", m.Title)
		}
	}
	due := func() {
		t.Helper()
		items, _ := s.Store.CleanupItems(e.ctx)
		for _, i := range items {
			s.Store.RelistCleanupItem(e.ctx, i.ID, i.Files, i.Size, time.Now().Add(-time.Hour), "")
		}
	}
	// Matinee has gone quiet: nothing is deleted.
	old, _ := json.Marshal(store.WatchReport{At: time.Now().Add(-48 * time.Hour), People: 2, Titles: 5})
	s.Store.SetSetting(e.ctx, "watch_report_movie", string(old))
	due()
	if items = e.items(); len(items) != 4 || !there(a.Path) {
		t.Fatalf("deleted on an old report: %+v", items)
	}
	if r, _ := s.Store.CleanupRule(e.ctx, rule.ID); r.Waiting != store.CleanupNoReport {
		t.Fatalf("rule: %+v", r)
	}
	e.report(finished(a, 60), finished(b, 60), finished(c, 60), finished(gone, 60), finished(locked, 60))

	// The share with Gone on it dropped out, and C's folder changed.
	os.Rename(gone.Path, gone.Path+".away")
	touch(t, filepath.Join(c.Path, "C.nfo"), 5)
	due()
	items = e.items()
	left := 0
	for _, m := range []store.Movie{a, b} {
		if there(m.Path) {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("with one a day allowed, %d of A and B are left", left)
	}
	if !there(c.Path) || !there(gone.Path+".away") || !there(locked.Path) {
		t.Fatal("something that shouldn't have been was deleted")
	}
	if _, err := s.Store.Movie(e.ctx, gone.ID); err != nil {
		t.Fatalf("a movie whose folder is away was removed: %v", err)
	}
	// Tomorrow the other goes.
	s.Store.SetSetting(e.ctx, settingCleanupToday, "")
	due()
	items = e.items()
	if there(a.Path) || there(b.Path) {
		t.Fatal("A and B should both be gone")
	}
	// The day after, C's turn comes: its folder isn't what was listed.
	s.Store.SetSetting(e.ctx, settingCleanupToday, "")
	items = e.items()
	var sawC bool
	for _, i := range items {
		if i.TitleID == c.ID {
			sawC = true
			if i.Files != 2 || i.Size != 505 || i.LeavesAt.Before(time.Now().Add(6*24*time.Hour)) {
				t.Fatalf("C: %+v", i)
			}
		}
	}
	if !sawC || !there(c.Path) {
		t.Fatalf("C: listed %v, there %v", sawC, there(c.Path))
	}
	// Turning the rule off clears its list and deletes nothing.
	rule.Enabled = false
	if err := s.Store.UpdateCleanupRule(e.ctx, rule); err != nil {
		t.Fatal(err)
	}
	if items = e.items(); len(items) != 0 || !there(c.Path) {
		t.Fatalf("off: %+v", items)
	}
}

// A "nobody" rule takes titles nobody has played since they came, and
// doesn't trust a report with no plays in it at all.
func TestCleanupNobody(t *testing.T) {
	s, _, _ := monitorServer(t)
	e := cleanupEnv{s, context.Background(), t.TempDir(), t}
	seen, unseen := e.movie(1, "Seen"), e.movie(2, "Unseen")
	rule := store.CleanupRule{Kind: "movie", Watched: store.CleanupNobody, Days: 30}
	later := time.Now().Add(40 * 24 * time.Hour)
	stamp := func(titles int) {
		b, _ := json.Marshal(store.WatchReport{At: later.Add(-time.Hour), People: 2, Titles: titles})
		s.Store.SetSetting(e.ctx, "watch_report_movie", string(b))
	}
	e.report()
	stamp(0)
	if got, waiting, err := s.cleanupMatches(e.ctx, rule, later); err != nil || waiting != store.CleanupNoPlays || len(got) != 0 {
		t.Fatalf("no plays at all: %+v %q %v", got, waiting, err)
	}
	e.report(store.Watched{TitleID: seen.ID, LastPlayed: ago(1), Viewers: 1})
	stamp(1)
	// Too new to say nobody wants it.
	if got, _, _ := s.cleanupMatches(e.ctx, rule, time.Now().Add(20*24*time.Hour)); len(got) != 0 {
		t.Fatalf("matched early: %+v", got)
	}
	got, waiting, err := s.cleanupMatches(e.ctx, rule, later)
	if err != nil || waiting != "" || len(got) != 1 || got[0].TitleID != unseen.ID {
		t.Fatalf("nobody: %+v %q %v", got, waiting, err)
	}
}
