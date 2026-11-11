package store

import (
	"context"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A file that couldn't be read for now, its share not answering, is
// looked inside again a day later; one that's unreadable for good isn't.
func TestFilesToCheckRetriesStalled(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	profiles, err := st.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := int64(0)
	add := func(path string, media quality.Media) File {
		n++
		m, err := st.AddMovie(ctx, Movie{TMDBID: n, Title: path, ProfileID: profiles[0].ID, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		f, err := st.AddFile(ctx, File{Kind: "movie", OwnerID: m.ID, Path: path + "/m.mkv", Size: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveCheck(ctx, f, media, 0); err != nil {
			t.Fatal(err)
		}
		return f
	}
	stalled := add("/movies/A", quality.Media{Error: "ffprobe: couldn't be read for now: it took more than a minute to answer", Retry: true})
	killed := add("/movies/B", quality.Media{Error: "ffprobe: signal: killed"}) // saved before Retry
	add("/movies/C", quality.Media{Error: "ffprobe: Invalid data found when processing input"})
	add("/movies/D", quality.Media{Width: 1920, Height: 1080})

	ids := func() []int64 {
		t.Helper()
		list, err := st.FilesToCheck(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, f := range list {
			out = append(out, f.ID)
		}
		return out
	}
	if got := ids(); len(got) != 0 {
		t.Fatalf("just looked at, yet to check: %v", got)
	}
	if _, err := st.exec(ctx, "UPDATE media_files SET probed_at = probed_at - 25*3600"); err != nil {
		t.Fatal(err)
	}
	if got := ids(); len(got) != 2 || got[0] != stalled.ID || got[1] != killed.ID {
		t.Fatalf("a day later, to check: %v, want %d and %d", got, stalled.ID, killed.ID)
	}
}
