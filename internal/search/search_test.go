package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

const feed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
%s
</channel></rss>`

func item(title, guid string, size int64) string {
	return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><link>{{base}}/getnzb/%s?apikey=k</link><pubDate>Mon, 02 Jan 2026 15:04:05 +0000</pubDate>
<enclosure url="{{base}}/getnzb/%s?apikey=k" length="%d" type="application/x-nzb"/>
<newznab:attr name="category" value="2040"/><newznab:attr name="grabs" value="12"/></item>`, title, guid, guid, guid, size)
}

const nzbBody = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="&quot;a.mkv&quot; yEnc (1/1)"><segments><segment bytes="10" number="1">a@b</segment></segments></file></nzb>`

func indexer(t *testing.T, items ...string) (*httptest.Server, *[]string) {
	var asked []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RawQuery)
		if strings.HasPrefix(r.URL.Path, "/getnzb/") {
			io.WriteString(w, nzbBody)
			return
		}
		if r.URL.Query().Get("apikey") != "k" {
			io.WriteString(w, `<error code="100" description="Incorrect user credentials"/>`)
			return
		}
		if r.URL.Query().Get("t") == "caps" {
			io.WriteString(w, `<caps><searching><movie-search available="yes"/><tv-search available="yes"/></searching><categories><category id="2000" name="Movies"><subcat id="2040" name="HD"/></category></categories></caps>`)
			return
		}
		io.WriteString(w, strings.ReplaceAll(fmt.Sprintf(feed, strings.Join(items, "\n")), "{{base}}", srv.URL))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func setup(t *testing.T, srvURL string) (*Service, *store.Store, store.Movie) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertIndexer(ctx, store.Indexer{Name: "Test", Protocol: "usenet", Kind: "newznab", URL: srvURL, APIKey: "k", EnableSearch: true, Categories: []int{2000, 5000}}); err != nil {
		t.Fatal(err)
	}
	st.UpsertIndexer(ctx, store.Indexer{Name: "Torrents", Protocol: "torrent", Kind: "torznab", URL: "http://nowhere.invalid", APIKey: "x", EnableSearch: true})
	ps, _ := st.Profiles(ctx)
	hd := ps[1] // HD 1080p
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 603, IMDBID: "tt0133093", Title: "The Matrix", Year: 1999, ProfileID: hd.ID, RootFolder: "/m", Path: "/m/The Matrix (1999)", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := &downloader.Engine{Store: st, Dir: t.TempDir(), Log: log}
	return &Service{Store: st, Engine: eng, Client: &Client{}, Log: log}, st, m
}

func TestMovieSearch(t *testing.T) {
	srv, asked := indexer(t,
		item("The.Matrix.1999.720p.BluRay.x264-GRP", "a", 5e9),
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "b", 9e9),
		item("The.Matrix.1999.1080p.WEB-DL.DDP5.1-GRP", "c", 7e9),
		item("The.Matrix.Reloaded.2003.1080p.BluRay-GRP", "d", 9e9),
		item("The.Matrix.1999.1080p.WEB-DL.Blocked-GRP", "e", 7e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.Block(ctx, store.Blocked{Kind: "movie", TitleID: m.ID, SourceTitle: "The.Matrix.1999.1080p.WEB-DL.Blocked-GRP"})
	res, err := s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*asked)[0], "imdbid=0133093") || !strings.Contains((*asked)[0], "cat=2000") {
		t.Fatalf("asked %v", *asked)
	}
	if res.Searched != 1 || len(res.Releases) != 5 {
		t.Fatalf("results %+v", res)
	}
	if got := res.Releases[0]; got.Title != "The.Matrix.1999.1080p.BluRay.x264-GRP" || !got.Accepted {
		t.Fatalf("best %+v", got)
	}
	for _, c := range res.Releases {
		switch c.GUID {
		case "a":
			if c.Accepted {
				t.Error("took 720p for an HD 1080p profile")
			}
		case "d":
			if c.Accepted {
				t.Error("took the 2003 sequel")
			}
		case "e":
			if c.Accepted {
				t.Error("took a blocked release")
			}
		}
	}
	// Downloading is off: grabbing is refused, and the job says what it found.
	if _, err := s.Grab(ctx, res.Releases[0].Key); err != ErrOff {
		t.Fatalf("grab while off: %v", err)
	}
	r, err := s.jobMovie(ctx, fmt.Sprintf(`{"id":%d}`, m.ID))
	if err != nil || r.Status != "skipped" || !strings.Contains(r.Message, "downloading is off") {
		t.Fatalf("job %+v %v", r, err)
	}
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	d, err := s.Grab(ctx, res.Releases[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "movie" || d.TitleID != m.ID || d.Quality != 11 {
		t.Fatalf("download %+v", d)
	}
}

func TestCaps(t *testing.T) {
	srv, _ := indexer(t)
	c := &Client{}
	caps, err := c.Caps(context.Background(), store.Indexer{Name: "Test", URL: srv.URL, APIKey: "k"})
	if err != nil || !caps.MovieSearch || len(caps.Categories) != 1 || caps.Categories[0].Subs[0].ID != 2040 {
		t.Fatalf("%+v %v", caps, err)
	}
	if _, err := c.Caps(context.Background(), store.Indexer{Name: "Test", URL: srv.URL, APIKey: "bad"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("bad key: %v", err)
	}
}

func TestParsing(t *testing.T) {
	if y := releaseYear("2001.A.Space.Odyssey.1968.1080p"); y != 1968 {
		t.Errorf("year %d", y)
	}
	s, e, ok := releaseEpisode("Show.Name.S02E03E04.720p")
	if !ok || s != 2 || len(e) != 2 || e[1] != 4 {
		t.Errorf("episode %d %v", s, e)
	}
	if _, e, ok := releaseEpisode("Show.Name.S03.1080p"); !ok || len(e) != 0 {
		t.Errorf("season pack %v", e)
	}
	if !titleMatches("The.Office.US.S01E01", "The Office") || titleMatches("The.Officer.S01E01", "The Office") {
		t.Error("title matching")
	}
}

// A search that finds nothing sends an empty list, not null: the web page
// counts the releases, and null left it blank.
func TestResultsNoReleasesIsEmptyList(t *testing.T) {
	b, err := json.Marshal(Results{Searched: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"releases":[],"searched":2}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	b, err = json.Marshal(Results{Releases: []Candidate{{Key: "k", Accepted: true}}, Errors: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Releases []map[string]any `json:"releases"`
		Errors   []string         `json:"errors"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Releases) != 1 || back.Releases[0]["key"] != "k" || len(back.Errors) != 1 {
		t.Fatalf("got %s", b)
	}
}

