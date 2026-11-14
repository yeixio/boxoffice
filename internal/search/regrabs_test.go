package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestRegrabsPerDay(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	srv, _ := indexer(t,
		posted("The.Matrix.1999.REPACK.1080p.BluRay.x264-GRP", "fix", 9e9, soon),
		posted("Show.S01E01.1080p.WEB-DL-GRP", "e1", 2e9, soon),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.SetSetting(ctx, SettingMinAge, "0") // they're posted just now
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})

	if a, _ := LoadAutoSettings(ctx, st); a.RegrabsPerDay != DefaultRegrabsPerDay {
		t.Fatalf("default %d", a.RegrabsPerDay)
	}
	a, _ := LoadAutoSettings(ctx, st)
	a.RegrabsPerDay = 1
	if err := SaveAutoSettings(ctx, st, a); err != nil {
		t.Fatal(err)
	}
	st.CountRegrab(ctx) // today's one is used

	// A missing episode isn't a replacement: it's grabbed, and not counted.
	show, eps := showWithEpisodes(t, st, m.ProfileID, 1)
	res, err := s.Episode(automatic(ctx), eps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.pick(automatic(ctx), res, "series", show.ID, eps[0].ID); err != nil || !strings.HasPrefix(r.Message, "grabbed ") {
		t.Fatalf("missing episode %+v %v", r, err)
	}
	if used, left, _ := s.RegrabsToday(ctx); used != 1 || left != 0 {
		t.Fatalf("used %d, left %d", used, left)
	}

	// A fix of the movie's copy is: it waits.
	moviePath := m.Path + "/The Matrix (1999).mkv"
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: moviePath, Quality: 11})
	fromRelease(t, st, "movie", m.ID, nil, "The.Matrix.1999.1080p.BluRay.x264-GRP", moviePath)
	res, err = s.Movie(automatic(ctx), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.pick(automatic(ctx), res, "movie", m.ID, 0)
	if err != nil || !strings.Contains(r.Message, "today's 1 replacement is used up") {
		t.Fatalf("fix %+v %v", r, err)
	}
	waiting, _ := st.RegrabsWaiting(ctx)
	if len(waiting) != 1 || waiting[0].Subject != "The Matrix (1999)" || waiting[0].Why != RegrabFix || waiting[0].Release != "The.Matrix.1999.REPACK.1080p.BluRay.x264-GRP" {
		t.Fatalf("waiting %+v", waiting)
	}
	if q, _ := st.Pending(ctx, "movie", m.ID); len(q) != 0 {
		t.Fatalf("grabbed anyway: %+v", q)
	}

	// Nothing's looked for again while the day has no room.
	if got, _ := s.TakeWaitingRegrabs(ctx); len(got) != 0 {
		t.Fatalf("took %v", got)
	}

	// With the limit raised, the waiting one is searched for again and
	// grabbed, and counted.
	a.RegrabsPerDay = 2
	SaveAutoSettings(ctx, st, a)
	got, problems := s.TakeWaitingRegrabs(ctx)
	if len(got) != 1 || got[0] != "The Matrix (1999)" {
		t.Fatalf("took %v, problems %v", got, problems)
	}
	if waiting, _ = st.RegrabsWaiting(ctx); len(waiting) != 0 {
		t.Fatalf("still waiting %+v", waiting)
	}
	if used, left, _ := s.RegrabsToday(ctx); used != 2 || left != 0 {
		t.Fatalf("used %d, left %d", used, left)
	}

	// The day is full again; a grab by hand isn't held back, or counted.
	i := 0
	for ; i < len(res.Releases) && !res.Releases[i].Accepted; i++ {
	}
	if _, err := s.grab(byHand(ctx), res.Releases[i]); err != nil {
		t.Fatalf("by hand: %v", err)
	}
	if used, _, _ := s.RegrabsToday(ctx); used != 2 {
		t.Fatalf("counted a grab by hand: %d", used)
	}

	// No limit: 0.
	a.RegrabsPerDay = 0
	SaveAutoSettings(ctx, st, a)
	if _, left, _ := s.RegrabsToday(ctx); left != -1 {
		t.Fatalf("no limit left %d", left)
	}
}

func TestRegrabOf(t *testing.T) {
	srv, _ := indexer(t)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	c := Candidate{kind: "movie", titleID: m.ID}
	cases := []struct {
		ctx     context.Context
		fix     bool
		upgrade bool
		want    string
	}{
		{ctx, false, false, ""},                              // a missing title
		{automatic(ctx), false, true, RegrabUpgrade},         // an automatic upgrade
		{automatic(ctx), true, false, RegrabFix},             // a PROPER or REPACK
		{byHand(ctx), true, false, ""},                       // picked by hand
		{reported(byHand(ctx)), false, false, RegrabReplace}, // reported in Matinee
	}
	for i, tc := range cases {
		c.fix = tc.fix
		if got := s.regrabOf(tc.ctx, c, tc.upgrade); got != tc.want {
			t.Errorf("%d: %q, want %q", i, got, tc.want)
		}
	}
	// An automatic grab for a copy reported as bad is a replacement.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Want: store.WantReplace})
	if got := s.regrabOf(automatic(ctx), Candidate{kind: "movie", titleID: m.ID}, true); got != RegrabReplace {
		t.Errorf("reported before: %q", got)
	}
	if !errors.Is(RegrabsUsedUp{Limit: 3}, ErrRegrabsUsedUp) {
		t.Error("RegrabsUsedUp isn't ErrRegrabsUsedUp")
	}
}

// A copy reported in Matinee counts, and when it waits it's looked for
// again as it was asked for: automatic upgrades being off doesn't stop it.
func TestReportedRegrabWaits(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	srv, _ := indexer(t, posted("Show.S01E01.1080p.WEB-DL-OTHER", "e1", 2e9, soon))
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.SetSetting(ctx, SettingMinAge, "0")
	st.SetSetting(ctx, SettingRegrabsPerDay, "1")
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	show, eps := showWithEpisodes(t, st, m.ProfileID, 1)
	p := show.Path + "/Season 01/Show - S01E01.mkv"
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: p, Quality: 10})
	fromRelease(t, st, "series", show.ID, []int64{eps[0].ID}, "Show.S01E01.1080p.WEB-DL-GRP", p)
	st.SetTitleWant(ctx, store.TitleWant{Kind: "episode", OwnerID: eps[0].ID, Want: store.WantReplace, Problem: store.ProblemVideo})
	st.CountRegrab(ctx)

	job := AskedJob{Kind: "episode", ID: eps[0].ID, Ask: Ask{Want: store.WantReplace}}
	r, err := s.jobAsked(ctx, job.Payload())
	if err != nil || !strings.Contains(r.Message, "used up") {
		t.Fatalf("asked %+v %v", r, err)
	}
	waiting, _ := st.RegrabsWaiting(ctx)
	if len(waiting) != 1 || waiting[0].Why != RegrabReplace || waiting[0].Subject != "Show S01E01" {
		t.Fatalf("waiting %+v", waiting)
	}

	st.SetSetting(ctx, SettingRegrabsPerDay, "2")
	if got, problems := s.TakeWaitingRegrabs(ctx); len(got) != 1 {
		t.Fatalf("took %v, problems %v", got, problems)
	}
	if q, _ := st.Pending(ctx, "series", show.ID); len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
	if used, _, _ := s.RegrabsToday(ctx); used != 2 {
		t.Fatalf("used %d", used)
	}
}
