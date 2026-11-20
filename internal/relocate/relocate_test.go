package relocate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

type fixture struct {
	st       *store.Store
	s        *Service
	old, new string
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	f := fixture{st: st, s: &Service{Store: st}, old: t.TempDir(), new: t.TempDir()}
	for _, p := range []string{f.old, f.new} {
		if _, err := st.AddRootFolder(ctx, "movie", p); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// acrossDisks makes moving a folder act as if the new root folder were on
// another disk, so it's copied rather than renamed.
func acrossDisks(t *testing.T) {
	was := renameFolder
	renameFolder = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	t.Cleanup(func() { renameFolder = was })
}

func (f fixture) movie(t *testing.T, name string, tmdb int64, files map[string]string) store.Movie {
	t.Helper()
	ctx := context.Background()
	profiles, _ := f.st.Profiles(ctx)
	folder := filepath.Join(f.old, name)
	m, err := f.st.AddMovie(ctx, store.Movie{TMDBID: tmdb, Title: name, ProfileID: profiles[0].ID, RootFolder: f.old, Path: folder})
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		write(t, filepath.Join(folder, rel), body)
	}
	return m
}

func (f fixture) start(t *testing.T, p Pick) Plan {
	t.Helper()
	ctx := context.Background()
	plan, err := f.s.Plan(ctx, p)
	if err != nil || plan.Problem != "" {
		t.Fatal(plan.Problem, err)
	}
	if _, err := f.s.Start(ctx, p, plan.Count, plan.Size); err != nil {
		t.Fatal(err)
	}
	return plan
}

func (f fixture) moves(t *testing.T) map[string]store.TitleMove {
	t.Helper()
	list, err := f.st.TitleMoves(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.TitleMove{}
	for _, m := range list {
		out[m.Title] = m
	}
	return out
}

// Moving a movie to another disk copies its files, checks them, points
// the movie, its file's record and its subtitles at the copy, and only
// then removes the original. A name with an accent is handled too.
func TestMoveCopiesChecksSwitchesThenRemoves(t *testing.T) {
	acrossDisks(t)
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Amélie (2001)", 194, map[string]string{"Amélie (2001).mkv": "the film", "Amélie (2001).fr.srt": "sous-titres", "extras/trailer.mp4": "trailer"})
	video := filepath.Join(m.Path, "Amélie (2001).mkv")
	file, err := f.st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: video, Size: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveSubtitle(ctx, store.Subtitle{FileID: file.ID, Language: "fr", State: "fetched", Path: filepath.Join(m.Path, "Amélie (2001).fr.srt")}); err != nil {
		t.Fatal(err)
	}
	var after []int64
	f.s.AfterMove = func(_ context.Context, kind string, id int64) { after = append(after, id) }

	plan := f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	if plan.Count != 1 || plan.Files != 3 || plan.Size != int64(len("the film")+len("sous-titres")+len("trailer")) {
		t.Fatalf("plan %+v", plan)
	}
	f.s.RunAll(ctx)

	to := filepath.Join(f.new, "Amélie (2001)")
	if got := f.moves(t)["Amélie (2001)"]; got.State != store.MoveDone || got.Note != "" || got.ToPath != to {
		t.Fatalf("move %+v", got)
	}
	if exists(m.Path) {
		t.Fatal("the original folder is still there")
	}
	if read(t, filepath.Join(to, "Amélie (2001).mkv")) != "the film" || read(t, filepath.Join(to, "extras", "trailer.mp4")) != "trailer" {
		t.Fatal("the copy isn't right")
	}
	if exists(tempFolder(f.moves(t)["Amélie (2001)"])) {
		t.Fatal("the hidden copy folder was left")
	}
	got, _ := f.st.Movie(ctx, m.ID)
	if got.RootFolder != f.new || got.Path != to || got.File == nil || got.File.Path != filepath.Join(to, "Amélie (2001).mkv") {
		t.Fatalf("movie %+v file %+v", got, got.File)
	}
	subs, _ := f.st.SubtitlesOf(ctx, file.ID)
	if len(subs[file.ID]) != 1 || subs[file.ID][0].Path != filepath.Join(to, "Amélie (2001).fr.srt") {
		t.Fatalf("subtitles %+v", subs)
	}
	if len(after) != 1 || after[0] != m.ID {
		t.Fatalf("AfterMove %v", after)
	}
	if f.s.Moving("movie", m.ID) {
		t.Fatal("still held")
	}
}

// On the same disk the folder is just renamed.
func TestMoveRenamesOnTheSameDisk(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	plan := f.start(t, Pick{Kind: "movie", From: f.old, To: f.new})
	if plan.Need != 0 {
		t.Fatalf("same disk needs no space: %+v", plan)
	}
	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveDone {
		t.Fatalf("move %+v", got)
	}
	if exists(m.Path) || read(t, filepath.Join(f.new, "Heat (1995)", "Heat (1995).mkv")) != "heat" {
		t.Fatal("not renamed")
	}
}

// Titles that can't be moved are left out of the plan, each with why.
func TestPlanSkips(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.movie(t, "Ronin (1998)", 8195, map[string]string{"Ronin (1998).mkv": "ronin"})
	taken := f.movie(t, "Taken (2008)", 8681, map[string]string{"Taken (2008).mkv": "taken"})
	write(t, filepath.Join(f.new, "Taken (2008)", "other.mkv"), "x")
	shared := f.movie(t, "Shared (2000)", 1, map[string]string{"a.mkv": "a"})
	profiles, _ := f.st.Profiles(ctx)
	if _, err := f.st.AddMovie(ctx, store.Movie{TMDBID: 2, Title: "Inside", ProfileID: profiles[0].ID, RootFolder: f.old, Path: filepath.Join(shared.Path, "Inside")}); err != nil {
		t.Fatal(err)
	}
	f.movie(t, "Nothing Yet (2030)", 3, nil)
	link := f.movie(t, "Linked (1999)", 4, map[string]string{"a.mkv": "a"})
	os.Symlink("/etc/hostname", filepath.Join(link.Path, "link.mkv"))

	plan, err := f.s.Plan(ctx, Pick{Kind: "movie", From: f.old, To: f.new})
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, s := range plan.Skipped {
		why[s.Title] = s.Problem
	}
	if !strings.Contains(why["Taken (2008)"], "already") || !strings.Contains(why["Shared (2000)"], "same folder") || !strings.Contains(why["Linked (1999)"], "link") {
		t.Fatalf("skipped %v", why)
	}
	if plan.Count != 3 { // Ronin, and Inside and Nothing Yet, which have no files, so only their folder setting changes
		t.Fatalf("plan %+v", plan)
	}
	if p, _ := f.s.Plan(ctx, Pick{Kind: "movie", IDs: []int64{taken.ID}, To: f.old}); len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0].Problem, "already") {
		t.Fatalf("moving to where it is: %+v", p)
	}
	if _, err := f.s.Plan(ctx, Pick{Kind: "movie", IDs: []int64{taken.ID}, To: t.TempDir()}); !errors.As(err, new(ErrBadPick)) {
		t.Fatalf("a folder that isn't a root folder: %v", err)
	}
	// Starting checks the plan is what was shown.
	if _, err := f.s.Start(ctx, Pick{Kind: "movie", From: f.old, To: f.new}, plan.Count, plan.Size+1); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed plan: %v", err)
	}
}

