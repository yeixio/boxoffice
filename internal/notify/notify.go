// Package notify records what Box Office tells people about: a download
// that started, a title that arrived in the library, a download that failed
// or looked unsafe. Box Office has no accounts of its own for the people
// who ask for titles (they ask in Matinee), so it doesn't send anything
// itself. Matinee reads the notifications through the Radarr and Sonarr
// API it already uses (GET /api/v3/boxoffice/notifications) and passes
// each one to the people who asked for that title and to its admins, in
// the Matinee apps.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/store"
)

// Recorder keeps notifications for Matinee to read.
type Recorder struct {
	Store *store.Store
	Log   *slog.Logger
}

// Add records a notification. Admin alerts and anything else that isn't
// about a download come in here too.
func (r *Recorder) Add(ctx context.Context, n store.Notification) error {
	if n.Level == "" {
		n.Level = store.LevelInfo
	}
	if n.Audience == "" {
		n.Audience = store.AudienceAdmins
	}
	_, err := r.Store.AddNotification(ctx, n)
	if err != nil && r.Log != nil {
		r.Log.Warn("couldn't save a notification", "event", n.Event, "err", err)
	}
	return err
}

// Alert passes an admin alert (internal/alerts) on to Matinee's admins.
// One that cleared up comes as information.
func (r *Recorder) Alert(ctx context.Context, a alerts.Alert) {
	n := store.Notification{Event: "alert." + a.Rule, Level: a.Level, Audience: store.AudienceAdmins, Title: a.Title, Body: a.Message}
	if a.Resolved {
		n.Level = store.LevelInfo
	}
	if a.Time > 0 {
		n.At = time.UnixMilli(a.Time)
	}
	r.Add(ctx, n)
}

// Download records what happened to a download. why is what went wrong,
// for failed and quarantined downloads.
func (r *Recorder) Download(ctx context.Context, d store.Download, event, why string) {
	m := r.media(ctx, d)
	name := d.Name
	if m != nil {
		name = mediaName(*m)
	} else if n := r.bookName(ctx, d); n != "" {
		name = n
	}
	n := store.Notification{Event: event, Level: store.LevelInfo, Audience: store.AudienceEveryone, Media: m, Download: d.ID}
	if m == nil {
		// Added by hand: only admins know what it is.
		n.Audience = store.AudienceAdmins
	}
	switch event {
	case store.EventGrabbed:
		n.Title = "Downloading " + name
		n.Body = "Box Office found a copy and started downloading it."
	case store.EventImported:
		n.Title = name + " is ready"
		n.Body = "It's downloaded and in the library."
	case store.EventDownloaded:
		n.Audience = store.AudienceAdmins
		n.Title = name + " finished downloading"
		n.Body = "It's in the downloads folder, as moving downloads into the library is off."
		if d.Kind == "" {
			n.Body = "It's in the downloads folder."
		}
	case store.EventFailed:
		n.Audience, n.Level = store.AudienceAdmins, store.LevelWarning
		n.Title = name + " didn't download"
		n.Body = sentence(why)
		if d.Source == "search" && d.Kind != "" {
			n.Body = join(n.Body, "Box Office blocked that release and is looking for another.")
		}
	case store.EventQuarantined:
		n.Audience, n.Level = store.AudienceAdmins, store.LevelCritical
		n.Title = name + " was quarantined"
		n.Body = join(sentence(why), "Nothing was opened or imported. Look at it on the Downloads page.")
	default:
		return
	}
	r.Add(ctx, n)
}

// bookName names the book a download is for: "Dune by Frank Herbert
// (audiobook)". Books go to admins only, as Matinee has nobody's requests
// for them.
func (r *Recorder) bookName(ctx context.Context, d store.Download) string {
	if !store.IsBookKind(d.Kind) {
		return ""
	}
	b, err := r.Store.Book(ctx, d.TitleID)
	if err != nil {
		return ""
	}
	a, err := r.Store.Author(ctx, b.AuthorID)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s by %s (%s)", b.Title, a.Name, d.Kind)
}

// media is the title a download is for, if it's for one.
func (r *Recorder) media(ctx context.Context, d store.Download) *store.Media {
	switch d.Kind {
	case "movie":
		mv, err := r.Store.Movie(ctx, d.TitleID)
		if err != nil {
			return nil
		}
		return &store.Media{Kind: "movie", Title: mv.Title, Year: mv.Year, TMDBID: mv.TMDBID, IMDBID: mv.IMDBID, Poster: mv.Poster}
	case "series":
		t, err := r.Store.Series(ctx, d.TitleID)
		if err != nil {
			return nil
		}
		m := &store.Media{Kind: "series", Title: t.Title, Year: t.Year, TMDBID: t.TMDBID, TVDBID: t.TVDBID, IMDBID: t.IMDBID, Poster: t.Poster}
		for _, id := range d.EpisodeIDs {
			e, err := r.Store.Episode(ctx, id)
			if err != nil {
				continue
			}
			if m.Season == 0 {
				m.Season = e.Season
			}
			if e.Season == m.Season {
				m.Episodes = append(m.Episodes, e.Number)
			}
		}
		slices.Sort(m.Episodes)
		return m
	}
	return nil
}

// mediaName is how a notification names its title: "Dune (2021)",
// "Severance S02E03", "Severance S02E01–E04", or "Severance season 2".
func mediaName(m store.Media) string {
	if m.Kind == "movie" {
		if m.Year > 0 {
			return fmt.Sprintf("%s (%d)", m.Title, m.Year)
		}
		return m.Title
	}
	switch n := len(m.Episodes); {
	case n == 0 && m.Season > 0:
		return fmt.Sprintf("%s season %d", m.Title, m.Season)
	case n == 0:
		return m.Title
	case n == 1:
		return fmt.Sprintf("%s S%02dE%02d", m.Title, m.Season, m.Episodes[0])
	default:
		return fmt.Sprintf("%s S%02dE%02d–E%02d", m.Title, m.Season, m.Episodes[0], m.Episodes[n-1])
	}
}

func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}
