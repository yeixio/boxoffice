package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// Minimum release age: a movie or episode release posted only minutes ago
// isn't grabbed by itself straight away. Broken uploads, fakes and ones
// taken down for copyright are mostly pulled within the first half hour,
// so Box Office holds a new release back until it's old enough, then
// judges it again as it does RSS sync's releases (it's still wanted, and
// still the best on offer) and grabs it. That costs no searches: the
// release is kept, not looked for again. If the indexer pulled it in the
// meantime, fetching its NZB fails, and the next search finds another.
//
// Only grabs Box Office makes by itself wait: RSS sync, the patrol, the
// search when a title is added and Matinee's requests. A grab by hand, or
// a copy someone asked for in Matinee (a replacement, say), never does.
// Books, music and games don't wait either; their indexers' lists move
// slowly enough that it doesn't help.

// SettingMinAge is how many minutes old a release must be; unset is
// DefaultMinAgeMinutes, and "0" grabs at once.
const SettingMinAge = "search_min_age"

const (
	DefaultMinAgeMinutes = 30
	MaxMinAgeMinutes     = 24 * 60
)

// heldStale is how long past its time a held release is kept while it
// can't be looked at (downloading off, or the data cap): after that it's
// old news, and the patrol finds what's still wanted.
const heldStale = 24 * time.Hour

type browseKey struct{}

// Browsing marks a context as a person looking at a title's releases in
// Box Office: a new release is shown as it is, with when Box Office would
// take it by itself, rather than turned down.
func Browsing(ctx context.Context) context.Context { return context.WithValue(ctx, browseKey{}, true) }

func isBrowsing(ctx context.Context) bool { v, _ := ctx.Value(browseKey{}).(bool); return v }

// minAge is how old a release must be before Box Office grabs it by
// itself.
func (s *Service) minAge(ctx context.Context) time.Duration {
	v, err := s.Store.Setting(ctx, SettingMinAge)
	if err != nil {
		return DefaultMinAgeMinutes * time.Minute
	}
	m, err := strconv.Atoi(v)
	if err != nil || m < 0 {
		m = DefaultMinAgeMinutes
	}
	return time.Duration(min(m, MaxMinAgeMinutes)) * time.Minute
}

// oldEnoughAt is when a release posted at pub may be grabbed by itself. A
// posting time ahead of the clock counts from now.
func oldEnoughAt(pub, now time.Time, age time.Duration) time.Time {
	if pub.After(now) {
		pub = now
	}
	return pub.Add(age)
}

// holdYoung goes over a search's movie and episode releases before
// they're ranked. One that would be taken but is too new is turned down
// and held, to be looked at again once it's old enough; when a person is
// looking at the list, it's only marked. Grabs by hand don't wait.
func (s *Service) holdYoung(ctx context.Context, out *Results) {
	if isByHand(ctx) {
		return
	}
	var age time.Duration
	loaded := false
	now := time.Now()
	for i := range out.Releases {
		c := &out.Releases[i]
		if !c.Accepted || c.PublishedAt.IsZero() || (c.kind != "movie" && c.kind != "series") {
			continue
		}
		if !loaded {
			age, loaded = s.minAge(ctx), true
			if age == 0 {
				return
			}
		}
		due := oldEnoughAt(c.PublishedAt, now, age)
		if !now.Before(due) {
			continue
		}
		if isBrowsing(ctx) {
			c.NewUntil = due
			continue
		}
		c.held = true
		c.reject(fmt.Sprintf("it was posted %s ago; Box Office waits until a release is %s old, so a broken or fake one can be taken down first, and will look at it again then",
			minutes(now.Sub(c.PublishedAt)), minutes(age)))
		if err := s.hold(ctx, *c, due); err != nil {
			s.logger().Warn("couldn't hold a new release back", "release", c.Title, "err", err)
		} else {
			s.logger().Info("holding a new release back until it's old enough", "release", c.Title, "indexer", c.Indexer, "until", due.Format(time.RFC3339))
		}
	}
}

