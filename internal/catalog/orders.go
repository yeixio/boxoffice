package catalog

import (
	"context"
	"errors"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Releases of some shows follow another episode order than the one they
// aired in: the DVD sets', most often. TMDB lists such orders, and a show
// can be told its releases follow one (store.Series.Order). Each episode
// then has a place in that order (store.Episode.Place), which searches
// and imports read release names by. The show's own seasons and numbers,
// and so its files' names, stay as aired.

// Orders lists the other episode orders TMDB has for a show. A show
// numbered by year has none to choose: its seasons aren't TMDB's.
func (s *Service) Orders(ctx context.Context, id int64) ([]tmdb.Order, error) {
	t, err := s.Store.Series(ctx, id)
	if err != nil {
		return nil, err
	}
	if yearNumbered(t.Seasons) {
		return []tmdb.Order{}, nil
	}
	orders, err := s.TMDB(ctx).Orders(ctx, t.TMDBID)
	if errors.Is(err, tmdb.ErrNotFound) {
		return []tmdb.Order{}, nil
	}
	return orders, err
}

// PlaceEpisodes gives a show's episodes their places in the order its
// releases follow (t.Numbering names it), or clears them when they follow
// none. An order TMDB doesn't have for the show is an Error.
func (s *Service) PlaceEpisodes(ctx context.Context, t store.Series) error {
	if _, ok := t.Order(); ok && yearNumbered(t.Seasons) {
		return badf("a show whose seasons are numbered by year can't follow another episode order")
	}
	return s.placeEpisodes(ctx, t)
}

// placeEpisodes gives a show's episodes their places in the order its
// releases follow, or clears them when they follow none.
func (s *Service) placeEpisodes(ctx context.Context, t store.Series) error {
	order, ok := t.Order()
	if !ok {
		return s.Store.SetEpisodePlaces(ctx, t.ID, nil)
	}
	list, err := s.TMDB(ctx).OrderPlaces(ctx, order)
	if errors.Is(err, tmdb.ErrNotFound) {
		return badf("TMDB has no such episode order for the show")
	}
	if err != nil {
		return err
	}
	eps, err := s.Store.Episodes(ctx, t.ID)
	if err != nil {
		return err
	}
	ids := map[store.Place]int64{}
	for _, e := range eps {
		ids[store.Place{Season: e.Season, Number: e.Number}] = e.ID
	}
	places := map[int64]store.Place{}
	for _, p := range list {
		if id, ok := ids[store.Place{Season: p.AiredSeason, Number: p.AiredNumber}]; ok {
			if _, dup := places[id]; !dup {
				places[id] = store.Place{Season: p.Season, Number: p.Number}
			}
		}
	}
	if len(places) == 0 {
		return badf("that episode order lists none of the show's episodes")
	}
	return s.Store.SetEpisodePlaces(ctx, t.ID, places)
}
