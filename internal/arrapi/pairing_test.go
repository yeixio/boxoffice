package arrapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
)

type asked struct {
	kind                string
	id                  int64
	edition, mode, want string
}

// pairedSetup serves the arr API with what Box Office offers Matinee,
// recording the searches asked for.
func pairedSetup(t *testing.T, live arrapi.LiveState) (env, *[]asked) {
	t.Helper()
	e := setup(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var got []asked
	p := &arrapi.Pairing{
		Live:      func() arrapi.LiveState { return live },
		Waiting:   func() string { return "" },
		CanImport: func(d store.Download) bool { return d.Status == store.DownloadCompleted && d.ImportedTo == "" },
		Editions: func(ctx context.Context, id int64, owned []string) (any, []string, error) {
			return []map[string]any{{"edition": "Director's Cut", "releases": 2}}, nil, nil
		},
		Ask: func(ctx context.Context, kind string, id int64, subject, edition, mode, want string) error {
			got = append(got, asked{kind, id, edition, mode, want})
			return nil
		},
	}
	keyFn := func(context.Context) string { return key }
	mux := http.NewServeMux()
	mux.Handle("/radarr/", http.StripPrefix("/radarr", arrapi.NewPaired(arrapi.Radarr, e.cat, keyFn, "test", log, p)))
	mux.Handle("/sonarr/", http.StripPrefix("/sonarr", arrapi.NewPaired(arrapi.Sonarr, e.cat, keyFn, "test", log, p)))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	e.url = ts.URL
	return e, &got
}

func call(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func features(t *testing.T, url string) []string {
	var s struct {
		Features []string `json:"boxOfficeFeatures"`
	}
	if code := call(t, "GET", url+"/api/v3/system/status", nil, &s); code != 200 {
		t.Fatalf("status: %d", code)
	}
	return s.Features
}

// Without the pairing (a server without a downloader), nothing new is
// advertised or answered, so Matinee carries on as with Radarr.
func TestPairingOffWithoutDownloader(t *testing.T) {
	e := setup(t)
	if f := features(t, e.url+"/radarr"); slices.Contains(f, "progress") || slices.Contains(f, "editions") {
		t.Fatalf("features: %v", f)
	}
	if code := call(t, "GET", e.url+"/radarr/api/v3/boxoffice/activity", nil, nil); code != 404 {
		t.Fatalf("activity: %d", code)
	}
}

func TestActivity(t *testing.T) {
	ctx := context.Background()
	e, _ := pairedSetup(t, arrapi.LiveState{Speed: 1000})
	st := e.cat.Store
	if f := features(t, e.url+"/radarr"); !slices.Contains(f, "progress") || !slices.Contains(f, "editions") || !slices.Contains(f, "upgrades") {
		t.Fatalf("radarr features: %v", f)
	}
	if f := features(t, e.url+"/sonarr"); slices.Contains(f, "editions") || !slices.Contains(f, "progress") {
		t.Fatalf("sonarr features: %v", f)
	}
	profiles, _ := st.Profiles(ctx)
	mv, _ := st.AddMovie(ctx, store.Movie{ProfileID: profiles[0].ID, TMDBID: 603, Title: "The Matrix", SortTitle: "matrix", Year: 1999, RootFolder: e.movies, Path: e.movies + "/The Matrix (1999)"})
	other, _ := st.AddMovie(ctx, store.Movie{ProfileID: profiles[0].ID, TMDBID: 604, Title: "The Matrix Reloaded", SortTitle: "matrix reloaded", Year: 2003, RootFolder: e.movies, Path: e.movies + "/The Matrix Reloaded (2003)"})
	first, _ := st.AddDownload(ctx, store.Download{Name: "The.Matrix.1999.1080p", Kind: "movie", TitleID: mv.ID, Size: 10000, Quality: 11})
	first.DoneBytes = 4000
	st.SaveDownloadProgress(ctx, first)
	st.SetDownloadStatus(ctx, first.ID, store.DownloadDownloading, "")
	st.AddDownload(ctx, store.Download{Name: "The.Matrix.Reloaded.2003.Directors.Cut.1080p", Kind: "movie", TitleID: other.ID, Size: 5000, Edition: "Director's Cut", Mode: "alongside"})
	bad, _ := st.AddDownload(ctx, store.Download{Name: "bad", Kind: "movie", TitleID: other.ID, Size: 100})
	st.SetDownloadStatus(ctx, bad.ID, store.DownloadFailed, "par2 couldn't repair it")
	st.AddDownload(ctx, store.Download{Name: "not-for-a-title.nzb", Size: 100})

	var got struct {
		Speed int64 `json:"speed"`
		Items []struct {
			ID      int64   `json:"id"`
			TMDBID  int64   `json:"tmdbId"`
			Stage   string  `json:"stage"`
			Percent float64 `json:"percent"`
			ETA     int64   `json:"etaSeconds"`
			Edition string  `json:"edition"`
			Mode    string  `json:"mode"`
			Message string  `json:"message"`
		} `json:"items"`
	}
	// Sonarr's API answers with movies too: Matinee reads one of them.
	if code := call(t, "GET", e.url+"/sonarr/api/v3/boxoffice/activity", nil, &got); code != 200 {
		t.Fatalf("activity: %d", code)
	}
	if got.Speed != 1000 || len(got.Items) != 3 {
		t.Fatalf("activity: %+v", got)
	}
	a, b, c := got.Items[0], got.Items[1], got.Items[2]
	if a.TMDBID != 603 || a.Stage != "downloading" || a.Percent != 40 || a.ETA != 6 {
		t.Errorf("downloading: %+v", a)
	}
	// The second waits for the first: 6000 bytes ahead, 5000 its own.
	if b.TMDBID != 604 || b.Stage != "queued" || b.ETA != 11 || b.Edition != "Director's Cut" || b.Mode != "alongside" {
		t.Errorf("queued: %+v", b)
	}
	if c.Stage != "failed" || c.Message == "" {
		t.Errorf("failed: %+v", c)
	}
}

func TestAskEditionAndUpgrade(t *testing.T) {
	ctx := context.Background()
	e, got := pairedSetup(t, arrapi.LiveState{})
	st := e.cat.Store
	profiles, _ := st.Profiles(ctx)
	mv, _ := st.AddMovie(ctx, store.Movie{ProfileID: profiles[0].ID, TMDBID: 78, Title: "Blade Runner", SortTitle: "blade runner", Year: 1982, RootFolder: e.movies, Path: e.movies + "/Blade Runner (1982)"})

	// No copy yet: an edition asked for replaces nothing.
	var res map[string]any
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/editions", map[string]any{"tmdbId": 78, "edition": "directors cut", "mode": "alongside"}, &res); code != 202 {
		t.Fatalf("ask edition: %d %v", code, res)
	}
	if len(*got) != 1 || (*got)[0] != (asked{"movie", mv.ID, "Director's Cut", "replace", ""}) {
		t.Fatalf("asked: %+v", *got)
	}
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/editions", map[string]any{"tmdbId": 78, "edition": "x", "mode": "both"}, nil); code != 400 {
		t.Fatalf("bad mode: %d", code)
	}
	// Nothing to upgrade without a copy.
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/upgrade", map[string]any{"tmdbId": 78, "want": "better"}, nil); code != 409 {
		t.Fatalf("upgrade without a copy: %d", code)
	}

	path := mv.Path + "/Blade Runner (1982).mkv"
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: mv.ID, Path: path, Quality: 9})
	d, _ := st.AddDownload(ctx, store.Download{Name: "Blade.Runner.1982.1080p.WEBRip-BAD", Kind: "movie", TitleID: mv.ID})
	st.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, "")
	st.SetDownloadImported(ctx, d.ID, path)

	var ed struct {
		Current  string           `json:"current"`
		HasFile  bool             `json:"hasFile"`
		Editions []map[string]any `json:"editions"`
		Known    []string         `json:"known"`
	}
	if code := call(t, "GET", e.url+"/radarr/api/v3/boxoffice/editions?tmdbId=78", nil, &ed); code != 200 || !ed.HasFile || len(ed.Editions) != 1 || len(ed.Known) == 0 {
		t.Fatalf("editions: %d %+v", code, ed)
	}
	// Sonarr has no editions.
	if code := call(t, "GET", e.url+"/sonarr/api/v3/boxoffice/editions?tmdbId=78", nil, nil); code != 404 {
		t.Fatalf("sonarr editions: %d", code)
	}

	var up map[string]any
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/upgrade", map[string]any{"movieId": mv.ID, "want": "replace", "reason": "out of sync"}, &up); code != 202 {
		t.Fatalf("upgrade: %d %v", code, up)
	}
	if up["blocked"] != "Blade.Runner.1982.1080p.WEBRip-BAD" {
		t.Fatalf("blocked: %v", up)
	}
	blocked, _ := st.Blocklist(ctx, 10)
	if len(blocked) != 1 || blocked[0].Reason != "reported as a bad copy: out of sync" {
		t.Fatalf("blocklist: %+v", blocked)
	}
	tw, _ := st.TitleWant(ctx, "movie", mv.ID)
	if tw.Want != store.WantReplace || tw.Note != "out of sync" {
		t.Fatalf("want: %+v", tw)
	}
	if last := (*got)[len(*got)-1]; last != (asked{"movie", mv.ID, "", "", "replace"}) {
		t.Fatalf("asked: %+v", last)
	}
	// A new copy clears what was asked.
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: mv.ID, Path: path, Quality: 11})
	st.ClearWant(ctx, "movie", mv.ID)
	if tw, _ := st.TitleWant(ctx, "movie", mv.ID); tw.Want != "" {
		t.Fatalf("want after: %+v", tw)
	}

	// A problem reported in Matinee: the wrong language, wanted in English.
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/upgrade", map[string]any{"movieId": mv.ID, "want": "replace", "problem": "language", "language": "fr"}, nil); code != 202 {
		t.Fatalf("problem: %d", code)
	}
	if tw, _ := st.TitleWant(ctx, "movie", mv.ID); tw.Want != store.WantReplace || tw.Problem != store.ProblemLanguage || tw.Language != "fr" {
		t.Fatalf("problem want: %+v", tw)
	}
	for _, bad := range []map[string]any{
		{"movieId": mv.ID, "want": "replace", "problem": "language"},                   // which language?
		{"movieId": mv.ID, "want": "replace", "problem": "language", "language": "xx"}, // not one
		{"movieId": mv.ID, "want": "better", "problem": "wrong"},                       // a problem wants another copy
		{"movieId": mv.ID, "want": "replace", "problem": "smell"},
	} {
		if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/upgrade", bad, nil); code != 400 {
			t.Errorf("%v: %d", bad, code)
		}
	}
	// What's asked next replaces the report.
	if code := call(t, "POST", e.url+"/radarr/api/v3/boxoffice/upgrade", map[string]any{"movieId": mv.ID, "want": "replace", "problem": "wrong", "reportedBy": "Ellen"}, nil); code != 202 {
		t.Fatalf("problem wrong: %d", code)
	}
	if tw, _ := st.TitleWant(ctx, "movie", mv.ID); tw.Problem != store.ProblemWrong || tw.Language != "" || tw.ReportedBy != "Ellen" {
		t.Fatalf("problem want: %+v", tw)
	}
}
