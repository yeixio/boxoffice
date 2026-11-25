package store_test

import (
	"context"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// Folders named with accents are matched by what's inside them: SQL's
// substr counts characters, not bytes, so "Amélie (2001)/" is 14 long.
func TestFolderPrefixesWithAccents(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 194, Title: "Amélie", ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Amélie (2001)"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Other", ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Other"})
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: other.ID, Path: "/m/Amélie (2001)/other.mkv"}); err != nil {
		t.Fatal(err)
	}
	if why, err := st.OtherTitleIn(ctx, m.Path, "movie", m.ID); err != nil || why == "" {
		t.Fatalf("another title's file in an accented folder wasn't seen: %q %v", why, err)
	}
	if why, err := st.OthersFilesIn(ctx, m.Path, "movie", m.ID); err != nil || why == "" {
		t.Fatalf("OthersFilesIn: %q %v", why, err)
	}
	own, _ := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: "/m/Amélie (2001)/Amélie.mkv"})
	if err := st.MoveTitleFolder(ctx, "movie", m.ID, m.Path, "/m/Amelie"); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.FileByID(ctx, own.ID); f.Path != "/m/Amelie/Amélie.mkv" {
		t.Fatalf("file not moved along: %q", f.Path)
	}
}

// A title's own other copies in its folder (a movie's edition or second
// copy, an episode's second copy) aren't another title's files.
func TestOwnCopiesArentAnotherTitles(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	profiles, _ := st.Profiles(ctx)
	m, _ := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Heat", ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Heat"})
	for _, kind := range []string{"movie", "edition", store.SecondKind} {
		if _, err := st.AddFile(ctx, store.File{Kind: kind, OwnerID: m.ID, Path: "/m/Heat/" + kind + ".mkv"}); err != nil {
			t.Fatal(err)
		}
	}
	show, _ := st.AddSeries(ctx, store.Series{TMDBID: 2, Title: "Show", ProfileID: profiles[0].ID, RootFolder: "/tv", Path: "/tv/Show"})
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: 1})
	eps, _ := st.Episodes(ctx, show.ID)
	for _, kind := range []string{"episode", store.SecondEpisodeKind} {
		if _, err := st.AddFile(ctx, store.File{Kind: kind, OwnerID: eps[0].ID, Path: "/tv/Show/" + kind + ".mkv"}); err != nil {
			t.Fatal(err)
		}
	}
	for kind, at := range map[string]struct {
		path string
		id   int64
	}{"movie": {m.Path, m.ID}, "series": {show.Path, show.ID}} {
		if why, err := st.OtherTitleIn(ctx, at.path, kind, at.id); err != nil || why != "" {
			t.Fatalf("%s OtherTitleIn: %q %v", kind, why, err)
		}
		if why, err := st.OthersFilesIn(ctx, at.path, kind, at.id); err != nil || why != "" {
			t.Fatalf("%s OthersFilesIn: %q %v", kind, why, err)
		}
	}
}
