package arrapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/yeixio/boxoffice/internal/notify"
	"github.com/yeixio/boxoffice/internal/store"
)

type feed struct {
	BoxOfficeID   string               `json:"boxOfficeId"`
	Last          int64                `json:"last"`
	Notifications []store.Notification `json:"notifications"`
}

func getFeed(t *testing.T, url string) feed {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Api-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%s: %d %s", url, res.StatusCode, b)
	}
	var f feed
	if err := json.NewDecoder(res.Body).Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

// Matinee reads what happened to downloads, with the ids it matches the
// people who asked by, from either API, and picks up where it left off.
func TestNotificationsFeed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	st := e.cat.Store
	profiles, _ := st.Profiles(ctx)
	mv, err := st.AddMovie(ctx, store.Movie{ProfileID: profiles[0].ID, TMDBID: 603, IMDBID: "tt0133093", Title: "The Matrix", SortTitle: "matrix", Year: 1999, RootFolder: e.movies, Path: e.movies + "/The Matrix (1999)"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &notify.Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d := store.Download{ID: 7, Name: "The.Matrix.1999.1080p", Kind: "movie", TitleID: mv.ID, Source: "search"}
	rec.Download(ctx, d, store.EventGrabbed, "")
	rec.Download(ctx, d, store.EventFailed, "par2 couldn't repair it")
	rec.Download(ctx, store.Download{ID: 8, Name: "something.nzb"}, store.EventDownloaded, "")

	f := getFeed(t, e.url+"/radarr/api/v3/boxoffice/notifications")
	if f.BoxOfficeID == "" || f.Last != 3 || len(f.Notifications) != 3 {
		t.Fatalf("feed: %+v", f)
	}
	g := f.Notifications[0]
	if g.Event != store.EventGrabbed || g.Audience != store.AudienceEveryone || g.Title != "Downloading The Matrix (1999)" ||
		g.Media == nil || g.Media.TMDBID != 603 || g.Media.IMDBID != "tt0133093" || g.Download != 7 {
		t.Fatalf("grabbed: %+v %+v", g, g.Media)
	}
	if fl := f.Notifications[1]; fl.Audience != store.AudienceAdmins || fl.Level != store.LevelWarning ||
		fl.Body != "Par2 couldn't repair it. Box Office blocked that release and is looking for another." {
		t.Fatalf("failed: %+v", fl)
	}
	if h := f.Notifications[2]; h.Media != nil || h.Audience != store.AudienceAdmins {
		t.Fatalf("by hand: %+v", h)
	}

	// Sonarr's side is the same Box Office and the same feed.
	s := getFeed(t, e.url+"/sonarr/api/v3/boxoffice/notifications?after=2")
	if s.BoxOfficeID != f.BoxOfficeID || len(s.Notifications) != 1 || s.Notifications[0].ID != 3 {
		t.Fatalf("after 2: %+v", s)
	}
	// Starting fresh skips the backlog.
	if n := getFeed(t, e.url+"/radarr/api/v3/boxoffice/notifications?after=-1"); n.Last != 3 || len(n.Notifications) != 0 {
		t.Fatalf("fresh: %+v", n)
	}
	// Ahead of the feed (an older backup was restored): from the start.
	if n := getFeed(t, e.url+"/radarr/api/v3/boxoffice/notifications?after=99"); len(n.Notifications) != 3 {
		t.Fatalf("ahead: %+v", n)
	}
}
