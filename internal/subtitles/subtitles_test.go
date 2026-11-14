package subtitles

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func init() {
	pause = 0
}

const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello there.\n"

// fake is a provider with set answers.
type fake struct {
	name     string
	mu       sync.Mutex
	found    map[string][]Found // by title
	quota    bool
	searches int
	fetched  []string
}

func (f *fake) Name() string { return f.name }

func (f *fake) Search(_ context.Context, q Query) ([]Found, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	var out []Found
	for _, x := range f.found[q.Title] {
		if slices.Contains(q.Languages, x.Language) {
			x.Provider = f.name
			out = append(out, x)
		}
	}
	return out, nil
}

func (f *fake) Download(_ context.Context, x Found) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.quota {
		return nil, "", ErrQuota
	}
	f.fetched = append(f.fetched, x.ID)
	return []byte(strings.ReplaceAll(srt, "Hello there.", "From "+x.ID)), x.ID + ".srt", nil
}

type world struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	dir string
	svc *Service
	p   *fake
	n   int64
	now time.Time
}

func newWorld(t *testing.T, c Config) *world {
	ctx := context.Background()
	w := &world{t: t, ctx: ctx, st: storetest.Open(t), dir: t.TempDir(), p: &fake{name: "fakesubs", found: map[string][]Found{}},
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	w.svc = &Service{Store: w.st, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return w.now },
		Providers: func(Config) []Provider { return []Provider{w.p} },
		Probe:     func(context.Context, string) ([]string, error) { return nil, nil }}
	c.On = true
	if err := SaveConfig(ctx, w.st, c); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) profile(name string) int {
	ps, _ := w.st.Profiles(w.ctx)
	for _, p := range ps {
		if p.Name == name {
			return p.ID
		}
	}
	w.t.Fatalf("no profile %q", name)
	return 0
}

// movie adds a made-up movie with a file on disk.
func (w *world) movie(title, file string, profile int) store.File {
	w.t.Helper()
	w.n++
	m, err := w.st.AddMovie(w.ctx, store.Movie{TMDBID: w.n, Title: title, Year: 2001, ProfileID: profile, Path: filepath.Join(w.dir, title)})
	if err != nil {
		w.t.Fatal(err)
	}
	path := filepath.Join(w.dir, title, file)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("not really a video"), 0o644)
	f, err := w.st.AddFile(w.ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: path, Size: 18, Quality: 7})
	if err != nil {
		w.t.Fatal(err)
	}
	return f
}

func (w *world) states(f store.File) map[string]store.Subtitle {
	w.t.Helper()
	rows, err := w.st.SubtitlesOf(w.ctx, f.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]store.Subtitle{}
	for _, r := range rows[f.ID] {
		out[r.Language] = r
	}
	return out
}

func (w *world) pass() {
	w.t.Helper()
	if err := w.svc.Pass(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

func TestPassFetchesWhatsMissing(t *testing.T) {
	w := newWorld(t, Config{Languages: []string{"en", "fr", "de", "es"}})
	hd := w.profile("HD 1080p")
	f := w.movie("Lanterns at Dusk", "Lanterns at Dusk (2001).mkv", hd)
	// French is inside the file, German beside it already.
	w.svc.Probe = func(context.Context, string) ([]string, error) { return []string{"fr"}, nil }
	os.WriteFile(filepath.Join(filepath.Dir(f.Path), "Lanterns at Dusk (2001).ger.srt"), []byte(srt), 0o644)
	w.p.found["Lanterns at Dusk"] = []Found{
		{ID: "popular", Language: "en", Release: "Lanterns.at.Dusk.2001.720p.HDTV-OTHER", Downloads: 900},
		{ID: "machine", Language: "en", Machine: true, Downloads: 5000},
		{ID: "exact", Language: "en", MatchesFile: true, Downloads: 3},
		{ID: "french", Language: "fr"},
	}
	w.pass()
	got := w.states(f)
	if got["fr"].State != store.SubEmbedded || got["de"].State != store.SubOnDisk {
		t.Fatalf("fr %q, de %q: want embedded and on_disk", got["fr"].State, got["de"].State)
	}
	en := got["en"]
	if en.State != store.SubFetched || en.Provider != "fakesubs" || !en.MatchesFile {
		t.Fatalf("en: %+v", en)
	}
	want := filepath.Join(filepath.Dir(f.Path), "Lanterns at Dusk (2001).en.srt")
	if en.Path != want {
		t.Fatalf("saved at %q, want %q", en.Path, want)
	}
	if b, _ := os.ReadFile(want); !strings.Contains(string(b), "From exact") {
		t.Fatalf("saved %q, want the one made for the file", b)
	}
	if got["es"].State != store.SubMissing || got["es"].Tries != 1 || !got["es"].NextAt.After(w.now) {
		t.Fatalf("es: %+v", got["es"])
	}
	if !slices.Equal(w.p.fetched, []string{"exact"}) {
		t.Fatalf("fetched %v: only the French inside and German beside weren't wanted", w.p.fetched)
	}

	// Nothing is due again until Spanish's time comes, which for a new
	// file is hours, not days.
	searches := w.p.searches
	w.pass()
	if w.p.searches != searches {
		t.Fatal("looked again too soon")
	}
	w.now = w.now.Add(7 * time.Hour)
	w.p.found["Lanterns at Dusk"] = append(w.p.found["Lanterns at Dusk"], Found{ID: "spanish", Language: "es"})
	w.pass()
	if got := w.states(f); got["es"].State != store.SubFetched {
		t.Fatalf("es after a retry: %+v", got["es"])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.Path), "Lanterns at Dusk (2001).en.srt")); err != nil {
		t.Fatal(err)
	}
}

