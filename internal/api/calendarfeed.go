package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
)

// The calendar feed: Coming up as an iCalendar file a phone's or desktop's
// calendar subscribes to, as Sonarr and Radarr have. Calendar apps fetch it
// by themselves with no way to sign in, so the address carries a token of
// its own. The token only opens the feed: it isn't the API key, and can be
// handed to a calendar service without giving anything else away.

const settingCalendarToken = "calendar_token"

// How far back and ahead the feed reaches.
const (
	feedBack  = 30 * 24 * time.Hour
	feedAhead = 180 * 24 * time.Hour
)

type calendarFeedView struct {
	// Path is the feed's address on this server, token and all.
	Path string `json:"path"`
}

func (s *Server) calendarToken(r *http.Request, renew bool) (string, error) {
	tok, err := s.Store.Setting(r.Context(), settingCalendarToken)
	if err != nil {
		return "", err
	}
	if tok == "" || renew {
		b := make([]byte, 24)
		rand.Read(b)
		tok = hex.EncodeToString(b)
		if err := s.Store.SetSetting(r.Context(), settingCalendarToken, tok); err != nil {
			return "", err
		}
	}
	return tok, nil
}

// calendarFeed gives the feed's address, making its token the first time.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) error {
	tok, err := s.calendarToken(r, false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, calendarFeedView{Path: "/api/v1/calendar.ics?token=" + tok})
}

// renewCalendarFeed changes the token, so the old address stops working.
func (s *Server) renewCalendarFeed(w http.ResponseWriter, r *http.Request) error {
	tok, err := s.calendarToken(r, true)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, calendarFeedView{Path: "/api/v1/calendar.ics?token=" + tok})
}

// calendarICS answers a calendar app with the feed.
func (s *Server) calendarICS(w http.ResponseWriter, r *http.Request) error {
	tok, err := s.Store.Setting(r.Context(), settingCalendarToken)
	if err != nil {
		return err
	}
	got := r.URL.Query().Get("token")
	if tok == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
		return errorf(http.StatusNotFound, "there's no calendar at this address; copy it again from Coming up")
	}
	now := time.Now()
	up, err := s.Catalog.Calendar(r.Context(), now.Add(-feedBack), now.Add(feedAhead))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="boxoffice.ics"`)
	w.Header().Set("Cache-Control", "private, max-age=900")
	_, err = w.Write([]byte(icsFeed(up, now)))
	return err
}

var releaseWords = map[string]string{"cinemas": "in cinemas", "digital": "digital release", "physical": "on disc"}

// icsFeed writes what's coming up as an iCalendar file of all-day events.
func icsFeed(up []catalog.Upcoming, now time.Time) string {
	var b strings.Builder
	line := func(s string) {
		// Lines are folded at 75 bytes, never inside a character.
		for len(s) > 75 {
			cut := 75
			for cut > 0 && s[cut]&0xC0 == 0x80 {
				cut--
			}
			b.WriteString(s[:cut] + "\r\n ")
			s = s[cut:]
		}
		b.WriteString(s + "\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Box Office//Coming up//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Box Office")
	line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	line("X-PUBLISHED-TTL:PT1H")
	stamp := now.UTC().Format("20060102T150405Z")
	for _, u := range up {
		var uid, summary string
		if u.Kind == "movie" {
			uid = fmt.Sprintf("movie-%d-%s@boxoffice", u.MovieID, u.Release)
			summary = u.Title
			if u.Year > 0 {
				summary += fmt.Sprintf(" (%d)", u.Year)
			}
			summary += ": " + releaseWords[u.Release]
		} else {
			uid = fmt.Sprintf("episode-%d-%d-%d@boxoffice", u.SeriesID, u.Season, u.Episode)
			summary = fmt.Sprintf("%s %dx%02d", u.Show, u.Season, u.Episode)
			if u.Title != "" {
				summary += ": " + u.Title
			}
		}
		// Release and air dates are days, kept at midnight UTC.
		day := u.Date.UTC()
		line("BEGIN:VEVENT")
		line("UID:" + uid)
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + day.Format("20060102"))
		line("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + icsText(summary))
		if u.HasFile {
			line("DESCRIPTION:" + icsText("In the library."))
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

// icsText escapes text for an iCalendar value.
func icsText(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", "").Replace(s)
}
