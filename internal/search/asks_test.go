package search

import (
	"context"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestEditionsAndAsks(t *testing.T) {
	srv, _ := indexer(t,
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "a", 9e9),
		item("The.Matrix.1999.Directors.Cut.1080p.WEB-DL-GRP", "b", 7e9),
		item("The.Matrix.1999.Directors.Cut.1080p.BluRay-GRP", "c", 9e9),
		item("The.Matrix.1999.EXTENDED.720p.BluRay-GRP", "d", 5e9),
		item("The.Matrix.1999.UNRATED.1080p.WEB-DL-GRP", "e", 7e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/The Matrix (1999).mkv", Quality: 11})

	// The copy there is the profile's best: a plain search takes nothing.
	res, err := s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Releases {
		if c.Accepted {
			t.Fatalf("took %s with a Blu-ray 1080p copy there", c.Title)
		}
	}

	// Editions are listed apart from the copy there; 720p isn't in the
	// profile, so Extended isn't on offer.
	eds, _, err := s.MovieEditions(ctx, m.ID, []string{"Unrated"})
	if err != nil {
		t.Fatal(err)
	}
	if len(eds) != 2 || eds[0].Edition != "Director's Cut" || eds[0].Releases != 2 || eds[0].Quality != "Blu-ray 1080p" || !eds[1].Owned || eds[1].Edition != "Unrated" {
		t.Fatalf("editions %+v", eds)
	}

	// Asked for alongside, only the Director's Cut is taken, best first.
	res, _ = s.MovieFor(ctx, m.ID, Ask{Edition: "Director's Cut", Mode: ModeAlongside})
	if !res.Releases[0].Accepted || res.Releases[0].GUID != "c" || res.Releases[0].mode != ModeAlongside {
		t.Fatalf("best %+v", res.Releases[0])
	}
	for _, c := range res.Releases[1:] {
		if c.Accepted && c.GUID != "b" {
			t.Errorf("took %s for the Director's Cut", c.Title)
		}
	}

	// The job grabs it with its edition and mode.
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	r, err := s.jobAsked(ctx, AskedJob{Kind: "movie", ID: m.ID, Ask: Ask{Edition: "Director's Cut", Mode: ModeAlongside}}.Payload())
	if err != nil || r.Status != "done" || !strings.Contains(r.Message, "Directors.Cut.1080p.BluRay") {
		t.Fatalf("job %+v %v", r, err)
	}
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 1 || q[0].Edition != "Director's Cut" || q[0].Mode != ModeAlongside {
		t.Fatalf("queue %+v", q)
	}
	st.DeleteDownload(ctx, q[0].ID)

	// A bad copy: anything the profile takes will do, except what's
	// blocked or was grabbed before.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Want: store.WantReplace})
	res, _ = s.Movie(ctx, m.ID)
	if !res.Releases[0].Accepted || res.Releases[0].GUID != "a" {
		t.Fatalf("replacement %+v", res.Releases[0])
	}

	// A movie wanted as the Director's Cut stays it.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Edition: "Director's Cut", Want: store.WantReplace})
	res, _ = s.Movie(ctx, m.ID)
	for _, c := range res.Releases {
		if c.Accepted && c.Edition != "Director's Cut" {
			t.Errorf("took %s for a movie wanted as the Director's Cut", c.Title)
		}
	}
}

func TestAskBetterPastCutoff(t *testing.T) {
	srv, _ := indexer(t, item("The.Matrix.1999.1080p.Remux-GRP", "r", 30e9), item("The.Matrix.1999.1080p.BluRay-GRP", "b", 9e9))
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	// WEB 1080p is the HD profile's cutoff: no upgrades past it.
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/The Matrix (1999).mkv", Quality: 10})
	res, _ := s.Movie(ctx, m.ID)
	if res.Releases[0].Accepted {
		t.Fatalf("took %s past the cutoff", res.Releases[0].Title)
	}
	res, _ = s.MovieFor(ctx, m.ID, Ask{Want: store.WantBetter})
	if !res.Releases[0].Accepted || res.Releases[0].GUID != "r" {
		t.Fatalf("better %+v", res.Releases[0])
	}
}

func TestAskReplaceInLanguage(t *testing.T) {
	srv, _ := indexer(t,
		item("The.Matrix.1999.German.DL.1080p.BluRay.x264-GRP", "de", 9e9),
		item("The.Matrix.1999.GERMAN.1080p.BluRay.x264-GRP", "g", 9e9),
		item("The.Matrix.1999.1080p.WEB-DL.x264-GRP", "w", 7e9),
		item("The.Matrix.1999.FRENCH.1080p.BluRay.x264-GRP", "f", 9e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/The Matrix (1999).mkv", Quality: 11})

	// Reported as French, wanted in German: German first, then
	// dual-language and unnamed ones; French-only is turned down.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Want: store.WantReplace, Problem: store.ProblemLanguage, Language: "de"})
	res, _ := s.Movie(ctx, m.ID)
	got := map[string]Candidate{}
	for _, c := range res.Releases {
		got[c.GUID] = c
	}
	if res.Releases[0].GUID != "de" && res.Releases[0].GUID != "g" {
		t.Fatalf("first %+v", res.Releases[0])
	}
	if !got["w"].Accepted || got["f"].Accepted || !strings.Contains(strings.Join(got["f"].Rejections, ";"), "it's in French, and German was asked for") {
		t.Fatalf("judged %+v", got)
	}

	// Wanted in English: the German-only one is turned down, the
	// dual-language one isn't.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: m.ID, Want: store.WantReplace, Problem: store.ProblemLanguage, Language: "en"})
	res, _ = s.Movie(ctx, m.ID)
	for _, c := range res.Releases {
		if c.Accepted != (c.GUID == "de" || c.GUID == "w") {
			t.Errorf("%s accepted %v %v", c.Title, c.Accepted, c.Rejections)
		}
	}
}
