package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Pacing: each indexer is asked at most every Client.Spacing, rests after
// errors (longer each time, as Sonarr does), rests an hour (or as long as
// it says) when it's out of its daily limit or asks Box Office to slow
// down, and has its API calls and NZB downloads counted against the daily
// limits its account has.

// rests is how long an indexer rests after errors in a row: not at all
// after one, then a minute, 5, 15 and 30 minutes, then 1, 3, 6, 12 and 24
// hours.
var rests = []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

func restAfter(failures int) time.Duration { return rests[min(max(failures-1, 0), len(rests)-1)] }

// limitedRest is how long an indexer out of its limit rests when it
// doesn't say.
const limitedRest = time.Hour

// grabReserve is the share of an indexer's daily downloads that automatic
// grabs leave for grabs by hand.
const grabReserve = 10 // percent

type autoKey struct{}

// automatic marks a context as Box Office searching by itself, so the
// search counts toward the automatic share of the indexers' limits.
func automatic(ctx context.Context) context.Context { return context.WithValue(ctx, autoKey{}, true) }

func isAuto(ctx context.Context) bool { v, _ := ctx.Value(autoKey{}).(bool); return v }

type handKey struct{}

// byHand marks a context as a person in Box Office picking a release, or
// asking for one movie or episode (or one show's wanted episodes), so an
// upgrade it grabs is theirs: it neither needs automatic upgrades on nor
// counts toward the month's limit. Everything else (the patrol, RSS sync,
// searches when a title is added, Matinee's search commands) follows the
// automatic upgrade rules.
func byHand(ctx context.Context) context.Context { return context.WithValue(ctx, handKey{}, true) }

func isByHand(ctx context.Context) bool { v, _ := ctx.Value(handKey{}).(bool); return v }

// noted records how a request to an indexer went.
func (s *Service) noted(ctx context.Context, ix store.Indexer, lim Limits, err error) {
	bg := context.WithoutCancel(ctx)
	if lim != (Limits{}) {
		if e := s.Store.NoteIndexerLimits(bg, ix.ID, lim.APIMax, lim.APIUsed, lim.GrabMax, lim.GrabUsed); e != nil {
			s.logger().Warn("couldn't note an indexer's limits", "indexer", ix.Name, "err", e)
		}
	}
	var ie *IndexerError
	switch {
	case err == nil:
		if e := s.Store.IndexerWorked(bg, ix.ID); e != nil {
			s.logger().Warn("couldn't note an indexer worked", "indexer", ix.Name, "err", e)
		}
	case errors.As(err, &ie):
		st, _ := s.Store.IndexerStatus(bg, ix.ID)
		rest := restAfter(st.Failures + 1)
		if ie.Limited {
			rest = limitedRest
			if ie.RetryAfter > 0 {
				rest = max(ie.RetryAfter, time.Minute)
			}
		} else if ie.RetryAfter > rest {
			rest = ie.RetryAfter
		}
		var until time.Time
		if rest > 0 {
			until = time.Now().Add(rest)
		}
		n, e := s.Store.IndexerFailed(bg, ix.ID, err.Error(), until)
		if e != nil {
			s.logger().Warn("couldn't note an indexer failed", "indexer", ix.Name, "err", e)
		}
		if rest > 0 {
			s.logger().Warn("indexer is resting after an error", "indexer", ix.Name, "errors in a row", n, "for", rest.String(), "err", err)
		}
	}
}

// query asks an indexer, counting the call and noting how it went.
func (s *Service) query(ctx context.Context, ix store.Indexer, q Query) ([]Release, error) {
	auto := 0
	if isAuto(ctx) {
		auto = 1
	}
	if err := s.Store.CountIndexerUse(context.WithoutCancel(ctx), ix.ID, 1, 0, auto); err != nil {
		s.logger().Warn("couldn't count an indexer search", "indexer", ix.Name, "err", err)
	}
	rs, lim, err := s.Client.Query(ctx, ix, q)
	if ctx.Err() == nil {
		s.noted(ctx, ix, lim, err)
	}
	return rs, err
}

// IndexerState is an indexer's status and today's use of its limits, for
// Settings and Health.
type IndexerState struct {
	store.IndexerStatus
	Name         string             `json:"name"`
	Resting      bool               `json:"resting"`
	Today        store.IndexerUsage `json:"today"`
	QueryLimit   int                `json:"queryLimit,omitempty"` // a day; 0 if unknown
	GrabLimit    int                `json:"grabLimit,omitempty"`
	LimitsReport bool               `json:"limitsReported"` // the indexer reports its limits itself
}

