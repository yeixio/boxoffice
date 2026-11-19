package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
)

// The calendar feed opens with the token in its address and nothing else,
// and a new address closes the old one.
func TestCalendarFeed(t *testing.T) {
	s, hs := portalServer(t)
	ctx := context.Background()
	ps, _ := s.Store.Profiles(ctx)
	soon := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 5)
	if _, err := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Soon; Really, Soon", Year: 2026, ProfileID: ps[0].ID, RootFolder: "/m", Path: "/m/Soon (2026)", Monitored: true, DigitalRelease: &soon}); err != nil {
		t.Fatal(err)
	}
	fetch := func(path string) (int, string, string) {
		res, err := http.Get(hs.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), string(b)
	}
	// Before anyone has asked for the address there's no feed at all.
	if code, _, _ := fetch("/api/v1/calendar.ics?token="); code != 404 {
		t.Fatalf("no token yet: %d", code)
	}
	var feed calendarFeedView
	if code := call(t, hs, "GET", "/api/v1/calendar/feed", "", &feed); code != 200 || !strings.HasPrefix(feed.Path, "/api/v1/calendar.ics?token=") || len(feed.Path) < 60 {
		t.Fatalf("feed: %d %+v", code, feed)
	}
	var again calendarFeedView
	call(t, hs, "GET", "/api/v1/calendar/feed", "", &again)
	if again != feed {
		t.Fatal("the address changed by being read")
	}
	code, ctype, body := fetch(feed.Path)
	if code != 200 || !strings.HasPrefix(ctype, "text/calendar") {
		t.Fatalf("ics: %d %s", code, ctype)
	}
	for _, want := range []string{"BEGIN:VCALENDAR\r\n", "UID:movie-1-digital@boxoffice\r\n", "DTSTART;VALUE=DATE:" + soon.Format("20060102") + "\r\n",
		"DTEND;VALUE=DATE:" + soon.AddDate(0, 0, 1).Format("20060102") + "\r\n", `SUMMARY:Soon\; Really\, Soon (2026): digital release` + "\r\n", "END:VCALENDAR\r\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("the feed lacks %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"/api/v1/calendar.ics", "/api/v1/calendar.ics?token=wrong", "/api/v1/calendar.ics?token=key"} {
		if code, _, _ := fetch(bad); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	// The token opens nothing else.
	if code, _, _ := fetch("/api/v1/movies?token=" + strings.TrimPrefix(feed.Path, "/api/v1/calendar.ics?token=")); code != 401 {
		t.Errorf("movies with the feed's token: %d", code)
	}
	var renewed calendarFeedView
	if code := call(t, hs, "POST", "/api/v1/calendar/feed/renew", "", &renewed); code != 200 || renewed == feed {
		t.Fatalf("renew: %d %+v", code, renewed)
	}
	if code, _, _ := fetch(feed.Path); code != 404 {
		t.Errorf("the old address: %d", code)
	}
	if code, _, _ := fetch(renewed.Path); code != 200 {
		t.Errorf("the new address: %d", code)
	}
}

func TestICSFolding(t *testing.T) {
	long := strings.Repeat("é", 100)
	out := icsFeed([]catalog.Upcoming{{Kind: "episode", Release: "airs", Date: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), Title: long, SeriesID: 7, Show: "Show", Season: 2, Episode: 3, HasFile: true}}, time.Now())
	for _, l := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(l) > 75 || strings.ToValidUTF8(l, "") != l {
			t.Fatalf("line of %d bytes: %q", len(l), l)
		}
	}
	if joined := strings.ReplaceAll(out, "\r\n ", ""); !strings.Contains(joined, "SUMMARY:Show 2x03: "+long+"\r\n") || !strings.Contains(joined, "UID:episode-7-2-3@boxoffice") || !strings.Contains(joined, "DESCRIPTION:In the library.") {
		t.Fatalf("unfolded:\n%s", joined)
	}
}
