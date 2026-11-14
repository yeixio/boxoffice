package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// Daily replacements: a grab that takes the place of a copy on disk is
// counted, and once the day's are used up the rest wait for the next day.
// Each one is a whole new download of something the library already has,
// so a burst of them (a run of false "damaged" reports from Matinee, a
// profile change that makes many copies upgradable, a group putting out a
// batch of REPACKs) could otherwise eat the month's data cap in a day.
//
// What counts:
//   - automatic upgrades (RSS sync, the patrol, searches when a title is
//     added, Matinee's search commands);
//   - fixed releases of a copy (PROPER, REPACK; see propers.go);
//   - replacements for copies reported in Matinee, by a person or by its
//     check for damaged videos.
//
// What doesn't: anything for a title with no copy yet, and grabs made by
// hand in Box Office (Choose a release, a title page's search). Those are
// never held back by it either.
//
// A replacement turned down for the limit is noted (regrab_waiting) and
// searched for again, oldest first, once a day has room: the next day, or
// as soon as the limit is raised.

// SettingRegrabsPerDay is the most replacements a day; unset is
// DefaultRegrabsPerDay, and "0" means no limit.
const SettingRegrabsPerDay = "search_regrabs_per_day"

const (
	DefaultRegrabsPerDay = 10
	MaxRegrabsPerDay     = 1000
)

// Why a replacement was grabbed (store.RegrabWait.Why).
const (
	RegrabUpgrade = "upgrade"
	RegrabFix     = "fix"
	RegrabReplace = "replace"
)

// ErrRegrabsUsedUp is what a replacement turned down for the day's limit
// wraps (see RegrabsUsedUp).
var ErrRegrabsUsedUp = errors.New("today's replacements are used up")

// RegrabsUsedUp says a replacement waits for tomorrow.
type RegrabsUsedUp struct{ Limit int }

func (e RegrabsUsedUp) Error() string {
	return fmt.Sprintf("today's %d %s used up, so it waits until tomorrow", e.Limit, plural(e.Limit, "replacement is", "replacements are"))
}

func (e RegrabsUsedUp) Is(target error) bool { return target == ErrRegrabsUsedUp }

type reportedKey struct{}

// reported marks a context as a search for a copy reported as bad in
// Matinee. It's asked for like a grab by hand, but it counts toward the
// day's replacements, as reports can come in bursts.
func reported(ctx context.Context) context.Context {
	return context.WithValue(ctx, reportedKey{}, true)
}

func isReported(ctx context.Context) bool { v, _ := ctx.Value(reportedKey{}).(bool); return v }

// regrabsPerDay is the most replacements a day, or 0 for no limit.
func regrabsPerDay(ctx context.Context, st *store.Store) (int, error) {
	v, err := st.Setting(ctx, SettingRegrabsPerDay)
	if err != nil {
		return DefaultRegrabsPerDay, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return DefaultRegrabsPerDay, nil
	}
	return min(n, MaxRegrabsPerDay), nil
}

// RegrabsToday is how many replacements Box Office grabbed by itself
// today, and how many more it may: left is -1 when there's no limit.
func (s *Service) RegrabsToday(ctx context.Context) (used, left int, err error) {
	limit, err := regrabsPerDay(ctx, s.Store)
	if err != nil {
		return 0, 0, err
	}
	if used, err = s.Store.RegrabsOn(ctx, store.DataDay(time.Now())); err != nil || limit == 0 {
		return used, -1, err
	}
	return used, max(limit-used, 0), nil
}

// regrabOf says whether a grab replaces a copy on disk in a way that
// counts toward the day's limit, and why ("" when it doesn't count).
// upgrade is what autoUpgradeAllowed made of it.
func (s *Service) regrabOf(ctx context.Context, c Candidate, upgrade bool) string {
	switch {
	case isReported(ctx):
		return RegrabReplace
	case isByHand(ctx):
		return ""
	case c.fix:
		return RegrabFix
	case !upgrade:
		return ""
	}
	// An automatic grab for a copy reported as bad (another search after
	// its first replacement failed, say) is still a replacement.
	kind, id := "movie", c.titleID
	if c.kind == "series" && len(c.episodes) > 0 {
		kind, id = "episode", c.episodes[0]
	}
	if tw, err := s.Store.TitleWant(ctx, kind, id); err == nil && tw.Want == store.WantReplace {
		return RegrabReplace
	}
	return RegrabUpgrade
}

