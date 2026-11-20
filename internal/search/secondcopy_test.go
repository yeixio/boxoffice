package search

import (
	"context"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// A movie that keeps a second copy is searched for once and gets a
// release for each: the first by its profile, the second by the second
// copy's. Each has its own download, and neither keeps the other waiting.
func TestSecondCopySearch(t *testing.T) {
	srv, asked := indexer(t,
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "hd", 9e9),
		item("The.Matrix.1999.2160p.UHD.BluRay.x265-GRP", "uhd", 40e9),
		item("The.Matrix.1999.2160p.WEB-DL.x265-GRP", "uhdweb", 20e9),
		item("The.Matrix.1999.720p.BluRay.x264-GRP", "small", 4e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	st.SetSetting(ctx, SettingMinAge, "0")
	ps, _ := st.Profiles(ctx)
	uhd := ps[2]
	if uhd.Name != "Ultra HD 4K" {
		t.Fatalf("profile %s", uhd.Name)
	}

	wanted := func() []Wanted {
		t.Helper()
		list, err := s.WantedList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	// With its first copy in and no second copy asked for, nothing's wanted.
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/a.mkv", Quality: 11}); err != nil {
		t.Fatal(err)
	}
	if w := wanted(); len(w) != 0 {
		t.Fatalf("wanted %+v", w)
	}
	// Asked for: the second copy is missing, the first is fine.
	m, _ = st.Movie(ctx, m.ID)
	m.SecondProfileID = uhd.ID
	if err := st.UpdateMovie(ctx, m); err != nil {
		t.Fatal(err)
	}
	if w := wanted(); len(w) != 1 || w[0].Copy != ModeSecond || w[0].Upgrade || w[0].Cutoff != uhd.Cutoff {
		t.Fatalf("wanted %+v", w)
	}
	// By hand, the first copy's list is the first copy's alone.
	res, err := s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Releases {
		if c.Accepted || c.mode != "" {
			t.Fatalf("for the first copy: %+v", c)
		}
	}
	// A search that may not take upgrades fetches the second copy only.
	before := len(*asked)
	r, searched, grabbed, err := s.findMovie(automatic(ctx), m.ID, false)
	if err != nil || searched != 1 || grabbed != 1 || !strings.HasPrefix(r.Message, "second copy: grabbed The.Matrix.1999.2160p.UHD.BluRay.x265-GRP") {
		t.Fatalf("found %+v %d %d %v", r, searched, grabbed, err)
	}
	// One search of the indexer, and one NZB.
	if n := len(*asked) - before; n != 2 {
		t.Fatalf("%d requests: %v", n, (*asked)[before:])
	}
	list, _ := st.Pending(ctx, "movie", m.ID)
	if len(list) != 1 || list[0].Mode != ModeSecond || list[0].Name != "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP" {
		t.Fatalf("downloads %+v", list)
	}
	// It's on its way: the second copy is busy, the first isn't.
	if b, _ := s.busyFor(ctx, "movie", m.ID, 0, "", true); !b {
		t.Fatal("the second copy isn't busy")
	}
	if b, _ := s.busy(ctx, "movie", m.ID, 0, ""); b {
		t.Fatal("the first copy is busy")
	}
	// Once it's in at the cutoff, nothing's wanted.
	st.SetDownloadStatus(ctx, list[0].ID, "completed", "")
	st.SetDownloadImported(ctx, list[0].ID, m.Path+"/b.mkv")
	if _, err := st.AddFile(ctx, store.File{Kind: store.SecondKind, OwnerID: m.ID, Path: m.Path + "/b.mkv", Quality: 16}); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Movie(ctx, m.ID)
	if m.File == nil || m.File.Quality != 11 || m.Second == nil || m.Second.Quality != 16 {
		t.Fatalf("copies %+v %+v", m.File, m.Second)
	}
	if w := wanted(); len(w) != 0 {
		t.Fatalf("wanted %+v", w)
	}
	// A second copy of the first copy's quality is never taken.
	m.SecondProfileID = ps[0].ID // Any
	st.UpdateMovie(ctx, m)
	st.DeleteFile(ctx, m.Second.ID)
	res2, err := s.MovieFor(ctx, m.ID, Ask{Mode: ModeSecond})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res2.Releases {
		if c.GUID == "hd" && (c.Accepted || !strings.Contains(strings.Join(c.Rejections, ";"), "the first copy is")) {
			t.Fatalf("the first copy's quality: %+v", c)
		}
		if c.mode != ModeSecond || !strings.HasSuffix(c.Key, "2") {
			t.Fatalf("not for the second copy: %+v", c)
		}
	}
}

// With both copies missing, one search grabs a release for each.
func TestSecondCopyBothMissing(t *testing.T) {
	srv, _ := indexer(t,
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "hd", 9e9),
		item("The.Matrix.1999.2160p.WEB-DL.x265-GRP", "uhdweb", 20e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	st.SetSetting(ctx, SettingMinAge, "0")
	ps, _ := st.Profiles(ctx)
	m.SecondProfileID = ps[2].ID
	st.UpdateMovie(ctx, m)
	r, _, grabbed, err := s.findMovie(automatic(ctx), m.ID, false)
	if err != nil || grabbed != 2 || !strings.Contains(r.Message, "; second copy: grabbed ") {
		t.Fatalf("found %+v %d %v", r, grabbed, err)
	}
	list, _ := st.Pending(ctx, "movie", m.ID)
	modes := map[string]string{}
	for _, d := range list {
		modes[d.Mode] = d.Name
	}
	if len(list) != 2 || modes[""] != "The.Matrix.1999.1080p.BluRay.x264-GRP" || modes[ModeSecond] != "The.Matrix.1999.2160p.WEB-DL.x265-GRP" {
		t.Fatalf("downloads %+v", modes)
	}
}
