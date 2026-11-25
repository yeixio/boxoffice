package search

import (
	"context"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// A show can keep a second copy of each episode at another quality (see
// store.Series.SecondProfileID). Searching for those is the same work as
// for the first copies, seen the other way round: the second copy's
// profile is the profile, and an episode's second copy is "the copy
// there". A search is told to look that way with asSecond; what it finds
// is marked ModeSecond, so it's moved in to the second copy's place.

type secondKey struct{}

// asSecond makes searches under ctx look for a show's second copies.
func asSecond(ctx context.Context) context.Context {
	return context.WithValue(ctx, secondKey{}, true)
}

func isSecond(ctx context.Context) bool {
	v, _ := ctx.Value(secondKey{}).(bool)
	return v
}

// copyOf is an episode as a search under ctx sees it: looking for second
// copies, its second copy is its File and its first copy its Second.
func copyOf(ctx context.Context, e store.Episode) store.Episode {
	if isSecond(ctx) {
		e.File, e.Second = e.Second, e.File
	}
	return e
}

// episode is an episode as a search under ctx sees it (see copyOf).
func (s *Service) episode(ctx context.Context, id int64) (store.Episode, error) {
	e, err := s.Store.Episode(ctx, id)
	return copyOf(ctx, e), err
}

// episodes are a show's episodes as a search under ctx sees them.
func (s *Service) episodes(ctx context.Context, seriesID int64) ([]store.Episode, error) {
	eps, err := s.Store.Episodes(ctx, seriesID)
	for i := range eps {
		eps[i] = copyOf(ctx, eps[i])
	}
	return eps, err
}

// showProfile is the profile a search under ctx judges a show's releases
// by: the show's, or its second copies'.
func (s *Service) showProfile(ctx context.Context, t store.Series) (quality.Profile, error) {
	if isSecond(ctx) {
		return s.profile(ctx, t.SecondProfileID)
	}
	return s.profile(ctx, t.ProfileID)
}

// keepsSecond says whether a show keeps second copies that can be
// searched for: it has a profile for them that takes downloads.
func (s *Service) keepsSecond(ctx context.Context, t store.Series) bool {
	if t.SecondProfileID == 0 {
		return false
	}
	p, err := s.profile(ctx, t.SecondProfileID)
	return err == nil && !p.Locked
}