// Releases the indexer says are locked with a password are passed over,
// unless the password comes with them.
func TestLockedReleases(t *testing.T) {
	attr := func(title, guid, value string) string {
		return strings.Replace(item(title, guid, 9e9), "</item>", `<newznab:attr name="password" value="`+value+`"/></item>`, 1)
	}
	srv, _ := indexer(t,
		attr("The.Matrix.1999.1080p.BluRay.x264-RAR", "a", "1"),
		attr("The.Matrix.1999.1080p.BluRay.x264-FILES", "b", "2"),
		attr("The.Matrix.1999.1080p.BluRay.x264-GIVEN", "c", "hunter2"),
		attr("The.Matrix.1999.1080p.BluRay.x264-NAMED{{hunter2}}", "d", "1"),
		attr("The.Matrix.1999.1080p.BluRay.x264-OPEN", "e", "0"),
	)
	s, _, m := setup(t, srv.URL)
	res, err := s.Movie(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a": false, "b": false, "c": true, "d": true, "e": true}
	for _, c := range res.Releases {
		if c.Accepted != want[c.GUID] {
			t.Errorf("%s: accepted %v (%v)", c.Title, c.Accepted, c.Rejections)
		}
		if c.GUID == "b" && c.Password != "" {
			t.Errorf("took %q for a password", c.Password)
		}
		if c.GUID == "c" && c.Password != "hunter2" {
			t.Errorf("password %q", c.Password)
		}
	}
}
