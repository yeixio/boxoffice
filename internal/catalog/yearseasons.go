package catalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Some long-running shows, such as the BBC's Horizon, are numbered by year
// on TheTVDB (Season 1964 … Season 2026), and so in Sonarr, on the indexers
// and in the files' names. TMDB numbers the same seasons 1, 2, 3 and only
// names them by year. A show Box Office already holds year seasons for
// keeps them: TMDB's episodes are filed under the year they aired instead of
// being added a second time under TMDB's numbers.

// firstYearSeason is the lowest season number taken for a year.
const firstYearSeason = 1900

// yearNumbered reports whether a show's seasons are numbered by year.
func yearNumbered(seasons []store.Season) bool {
	return slices.ContainsFunc(seasons, func(se store.Season) bool { return se.Number >= firstYearSeason })
}

// syncYearEpisodes is syncEpisodes for a show numbered by year. An episode
// Box Office has already (matched by the day it aired, or else by its
// title) gets TMDB's details. One it hasn't is added only if it aired
// after everything Box Office has for that year, so TheTVDB's list (which
// the files and indexers follow) isn't padded with TMDB's extras.
func (s *Service) syncYearEpisodes(ctx context.Context, t store.Series, info tmdb.Show, monitor string) error {
	c := s.TMDB(ctx)
	now := time.Now()
	have, err := s.Store.Episodes(ctx, t.ID)
	if err != nil {
		return err
	}
	type year struct {
		byDate, byTitle map[string]int
		taken           map[int]bool
		last            int
		latest          time.Time
	}
	years := map[int]*year{}
	yearOf := func(n int) *year {
		y := years[n]
		if y == nil {
			y = &year{byDate: map[string]int{}, byTitle: map[string]int{}, taken: map[int]bool{}}
			years[n] = y
		}
		return y
	}
	for _, e := range have {
		if e.Season < firstYearSeason {
			continue
		}
		y := yearOf(e.Season)
		if e.AirDate != nil {
			if _, dup := y.byDate[day(*e.AirDate)]; !dup {
				y.byDate[day(*e.AirDate)] = e.Number
			}
			if e.AirDate.After(y.latest) {
				y.latest = *e.AirDate
			}
		}
		if k := titleKey(e.Title); k != "" {
			y.byTitle[k] = e.Number
		}
		y.last = max(y.last, e.Number)
	}
	known := map[int]bool{}
	for _, se := range t.Seasons {
		known[se.Number] = true
	}

	var eps []tmdb.Episode
	for _, se := range info.Seasons {
		got, err := c.Season(ctx, info.TMDBID, se.Number)
		if errors.Is(err, tmdb.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if se.Number == 0 { // specials are numbered the same way on both
			if err := s.Store.EnsureSeason(ctx, t.ID, store.Season{Number: 0}); err != nil {
				return err
			}
			for _, e := range got {
				mon := monitor != "none" && !(monitor == "future" && e.AirDate != nil && e.AirDate.Before(now))
				if err := s.Store.UpsertEpisode(ctx, store.Episode{SeriesID: t.ID, Season: 0, Number: e.Number, Title: e.Title,
					Overview: e.Overview, AirDate: e.AirDate, Monitored: mon}); err != nil {
					return err
				}
			}
			continue
		}
		for _, e := range got {
			if e.AirDate != nil && e.AirDate.Year() >= firstYearSeason {
				eps = append(eps, e) // without a day it aired, there's no year to file it under
			}
		}
	}
	// In the order they aired, so a year's new episodes are numbered in turn.
	slices.SortStableFunc(eps, func(a, b tmdb.Episode) int { return a.AirDate.Compare(*b.AirDate) })
	for _, e := range eps {
		n := e.AirDate.Year()
		y := yearOf(n)
		number, ok := y.byDate[day(*e.AirDate)]
		if ok && y.taken[number] {
			ok = false
		}
		if !ok {
			number, ok = y.byTitle[titleKey(e.Title)]
			ok = ok && !y.taken[number]
		}
		if !ok {
			if !y.latest.IsZero() && !e.AirDate.After(y.latest) {
				continue // TheTVDB doesn't list it for that year
			}
			y.last++
			number = y.last
		}
		y.taken[number] = true
		if !known[n] {
			if err := s.Store.EnsureSeason(ctx, t.ID, store.Season{Number: n, Monitored: t.Monitored && monitor != "none"}); err != nil {
				return err
			}
			known[n] = true
		}
		mon := monitor != "none" && !(monitor == "future" && e.AirDate.Before(now))
		if err := s.Store.UpsertEpisode(ctx, store.Episode{SeriesID: t.ID, Season: n, Number: number, Title: e.Title,
			Overview: e.Overview, AirDate: e.AirDate, Monitored: mon}); err != nil {
			return err
		}
	}
	// Seasons an earlier refresh added under TMDB's numbers.
	dropped, err := s.Store.DropNumberedSeasons(ctx, t.ID)
	if err != nil {
		return err
	}
	if dropped > 0 {
		s.Log.Info("show is numbered by year: cleared the seasons TMDB numbers instead", "title", t.Title, "seasons", dropped)
	}
	return nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// titleKey is an episode title for matching: lower case, letters and digits.
func titleKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