// A file that changes while it's copied leaves the original as it was,
// clears the copy away, and the title is tried again.
func TestChangedWhileCopyingIsTriedAgain(t *testing.T) {
	acrossDisks(t)
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	afterCopy = func() {
		write(t, filepath.Join(m.Path, "Heat (1995).en.srt"), "new subtitle")
		afterCopy = nil
	}
	t.Cleanup(func() { afterCopy = nil })
	f.s.runOnce(ctx)
	got := f.moves(t)["Heat (1995)"]
	if got.State != store.MoveWaiting || got.Tries != 1 || !strings.Contains(got.Note, "changed") {
		t.Fatalf("move %+v", got)
	}
	if exists(tempFolder(got)) || exists(filepath.Join(f.new, "Heat (1995)")) || read(t, filepath.Join(m.Path, "Heat (1995).mkv")) != "heat" {
		t.Fatal("original or copy wrong after a change")
	}
	// The next go moves both files.
	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveDone {
		t.Fatalf("move %+v", got)
	}
	if read(t, filepath.Join(f.new, "Heat (1995)", "Heat (1995).en.srt")) != "new subtitle" || exists(m.Path) {
		t.Fatal("not moved the second time")
	}
}

// A title removed from the list while it's copied isn't switched; the
// original stays and the copy goes.
func TestRemovedWhileCopyingBacksOut(t *testing.T) {
	acrossDisks(t)
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	afterCopy = func() {
		f.st.DeleteMovie(ctx, m.ID)
		afterCopy = nil
	}
	t.Cleanup(func() { afterCopy = nil })
	f.s.RunAll(ctx)
	got := f.moves(t)["Heat (1995)"]
	if got.State != store.MoveFailed || !strings.Contains(got.Note, "nothing was moved") {
		t.Fatalf("move %+v", got)
	}
	if !exists(filepath.Join(m.Path, "Heat (1995).mkv")) || exists(filepath.Join(f.new, "Heat (1995)")) {
		t.Fatal("original should stay and the copy go")
	}
}

