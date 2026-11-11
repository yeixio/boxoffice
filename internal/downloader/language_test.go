package downloader

import (
	"context"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// A replacement for a copy reported in the wrong language is turned down
// when its sound is in another language again.
func TestDownloadWrongLanguage(t *testing.T) {
	smallMedia(t)
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	tracks := []string{"de"}
	e.AudioLanguages = func(context.Context, string) ([]string, error) { return tracks, nil }
	enable(t, st, Settings{})
	run(t, e)
	ctx := context.Background()
	movie := aMovie(t, st)
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: movie, Want: store.WantReplace, Problem: store.ProblemLanguage, Language: "en"})

	get := func(name string) store.Download {
		nzb := postFiles(srv, name, [][2]any{{strings.ToLower(name) + ".mkv", []byte(strings.Repeat("video ", 2000))}})
		d, err := e.Add(ctx, store.Download{Source: "search", Kind: "movie", TitleID: movie}, nzb)
		if err != nil {
			t.Fatal(err)
		}
		return waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	}
	if d := get("Movie.2024.GERMAN.1080p"); d.Status != store.DownloadFailed || !strings.Contains(d.Message, "sound is in German, and English was asked for") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	tracks = []string{"de", "en"}
	if d := get("Movie.2024.German.DL.1080p"); d.Status != store.DownloadCompleted {
		t.Fatalf("both languages: %s: %s", d.Status, d.Message)
	}
	// Untagged tracks can't tell.
	tracks = nil
	if d := get("Movie.2024.1080p"); d.Status != store.DownloadCompleted {
		t.Fatalf("untagged: %s: %s", d.Status, d.Message)
	}
	// Nothing reported: anything goes.
	st.SetTitleWant(ctx, store.TitleWant{Kind: "movie", OwnerID: movie})
	tracks = []string{"de"}
	if d := get("Movie.2024.GERMAN.720p"); d.Status != store.DownloadCompleted {
		t.Fatalf("not reported: %s: %s", d.Status, d.Message)
	}
}