func TestLeavesDoNotTouchAlone(t *testing.T) {
	w := newWorld(t, Config{Languages: []string{"en"}})
	f := w.movie("Quiet Harbour", "Quiet Harbour.mkv", w.profile(quality.DoNotTouchName))
	w.p.found["Quiet Harbour"] = []Found{{ID: "x", Language: "en"}}
	w.pass()
	if len(w.states(f)) != 0 || w.p.searches != 0 {
		t.Fatal("looked up a Do not touch title")
	}
}

func TestDailyLimitAndFindNow(t *testing.T) {
	w := newWorld(t, Config{Languages: []string{"en"}, DailyLimit: 1})
	hd := w.profile("HD 1080p")
	a := w.movie("Paper Comets", "Paper Comets.mkv", hd)
	b := w.movie("Glass Orchard", "Glass Orchard.mkv", hd)
	w.pass()
	if w.p.searches != 1 {
		t.Fatalf("%d searches, want the day's one", w.p.searches)
	}
	// The newest goes first.
	if len(w.states(b)) != 1 || len(w.states(a)) != 0 {
		t.Fatal("didn't look up the newest file first")
	}
	// Asked for by hand, it's looked up whatever the limit.
	if err := w.svc.FindNow(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	w.pass()
	if len(w.states(a)) != 1 {
		t.Fatal("Find now didn't look past the day's limit")
	}
	st, err := w.svc.State(w.ctx, Config{Languages: []string{"en"}, DailyLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 2 || st.Missing != 2 || st.Today.Looked != 2 {
		t.Fatalf("status %+v", st)
	}
}

func TestUsedUpWaitsForTomorrow(t *testing.T) {
	w := newWorld(t, Config{Languages: []string{"en"}})
	f := w.movie("Iron Meadow", "Iron Meadow.mkv", w.profile("HD 1080p"))
	w.p.found["Iron Meadow"] = []Found{{ID: "x", Language: "en"}}
	w.p.quota = true
	w.pass()
	got := w.states(f)["en"]
	if got.State != store.SubWaiting || got.Tries != 0 || got.NextAt.Day() != 15 {
		t.Fatalf("en: %+v", got)
	}
	w.p.quota = false
	w.now = w.now.Add(13 * time.Hour) // the next UTC day
	w.pass()
	if got := w.states(f)["en"]; got.State != store.SubFetched {
		t.Fatalf("en the next day: %+v", got)
	}
}

func TestRetryAfter(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		tries int
		age   time.Duration
		want  time.Duration
	}{
		{1, time.Hour, 6 * time.Hour}, {8, 13 * day, 6 * time.Hour}, {9, 13 * day, 30 * day},
		{1, 100 * day, day}, {2, 100 * day, 3 * day}, {3, 100 * day, 7 * day}, {6, 100 * day, 30 * day},
	} {
		if got := RetryAfter(c.tries, c.age); got != c.want {
			t.Errorf("RetryAfter(%d, %v) = %v, want %v", c.tries, c.age, got, c.want)
		}
	}
}