// minutes says a duration in whole minutes, or hours for whole hours.
func minutes(d time.Duration) string {
	m := max(int(d.Minutes()), 0)
	switch {
	case m >= 60 && m%60 == 0:
		return plural(m/60, "1 hour", fmt.Sprintf("%d hours", m/60))
	case m == 1:
		return "1 minute"
	case m == 0:
		return "less than a minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

// heldRelease is how a held release is kept: all of it, the NZB link and
// the ids too, and what it was found for.
type heldRelease struct {
	Release
	Link     string `json:"link"`
	Password string `json:"password,omitempty"`
	IMDBID   string `json:"imdbId,omitempty"`
	TMDBID   int64  `json:"tmdbId,omitempty"`
	TVDBID   int64  `json:"tvdbId,omitempty"`
	Kind     string `json:"kind"`
	TitleID  int64  `json:"titleId"`
}

func (s *Service) hold(ctx context.Context, c Candidate, due time.Time) error {
	h := heldRelease{Release: c.Release, Link: c.Link, Password: c.Password, IMDBID: c.IMDBID, TMDBID: c.TMDBID, TVDBID: c.TVDBID,
		Kind: c.kind, TitleID: c.titleID}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return s.Store.HoldRelease(context.WithoutCancel(ctx), c.Key, c.Title, string(b), due)
}

// unheld is a held release as it was found, and what it was found for.
// It's given that title's ids, so RSS sync's matching finds the same
// title even when the indexer didn't say.
func (s *Service) unheld(ctx context.Context, h store.HeldRelease) (r Release, kind string, titleID int64, ok bool) {
	var v heldRelease
	if err := json.Unmarshal([]byte(h.Data), &v); err != nil || v.Title == "" {
		return Release{}, "", 0, false
	}
	r = v.Release
	r.Link, r.Password, r.IMDBID, r.TMDBID, r.TVDBID = v.Link, v.Password, v.IMDBID, v.TMDBID, v.TVDBID
	switch v.Kind {
	case "movie":
		if m, err := s.Store.Movie(ctx, v.TitleID); err == nil {
			if r.IMDBID == "" {
				r.IMDBID = m.IMDBID
			}
			if r.TMDBID == 0 {
				r.TMDBID = m.TMDBID
			}
		}
	case "series":
		if t, err := s.Store.Series(ctx, v.TitleID); err == nil && r.TVDBID == 0 {
			r.TVDBID = t.TVDBID
		}
	}
	return r, v.Kind, v.TitleID, true
}

// HeldEvery looks at held releases every minute, grabbing those old
// enough.
func (s *Service) HeldEvery(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		s.TakeHeld(ctx)
	}
}

// TakeHeld grabs the held releases that are old enough, if they're still
// wanted and still the best of what's held. It waits for RSS sync if one
// is running, so the two don't grab for the same title at once.
func (s *Service) TakeHeld(ctx context.Context) (got, problems []string) {
	held, err := s.Store.HeldReleases(ctx)
	if err != nil || len(held) == 0 {
		return nil, nil
	}
	now := time.Now()
	var due []store.HeldRelease
	for _, h := range held {
		switch {
		case now.Sub(h.DueAt) > heldStale:
			if err := s.Store.DropHeld(ctx, h.Key); err != nil {
				s.logger().Warn("couldn't forget a held release", "release", h.Title, "err", err)
			}
		case !now.Before(h.DueAt):
			due = append(due, h)
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	// It takes RSS sync's turn.
	s.rss.mu.Lock()
	if s.rss.busy {
		s.rss.mu.Unlock()
		return nil, nil
	}
	s.rss.busy = true
	s.rss.mu.Unlock()
	defer func() {
		s.rss.mu.Lock()
		s.rss.busy = false
		s.rss.mu.Unlock()
	}()
	ctx = automatic(ctx)
	d, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil || !d.Enabled {
		return nil, nil // they wait, until they're stale
	}
	c, err := downloader.LoadCap(ctx, s.Store)
	if err != nil || c.Level == downloader.CapMissing || c.Level == downloader.CapFull {
		return nil, nil
	}
	var releases []Release
	var movies []int64
	for _, h := range due {
		if r, kind, id, ok := s.unheld(ctx, h); ok {
			releases = append(releases, r)
			if kind == "movie" {
				movies = append(movies, id)
			}
		}
		if err := s.Store.DropHeld(ctx, h.Key); err != nil {
			s.logger().Warn("couldn't forget a held release", "release", h.Title, "err", err)
		}
	}
	got, problems, err = s.takeNew(ctx, c, releases, movies...)
	if err != nil {
		problems = append(problems, err.Error())
	}
	s.logger().Info("held releases looked at again", "releases", len(releases), "grabbed", strings.Join(got, ", "), "problems", strings.Join(problems, "; "))
	return got, problems
}