func limitOf(set, reported int) int {
	if set > 0 {
		return set
	}
	return reported
}

// IndexerStates is every indexer's state, by indexer.
func (s *Service) IndexerStates(ctx context.Context) (map[int64]IndexerState, error) {
	list, err := s.Store.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	sts, err := s.Store.IndexerStatuses(ctx)
	if err != nil {
		return nil, err
	}
	use, err := s.Store.IndexerUsageOn(ctx, store.UsageDay(time.Now()))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := map[int64]IndexerState{}
	for _, ix := range list {
		st, ok := sts[ix.ID]
		if !ok {
			st = store.IndexerStatus{IndexerID: ix.ID}
		}
		out[ix.ID] = IndexerState{IndexerStatus: st, Name: ix.Name, Resting: st.Resting(now), Today: use[ix.ID],
			QueryLimit: limitOf(ix.DailyQueries, st.APIMax), GrabLimit: limitOf(ix.DailyGrabs, st.GrabMax), LimitsReport: st.APIMax > 0 || st.GrabMax > 0}
	}
	return out, nil
}

// ResetIndexer ends an indexer's rest, to try it again now.
func (s *Service) ResetIndexer(ctx context.Context, id int64) error {
	if err := s.Store.RestIndexer(ctx, id, time.Time{}); err != nil {
		return err
	}
	return s.Store.IndexerWorked(ctx, id)
}

// restingNote says an indexer is resting, and until when.
func restingNote(ix store.Indexer, st store.IndexerStatus) string {
	why := ""
	if st.LastError != "" {
		why = " after: " + st.LastError
	}
	return fmt.Sprintf("%s is resting until %s%s", ix.Name, st.RestingUntil.Local().Format("15:04"), why)
}

// autoSearchRoom is how many more searches Box Office may make by itself
// now: half of what's left today on the indexer with the least left, of
// those whose limit is known; -1 when none is.
func (s *Service) autoSearchRoom(ctx context.Context, list []store.Indexer) (int, string, error) {
	states, err := s.IndexerStates(ctx)
	if err != nil {
		return 0, "", err
	}
	room, tight := -1, ""
	for _, ix := range list {
		st := states[ix.ID]
		if st.QueryLimit <= 0 {
			continue
		}
		left := max(st.QueryLimit-st.Today.Queries, 0) / 2
		if room < 0 || left < room {
			room, tight = left, fmt.Sprintf("%s has %d of its %d searches left today, and automatic searches leave half of what's left for you", ix.Name, max(st.QueryLimit-st.Today.Queries, 0), st.QueryLimit)
		}
	}
	return room, tight, nil
}

// grabAllowed says whether an NZB may be fetched from an indexer now: not
// past its daily download limit, and, for an automatic grab, not into the
// share left for grabs by hand.
func (s *Service) grabAllowed(ctx context.Context, ix store.Indexer, auto bool) error {
	st, err := s.Store.IndexerStatus(ctx, ix.ID)
	if err != nil {
		return err
	}
	if st.Resting(time.Now()) {
		return errors.New(restingNote(ix, st))
	}
	limit := limitOf(ix.DailyGrabs, st.GrabMax)
	if limit <= 0 {
		return nil
	}
	use, err := s.Store.IndexerUsageOn(ctx, store.UsageDay(time.Now()))
	if err != nil {
		return err
	}
	used := use[ix.ID].Grabs
	if used >= limit {
		return fmt.Errorf("%s's %d downloads for today are used up; it resets at midnight UTC", ix.Name, limit)
	}
	if auto && used >= limit-max(limit*grabReserve/100, 1) {
		return fmt.Errorf("%s has %d of its %d downloads left today, kept for grabs by hand", ix.Name, limit-used, limit)
	}
	return nil
}

// PruneEvery forgets the indexers' usage from over a month ago, daily.
func (s *Service) PruneEvery(ctx context.Context) {
	for {
		if err := s.Store.PruneIndexerUsage(ctx, store.UsageDay(time.Now().AddDate(0, -1, 0))); err != nil && ctx.Err() == nil {
			s.logger().Warn("couldn't prune indexer usage", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
