package catalog

import (
	"context"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Space saver, a choice per show: only the next few episodes past the
// latest one anyone has watched are fetched, and more as they watch, so a
// long show doesn't fill the disk before anyone gets to it. Matinee says
// how far a show has been watched (its watch report); until it does, the
// show counts as not started.
//
// It works by holding episodes back (episodes.held): a held episode that
// isn't on disk isn't wanted, whatever its own and its season's switches
// say, and those switches are left as the person set them. One that is on
// disk already stays, and is upgraded like any other. Only episodes that would be
// wanted are counted, so seasons switched off stay out of it. Episodes up
// to the one being watched are never held, and nothing is deleted here.
// Removing the episodes everyone has finished is a separate choice, off
// unless turned on (see api/watchedeps.go).

// MaxKeepAhead is the most episodes space saver can keep ahead.
const MaxKeepAhead = 100

// KeepAheadChoices are what the UI offers.
var KeepAheadChoices = []int{3, 5, 10, 20}

// CheckKeepAhead says whether n is a number of episodes space saver takes.
func CheckKeepAhead(n int) error {
	if n < 0 || n > MaxKeepAhead {
		return badf("space saver keeps between 1 and %d episodes ahead, or 0 for off", MaxKeepAhead)
	}
	return nil
}

// HoldAhead brings which of a show's episodes are held back in line with
// its space saver, its switches and what has been watched. released is
// how many episodes it let go of that are out and not on disk: there's
// something new to look for.
func (s *Service) HoldAhead(ctx context.Context, id int64) (released int, err error) {
	t, err := s.Store.Series(ctx, id)
	if err != nil {
		return 0, err
	}
	var w store.Watched
	if t.KeepAhead > 0 {
		all, err := s.Store.WatchedTitles(ctx, "series")
		if err != nil {
			return 0, err
		}
		w = all[t.ID]
	}
	return s.holdAhead(ctx, t, w)
}

func (s *Service) holdAhead(ctx context.Context, t store.Series, w store.Watched) (released int, err error) {
	eps, err := s.Store.Episodes(ctx, t.ID) // in order
	if err != nil {
		return 0, err
	}
	seasons := map[int]bool{}
	for _, se := range t.Seasons {
		seasons[se.Number] = se.Monitored
	}
	now := time.Now()
	left := t.KeepAhead
	for _, e := range eps {
		hold := false
		past := e.Season > w.FurthestSeason || e.Season == w.FurthestSeason && e.Number > w.FurthestEpisode
		if t.KeepAhead > 0 && e.Season > 0 && past && e.Monitored && seasons[e.Season] {
			if left > 0 {
				left--
			} else {
				hold = true
			}
		}
		if hold == e.Held {
			continue
		}
		if err := s.Store.SetEpisodeHeld(ctx, e.ID, hold); err != nil {
			return released, err
		}
		if !hold && e.File == nil && e.Monitored && seasons[e.Season] && e.AirDate != nil && e.AirDate.Before(now) {
			released++
		}
	}
	return released, nil
}

// HoldAheadAndSearch is HoldAhead, then a search for the show when
// episodes were let go.
func (s *Service) HoldAheadAndSearch(ctx context.Context, id int64) error {
	released, err := s.HoldAhead(ctx, id)
	if err != nil || released == 0 {
		return err
	}
	if t, err := s.Store.Series(ctx, id); err != nil || !t.Monitored {
		return err
	}
	return s.SearchSeries(ctx, id)
}

// SpaceSaver runs every show's space saver: Matinee has said what has
// been watched. Shows with episodes let go are searched.
func (s *Service) SpaceSaver(ctx context.Context) error {
	shows, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return err
	}
	var all map[int64]store.Watched
	var search []int64
	for _, t := range shows {
		if t.KeepAhead <= 0 {
			continue
		}
		if all == nil {
			if all, err = s.Store.WatchedTitles(ctx, "series"); err != nil {
				return err
			}
		}
		released, err := s.holdAhead(ctx, t, all[t.ID])
		if err != nil {
			return err
		}
		if released > 0 && t.Monitored {
			search = append(search, t.ID)
		}
	}
	if len(search) == 0 {
		return nil
	}
	return s.SearchSeries(ctx, search...)
}
