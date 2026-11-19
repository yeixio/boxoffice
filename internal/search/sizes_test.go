package search

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// A profile's size limits turn down a release that's too big or too small
// for the title's running time, by the codec in its name.
func TestMovieSizeLimits(t *testing.T) {
	srv, _ := indexer(t,
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "h264", 12e9),    // 88 MB a minute
		item("The.Matrix.1999.1080p.BluRay.x264-BIG", "h264big", 20e9), // 147
		item("The.Matrix.1999.1080p.WEB-DL.AV1-GRP", "av1", 3e9),       // 22
		item("The.Matrix.1999.1080p.WEB-DL.AV1-BIG", "av1big", 12e9),   // 88
		item("The.Matrix.1999.1080p.WEB-DL.x265-TINY", "tiny", 400e6),  // 2.9
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	p, _ := st.Profile(ctx, m.ProfileID)
	p.Sizes = quality.Sizes{H264: quality.SizeLimit{Max: 100}, HEVC: quality.SizeLimit{Min: 5, Max: 60}, AV1: quality.SizeLimit{Max: 25}}
	if err := st.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Profile(ctx, p.ID); got.Sizes != p.Sizes {
		t.Fatalf("kept %+v", got.Sizes)
	}
	// Without a running time nothing is turned down for its size.
	res, err := s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Releases {
		if !c.Accepted {
			t.Errorf("no running time, and %s was turned down: %v", c.GUID, c.Rejections)
		}
	}
	m.Runtime = 136
	if err := st.RefreshMovie(ctx, m); err != nil {
		t.Fatal(err)
	}
	res, err = s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"h264": "", "h264big": "at most 100 for H.264", "av1": "", "av1big": "at most 25 for AV1", "tiny": "at least 5 for H.265"}
	for _, c := range res.Releases {
		why, ok := want[c.GUID]
		if !ok {
			t.Fatalf("release %s", c.GUID)
		}
		if c.Accepted != (why == "") || !strings.Contains(strings.Join(c.Rejections, "; "), why) {
			t.Errorf("%s: accepted %v, %v; want %q", c.GUID, c.Accepted, c.Rejections, why)
		}
	}
}

// An episode is held to the show's running time, and a season pack to all
// its episodes'.
func TestEpisodeSizeLimits(t *testing.T) {
	srv, _ := indexer(t,
		item("Show.S01.1080p.WEB-DL.AV1-GRP", "pack", 4e9),      // 4 episodes of 45 minutes: 22 a minute
		item("Show.S01.1080p.WEB-DL.AV1-BIG", "packbig", 10e9),  // 56
		item("Show.S01E01.1080p.WEB-DL.AV1-GRP", "ep", 1e9),     // 22
		item("Show.S01E01.1080p.WEB-DL.AV1-BIG", "epbig", 3e9),  // 67
		item("Show.S01E01E02.1080p.WEB-DL.AV1-GRP", "two", 2e9), // 22 over two episodes
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	p, _ := st.Profile(ctx, m.ProfileID)
	p.Sizes = quality.Sizes{AV1: quality.SizeLimit{Max: 25}}
	if err := st.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1, TVDBID: 11, Title: "Show", Runtime: 45, ProfileID: p.ID, RootFolder: "/tv", Path: "/tv/Show",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	aired := time.Now().AddDate(0, 0, -7)
	for n := 1; n <= 4; n++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, AirDate: &aired, Monitored: true})
	}
	res, err := s.SeasonReleases(ctx, show.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"pack": true, "packbig": false, "ep": true, "epbig": false, "two": true}
	if len(res.Releases) != len(want) {
		t.Fatalf("releases %+v", res.Releases)
	}
	for _, c := range res.Releases {
		if c.Accepted != want[c.GUID] {
			t.Errorf("%s: accepted %v, %v", c.GUID, c.Accepted, c.Rejections)
		}
		if !c.Accepted && !strings.Contains(strings.Join(c.Rejections, "; "), "at most 25 for AV1") {
			t.Errorf("%s: %v", c.GUID, c.Rejections)
		}
	}
}