// regrabRoom turns down a replacement once the day's are used up, and
// notes it to be looked for again when there's room.
func (s *Service) regrabRoom(ctx context.Context, c Candidate, why string) error {
	limit, err := regrabsPerDay(ctx, s.Store)
	if err != nil || limit == 0 {
		return err
	}
	used, err := s.Store.RegrabsOn(ctx, store.DataDay(time.Now()))
	if err != nil {
		return err
	}
	if used < limit {
		return nil
	}
	if err := s.waitRegrab(ctx, c, why); err != nil {
		s.logger().Warn("couldn't note a replacement to grab tomorrow", "release", c.Title, "err", err)
	} else {
		s.logger().Info("today's replacements are used up; this one waits until tomorrow", "release", c.Title, "why", why, "limit", limit)
	}
	return RegrabsUsedUp{Limit: limit}
}

// regrabWant is what a waiting replacement is for, to search for it again.
type regrabWant struct {
	Kind     string  `json:"kind"` // movie or series
	TitleID  int64   `json:"titleId"`
	Episodes []int64 `json:"episodes,omitempty"`
	Reported bool    `json:"reported,omitempty"`
}

func (w regrabWant) key() string {
	ids := slices.Clone(w.Episodes)
	slices.Sort(ids)
	parts := []string{w.Kind, strconv.FormatInt(w.TitleID, 10)}
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, "/")
}

// covers says whether w is for all of o's episodes already.
func (w regrabWant) covers(o regrabWant) bool {
	if w.Kind != o.Kind || w.TitleID != o.TitleID {
		return false
	}
	for _, id := range o.Episodes {
		if !slices.Contains(w.Episodes, id) {
			return false
		}
	}
	return true
}

func (s *Service) waitRegrab(ctx context.Context, c Candidate, why string) error {
	ctx = context.WithoutCancel(ctx)
	w := regrabWant{Kind: c.kind, TitleID: c.titleID, Episodes: c.episodes, Reported: isReported(ctx)}
	if w.Kind != "movie" && w.Kind != "series" {
		return nil
	}
	waiting, err := s.Store.RegrabsWaiting(ctx)
	if err != nil {
		return err
	}
	for _, o := range waiting {
		var ow regrabWant
		// A season pack waiting already brings these episodes.
		if json.Unmarshal([]byte(o.Data), &ow) == nil && o.Key != w.key() && ow.covers(w) {
			return nil
		}
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return s.Store.WaitRegrab(ctx, store.RegrabWait{Key: w.key(), Subject: s.regrabSubject(ctx, w), Why: why, Release: c.Title, Data: string(b)})
}

// regrabSubject names the movie or episodes, for Settings.
func (s *Service) regrabSubject(ctx context.Context, w regrabWant) string {
	if w.Kind == "movie" {
		if m, err := s.Store.Movie(ctx, w.TitleID); err == nil {
			return movieSubject(m)
		}
		return fmt.Sprintf("movie %d", w.TitleID)
	}
	t, err := s.Store.Series(ctx, w.TitleID)
	if err != nil {
		return fmt.Sprintf("show %d", w.TitleID)
	}
	var codes []string
	season := -1
	for _, id := range w.Episodes {
		e, err := s.Store.Episode(ctx, id)
		if err != nil {
			continue
		}
		codes = append(codes, fmt.Sprintf("S%02dE%02d", e.Season, e.Number))
		if season < 0 || season == e.Season {
			season = e.Season
		} else {
			season = -2 // more than one
		}
	}
	slices.Sort(codes)
	switch {
	case len(codes) == 0:
		return t.Title
	case len(codes) >= seasonSearchFrom && season >= 0:
		return fmt.Sprintf("%s season %d (%d episodes)", t.Title, season, len(codes))
	}
	return t.Title + " " + strings.Join(codes, ", ")
}

// RegrabsEvery looks at waiting replacements every few minutes, and
// grabs them once there's room.
func (s *Service) RegrabsEvery(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
		s.TakeWaitingRegrabs(ctx)
	}
}