// A restart after the copy was checked but before the switch carries on
// from there.
func TestResumeAfterCopied(t *testing.T) {
	acrossDisks(t)
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	mv := f.moves(t)["Heat (1995)"]
	// As if the copy were made and the server stopped then.
	mv.ToPath, mv.State = filepath.Join(f.new, "Heat (1995)"), store.MoveCopying
	f.st.SaveTitleMove(ctx, mv)
	p := newLive(0)
	list, err := f.s.copyTree(ctx, m.Path, tempFolder(mv), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := jsonList(list)
	mv.Manifest, mv.State = b, store.MoveCopied
	f.st.SaveTitleMove(ctx, mv)

	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveDone {
		t.Fatalf("move %+v", got)
	}
	if exists(m.Path) || read(t, filepath.Join(f.new, "Heat (1995)", "Heat (1995).mkv")) != "heat" {
		t.Fatal("not finished")
	}
	got, _ := f.st.Movie(ctx, m.ID)
	if got.Path != filepath.Join(f.new, "Heat (1995)") {
		t.Fatalf("movie %+v", got)
	}
}

// A restart part-way through copying starts that title over, leaving the
// original alone.
func TestResumeWhileCopyingStartsOver(t *testing.T) {
	acrossDisks(t)
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	mv := f.moves(t)["Heat (1995)"]
	mv.ToPath, mv.State = filepath.Join(f.new, "Heat (1995)"), store.MoveCopying
	f.st.SaveTitleMove(ctx, mv)
	write(t, filepath.Join(tempFolder(mv), "Heat (1995).mkv"), "he") // cut off

	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveDone {
		t.Fatalf("move %+v", got)
	}
	if read(t, filepath.Join(f.new, "Heat (1995)", "Heat (1995).mkv")) != "heat" || exists(tempFolder(mv)) {
		t.Fatal("not started over")
	}
}

// Paused, nothing moves; cancelled, the waiting ones never do.
func TestPauseAndCancel(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	f.s.Pause(ctx)
	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveWaiting || !exists(m.Path) {
		t.Fatalf("moved while paused: %+v", got)
	}
	f.s.Cancel(ctx)
	f.s.Resume(ctx)
	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveCancelled || !exists(m.Path) {
		t.Fatalf("moved after cancelling: %+v", got)
	}
	st, err := f.s.Status(ctx)
	if err != nil || st.Paused || len(st.Moves) != 1 {
		t.Fatalf("status %+v %v", st, err)
	}
	f.s.Clear(ctx)
	if st, _ := f.s.Status(ctx); len(st.Moves) != 0 {
		t.Fatal("not cleared")
	}
}

// A download going into the title's folder holds the move back.
func TestWaitsForADownloadGoingIn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	m := f.movie(t, "Heat (1995)", 949, map[string]string{"Heat (1995).mkv": "heat"})
	f.start(t, Pick{Kind: "movie", IDs: []int64{m.ID}, To: f.new})
	d, err := f.st.AddDownload(ctx, store.Download{Name: "Heat.1995.1080p", Kind: "movie", TitleID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.st.SetDownloadStatus(ctx, d.ID, store.DownloadImporting, "")
	if f.s.runOnce(ctx) {
		t.Fatal("moved while a download was going in")
	}
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveWaiting || !strings.Contains(got.Note, "download") {
		t.Fatalf("move %+v", got)
	}
	f.st.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, "")
	f.s.RunAll(ctx)
	if got := f.moves(t)["Heat (1995)"]; got.State != store.MoveDone {
		t.Fatalf("move %+v", got)
	}
}

func jsonList(list []entry) (string, error) {
	b, err := json.Marshal(list)
	return string(b), err
}
