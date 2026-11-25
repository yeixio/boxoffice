package search

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Settings keys for the search order and early grabs.
const (
	// SettingSearchOrder is OrderTurns to have the patrol's missing titles
	// and upgrades take turns; the download order otherwise.
	SettingSearchOrder = "search_order"
	// SettingEarly is EarlyAny or EarlyOff; EarlyCutoff otherwise.
	SettingEarly = "search_early"
)

// The search orders.
const (
	OrderPriority = "priority" // the download order: missing titles first
	OrderTurns    = "turns"    // missing titles and upgrades in turn
)

// What RSS sync grabs of a movie or an episode before it's out.
const (
	EarlyCutoff = "cutoff" // a release whose quality meets the profile's cutoff
	EarlyAny    = "any"    // any release the profile takes
	EarlyOff    = "off"    // nothing: it waits for its date
)

// How far ahead of their dates movies and episodes are early wants: a
// movie whose digital or disc date is in the next month (or that's in
// cinemas now), and an episode that airs in the next 3 days. Movies with
// no dates, and episodes with no air date, aren't.
const (
	earlyMovieDays   = 30
	earlyEpisodeDays = 3
)

// group is the download order's group a wanted title would download in,
// so the patrol can search in that order.
func (w Wanted) group(newDays int, now time.Time) string {
	switch {
	case w.Upgrade:
		return store.GroupUpgrades
	case w.Copy == ModeSecond:
		return store.GroupSecondCopies
	}
	isNew := store.IsNew(w.out, newDays, now)
	switch w.Kind {
	case "movie":
		if isNew {
			return store.GroupNewMovies
		}
		return store.GroupBacklogMovies
	case "episode":
		if isNew {
			return store.GroupNewEpisodes
		}
		return store.GroupBacklogEpisodes
	case store.Ebook, store.Audiobook:
		return store.GroupBooks
	case store.AlbumKind:
		return store.GroupMusic
	case store.GameKind:
		return store.GroupGames
	}
	return store.GroupOther
}

// byPriority puts wanted titles in the download order's order of their
// groups, keeping the order they had within each.
func byPriority(list []Wanted, p store.QueuePriority, now time.Time) []Wanted {
	rank := map[string]int{}
	for i, g := range store.CleanOrder(p.Order) {
		rank[g] = i
	}
	days := p.NewDays
	if days <= 0 {
		days = store.DefaultNewDays
	}
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b Wanted) int {
		return cmp.Compare(rank[a.group(days, now)], rank[b.group(days, now)])
	})
	return out
}

// earlyMovie says whether a missing movie that isn't out by its dates is
// near enough to be grabbed early: in cinemas now, or out to buy or stream
// within earlyMovieDays.
func earlyMovie(m store.Movie, now time.Time) bool {
	if released(m, now) {
		return false
	}
	if m.InCinemas != nil && m.InCinemas.Before(now) {
		return true
	}
	soon := now.AddDate(0, 0, earlyMovieDays)
	for _, d := range []*time.Time{m.DigitalRelease, m.PhysicalRelease} {
		if d != nil && d.Before(soon) {
			return true
		}
	}
	return false
}

// earlyEpisode says whether an episode airs soon enough to be grabbed
// early: after now, and within earlyEpisodeDays.
func earlyEpisode(e store.Episode, now time.Time) bool {
	return e.AirDate != nil && !e.AirDate.Before(now) && e.AirDate.Before(now.AddDate(0, 0, earlyEpisodeDays))
}

// earlyWants are the movies and episodes RSS sync may grab before they're
// out: monitored, with no copy, near their dates (earlyMovie,
// earlyEpisode). They're never searched for, only matched to new
// releases.
func (s *Service) earlyWants(ctx context.Context, now time.Time) ([]Wanted, error) {
	ps, err := s.Store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	profiles := map[int]quality.Profile{}
	for _, p := range ps {
		profiles[p.ID] = p
	}
	var out []Wanted
	movies, err := s.Store.Movies(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range movies {
		p, ok := profiles[m.ProfileID]
		if !m.Monitored || !ok || p.Locked || m.File != nil || !earlyMovie(m, now) {
			continue
		}
		out = append(out, Wanted{Kind: "movie", ID: m.ID, Subject: movieSubject(m), Cutoff: p.Cutoff, New: true, Early: true, out: movieOut(m)})
	}
	shows, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range shows {
		p, ok := profiles[t.ProfileID]
		if !t.Monitored || !ok || p.Locked {
			continue
		}
		seasons := map[int]bool{}
		for _, se := range t.Seasons {
			seasons[se.Number] = se.Monitored
		}
		eps, err := s.Store.Episodes(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range eps {
			if !e.Monitored || e.Held || e.File != nil || !seasons[e.Season] || !earlyEpisode(e, now) {
				continue
			}
			out = append(out, Wanted{Kind: "episode", ID: e.ID, SeriesID: t.ID, Subject: fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number),
				Cutoff: p.Cutoff, Season: e.Season, New: true, Early: true, out: *e.AirDate})
		}
	}
	return out, nil
}

// earlyMiss is why a release of a title that isn't out yet isn't taken
// early, or "" when it is: a cinema recording or a screener only when the
// profile takes that quality, and with EarlyCutoff its quality must meet
// the profile's cutoff.
func earlyMiss(mode string, p quality.Profile, c Candidate) string {
	switch {
	case mode == EarlyOff:
		return "it isn't out yet, and early grabs are off"
	case isPreRelease(c.Quality) && !p.Accepts(c.Quality):
		return "it isn't out yet, and this looks like a cinema recording or a screener"
	case mode != EarlyAny && (c.Quality == 0 || quality.Rank(c.Quality) < quality.Rank(p.Cutoff)):
		return "it isn't out yet, and this is below the profile's cutoff"
	}
	return ""
}

// isPreRelease says whether quality q is a copy made before release.
func isPreRelease(q int) bool {
	pq, _ := quality.ByID(q)
	return pq.PreRelease
}
