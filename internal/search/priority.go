package search

import (
	"context"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// reasonFor says why a release is grabbed, for the download order: a fix
// or replacement of a copy there, an upgrade, a movie's second copy, or a
// first copy of something new or out a while.
func (s *Service) reasonFor(ctx context.Context, c Candidate, upgrade bool, regrab string) string {
	switch {
	case regrab == RegrabFix || c.fix:
		return store.ReasonFix
	case regrab == RegrabReplace:
		return store.ReasonReplace
	case upgrade, regrab == RegrabUpgrade:
		return store.ReasonUpgrade
	}
	p, err := s.Store.QueuePriority(ctx)
	if err != nil {
		p.NewDays = store.DefaultNewDays
	}
	return firstCopyReason(ctx, s.Store, store.Download{Kind: c.kind, TitleID: c.titleID, EpisodeIDs: c.episodes, Mode: c.mode}, p.NewDays, time.Now())
}

// firstCopyReason says why a download for a movie or episodes was grabbed
// when that wasn't noted: an upgrade if there are copies already, a second
// copy, or new or backlog by when it came out.
func firstCopyReason(ctx context.Context, st *store.Store, d store.Download, days int, now time.Time) string {
	switch {
	case d.Kind == "movie" && d.TitleID != 0:
		m, err := st.Movie(ctx, d.TitleID)
		if err != nil {
			return store.ReasonBacklog
		}
		if d.Mode == ModeSecond {
			if m.Second != nil {
				return store.ReasonUpgrade
			}
			return store.ReasonSecond
		}
		if m.File != nil {
			return store.ReasonUpgrade
		}
		// Out to buy or stream (or, with no date for that, 90 days after
		// cinemas) in the last days days, as the library patrol sees it.
		if releasedSince(m, now.AddDate(0, 0, -days)) {
			return store.ReasonNew
		}
		return store.ReasonBacklog
	case d.Kind == "series" && len(d.EpisodeIDs) > 0:
		all, isNew := true, false
		for _, id := range d.EpisodeIDs {
			e, err := st.Episode(ctx, id)
			if err != nil {
				continue
			}
			all = all && e.File != nil
			isNew = isNew || e.AirDate != nil && store.IsNew(*e.AirDate, days, now)
		}
		switch {
		case all:
			return store.ReasonUpgrade
		case isNew:
			return store.ReasonNew
		}
	}
	return store.ReasonBacklog
}

// SortOutQueue notes why each waiting download for a movie or episodes was
// grabbed, for those grabbed before that was kept, so the download order
// can place them. Upgrades can be told by the copies there; fixes and
// replacements can't, and count as upgrades.
func (s *Service) SortOutQueue(ctx context.Context) {
	queue, err := s.Store.DownloadQueue(ctx)
	if err != nil {
		return
	}
	p, err := s.Store.QueuePriority(ctx)
	if err != nil {
		return
	}
	now, n := time.Now(), 0
	for _, d := range queue {
		if d.Reason != "" || d.Source != "search" || d.Kind != "movie" && d.Kind != "series" {
			continue
		}
		if err := s.Store.SetDownloadReason(ctx, d.ID, firstCopyReason(ctx, s.Store, d, p.NewDays, now)); err != nil {
			return
		}
		n++
	}
	if n > 0 {
		s.logger().Info("noted why waiting downloads were grabbed, for the download order", "downloads", n)
		if s.Engine != nil {
			s.Engine.Wake()
		}
	}
}