// TakeWaitingRegrabs searches again for waiting replacements, oldest
// first, while the day has room for them, and grabs what's still wanted.
// One the search no longer finds anything for is forgotten: the patrol
// looks for it as it does for anything else. They wait while downloading
// is off or the data cap has paused upgrades.
func (s *Service) TakeWaitingRegrabs(ctx context.Context) (got, problems []string) {
	waiting, err := s.Store.RegrabsWaiting(ctx)
	if err != nil || len(waiting) == 0 {
		return nil, nil
	}
	if d, err := downloader.LoadSettings(ctx, s.Store); err != nil || !d.Enabled {
		return nil, nil
	}
	if c, err := downloader.LoadCap(ctx, s.Store); err != nil || c.Level != downloader.CapOK {
		return nil, nil
	}
	if _, left, err := s.RegrabsToday(ctx); err != nil || left == 0 {
		return nil, nil
	}
	// It takes RSS sync's turn, so the two don't grab for the same title
	// at once.
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
	tried := 0
	for _, w := range waiting {
		if ctx.Err() != nil {
			break
		}
		if _, left, err := s.RegrabsToday(ctx); err != nil || left == 0 {
			break
		}
		// Forgotten first: if the day fills up again while it's grabbed,
		// grab notes it again, in its old place.
		if err := s.Store.DropRegrab(ctx, w.Key); err != nil {
			problems = append(problems, err.Error())
			break
		}
		var rw regrabWant
		if err := json.Unmarshal([]byte(w.Data), &rw); err != nil {
			continue
		}
		tried++
		ok, err := s.regrabAgain(ctx, rw, w)
		switch {
		case errors.Is(err, ErrNoIndexers):
			// Nothing can be searched now: it keeps waiting.
			_ = s.Store.WaitRegrab(ctx, w)
			return got, append(problems, err.Error())
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", w.Subject, err))
		case ok:
			got = append(got, w.Subject)
		}
	}
	if tried > 0 {
		s.logger().Info("waiting replacements looked for again", "tried", tried, "grabbed", strings.Join(got, ", "), "problems", strings.Join(problems, "; "))
	}
	return got, problems
}

// regrabAgain searches for a waiting replacement as it was first looked
// for, and grabs the best of what's found; ok says something was grabbed.
func (s *Service) regrabAgain(ctx context.Context, rw regrabWant, w store.RegrabWait) (bool, error) {
	ctx = automatic(ctx)
	if rw.Reported {
		ctx = reported(byHand(ctx))
	}
	switch rw.Kind {
	case "movie":
		if b, err := s.busy(ctx, "movie", rw.TitleID, 0, ""); err != nil || b {
			return false, err
		}
		res, err := s.MovieFor(ctx, rw.TitleID, Ask{})
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrLocked) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		r, err := s.pick(ctx, res, "movie", rw.TitleID, 0)
		if err != nil {
			return false, err
		}
		return r.Status == "done" && strings.HasPrefix(r.Message, "grabbed "), nil
	case "series":
		t, err := s.Store.Series(ctx, rw.TitleID)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var eps []store.Episode
		season := -1
		for _, id := range rw.Episodes {
			e, err := s.Store.Episode(ctx, id)
			if err != nil || e.File == nil {
				continue // gone, or no copy to replace any more
			}
			if season < 0 {
				season = e.Season
			}
			if e.Season == season {
				eps = append(eps, e)
			}
		}
		if len(eps) == 0 {
			return false, nil
		}
		o, err := s.findEpisodes(ctx, t, season, eps)
		if err != nil {
			return false, err
		}
		if o.noIndexers {
			return false, ErrNoIndexers
		}
		if len(o.problems) > 0 {
			return o.grabbed > 0, errors.New(compactProblems(o.problems))
		}
		return o.grabbed > 0, nil
	}
	return false, nil
}
