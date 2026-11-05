package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// JobSearchWanted is the automatic search for missing titles and
// upgrades, queued every hour while it's on.
const JobSearchWanted = "search-wanted"

// Settings keys.
const (
	SettingAuto       = "search_auto"         // "off" turns automatic searches off; on otherwise
	SettingAutoPerDay = "search_auto_per_day" // searches a day; DefaultPerDay when unset
	SettingRSS        = "search_rss"          // "off" turns RSS sync off; on otherwise
	SettingRSSMinutes = "search_rss_minutes"  // how often; DefaultRSSMinutes when unset
)

// RSS sync: every 15 minutes unless changed, and never more often than
// every 10.
const (
	DefaultRSSMinutes = 15
	MinRSSMinutes     = 10
	MaxRSSMinutes     = 24 * 60
)

// DefaultPerDay is how many automatic searches a day Box Office makes
// unless told otherwise: few enough for a free indexer account's limit.
const DefaultPerDay = 100

// MaxPerDay caps the searches a day, however high the setting.
const MaxPerDay = 2000

// waits is how long a movie or an episode waits before it's searched for
// again automatically, by how many searches in a row found nothing to take:
// a day, then 3, 7, 14 and 30 days.
var waits = []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour}

func againAfter(misses int) time.Duration { return waits[min(max(misses-1, 0), len(waits)-1)] }

// AutoSettings say whether and how much Box Office searches by itself.
type AutoSettings struct {
	Enabled bool `json:"enabled"`
	PerDay  int  `json:"perDay"` // searches a day, across all titles
	// RSS sync: reading the indexers' lists of new releases for anything
	// wanted.
	RSS        bool `json:"rss"`
	RSSMinutes int  `json:"rssMinutes"`
}

// LoadAutoSettings reads the settings; automatic searches are on, at
// DefaultPerDay, until changed. They only run while downloading is on.
func LoadAutoSettings(ctx context.Context, st *store.Store) (AutoSettings, error) {
	v, err := st.Setting(ctx, SettingAuto)
	if err != nil {
		return AutoSettings{}, err
	}
	n, err := st.Setting(ctx, SettingAutoPerDay)
	if err != nil {
		return AutoSettings{}, err
	}
	a := AutoSettings{Enabled: v != "off", PerDay: DefaultPerDay, RSSMinutes: DefaultRSSMinutes}
	if p, err := strconv.Atoi(n); err == nil && p > 0 {
		a.PerDay = min(p, MaxPerDay)
	}
	if v, err = st.Setting(ctx, SettingRSS); err != nil {
		return a, err
	}
	a.RSS = v != "off"
	if v, err = st.Setting(ctx, SettingRSSMinutes); err != nil {
		return a, err
	}
	if m, err := strconv.Atoi(v); err == nil && m > 0 {
		a.RSSMinutes = min(max(m, MinRSSMinutes), MaxRSSMinutes)
	}
	return a, nil
}

// SaveAutoSettings writes the settings.
func SaveAutoSettings(ctx context.Context, st *store.Store, a AutoSettings) error {
	on := ""
	if !a.Enabled {
		on = "off"
	}
	if err := st.SetSetting(ctx, SettingAuto, on); err != nil {
		return err
	}
	per := ""
	if a.PerDay > 0 && a.PerDay != DefaultPerDay {
		per = strconv.Itoa(min(a.PerDay, MaxPerDay))
	}
	if err := st.SetSetting(ctx, SettingAutoPerDay, per); err != nil {
		return err
	}
	rss := ""
	if !a.RSS {
		rss = "off"
	}
	if err := st.SetSetting(ctx, SettingRSS, rss); err != nil {
		return err
	}
	mins := ""
	if a.RSSMinutes > 0 && a.RSSMinutes != DefaultRSSMinutes {
		mins = strconv.Itoa(min(max(a.RSSMinutes, MinRSSMinutes), MaxRSSMinutes))
	}
	return st.SetSetting(ctx, SettingRSSMinutes, mins)
}

// Wanted is a movie or an episode Box Office would search for by itself:
// one with no file, or one whose file is below its profile's cutoff while
// the profile takes upgrades.
type Wanted struct {
	Kind     string    `json:"kind"` // movie or episode
	ID       int64     `json:"id"`   // the movie's or the episode's
	SeriesID int64     `json:"seriesId,omitempty"`
	Subject  string    `json:"subject"`
	Upgrade  bool      `json:"upgrade"`
	Have     int       `json:"have,omitempty"` // the file's quality, for an upgrade
	Cutoff   int       `json:"cutoff"`
	Searched time.Time `json:"searched,omitzero"` // when it was last searched for
	Misses   int       `json:"misses,omitempty"`  // searches in a row that found nothing to take
	// New means it came out in the last few weeks: missing titles that are
	// new go before ones missing for longer.
	New    bool `json:"new,omitempty"`
	Season int  `json:"season,omitempty"` // an episode's
}

// newFor is how long a missing title counts as new.
const newFor = 30 * 24 * time.Hour

// tier is the order the patrol takes things in: newly missing, missing
// for longer, then upgrades (jobWanted takes missing ones and upgrades in
// turn, so upgrades aren't starved).
func (w Wanted) tier() int {
	switch {
	case w.Upgrade:
		return 2
	case w.New:
		return 0
	}
	return 1
}

// due says whether it's time to search for it again.
func (w Wanted) due(now time.Time) bool {
	return w.Searched.IsZero() || now.Sub(w.Searched) >= againAfter(w.Misses)
}

// WantedList is every movie and episode that's missing or due an upgrade,
// in the order automatic searches take them: newly missing, missing for
// longer, then upgrades; within each, never searched, then the longest ago.
func (s *Service) WantedList(ctx context.Context) ([]Wanted, error) {
	ps, err := s.Store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	profiles := map[int]quality.Profile{}
	for _, p := range ps {
		profiles[p.ID] = p
	}
	now := time.Now()
	var out []Wanted

	searched, err := s.Store.Searched(ctx, "movie")
	if err != nil {
		return nil, err
	}
	movies, err := s.Store.Movies(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range movies {
		p, ok := profiles[m.ProfileID]
		if !m.Monitored || !ok || p.Locked {
			continue
		}
		w := Wanted{Kind: "movie", ID: m.ID, Subject: movieSubject(m), Cutoff: p.Cutoff, Searched: searched[m.ID].At, Misses: searched[m.ID].Misses}
		switch {
		case m.File == nil:
			if !released(m, now) {
				continue
			}
			w.New = releasedSince(m, now.Add(-newFor))
		case p.CutoffUnmet(m.File.Quality):
			w.Upgrade, w.Have = true, m.File.Quality
		default:
			continue
		}
		out = append(out, w)
	}

	if searched, err = s.Store.Searched(ctx, "episode"); err != nil {
		return nil, err
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
			if !e.Monitored || !seasons[e.Season] || e.Season == 0 || e.AirDate == nil || !e.AirDate.Before(now) {
				continue
			}
			w := Wanted{Kind: "episode", ID: e.ID, SeriesID: t.ID, Subject: fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number),
				Cutoff: p.Cutoff, Searched: searched[e.ID].At, Misses: searched[e.ID].Misses, Season: e.Season}
			switch {
			case e.File == nil:
				w.New = e.AirDate.After(now.Add(-newFor))
			case p.CutoffUnmet(e.File.Quality):
				w.Upgrade, w.Have = true, e.File.Quality
			default:
				continue
			}
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b Wanted) int {
		if c := cmp.Compare(a.tier(), b.tier()); c != 0 {
			return c
		}
		if c := a.Searched.Compare(b.Searched); c != 0 {
			return c
		}
		return cmp.Compare(a.Subject, b.Subject)
	})
	return out, nil
}

func movieSubject(m store.Movie) string {
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}

// released says whether a movie can be downloaded yet: it's out on
// digital or disc, or it left cinemas a while ago.
func released(m store.Movie, now time.Time) bool {
	for _, d := range []*time.Time{m.DigitalRelease, m.PhysicalRelease} {
		if d != nil && d.Before(now) {
			return true
		}
	}
	if m.InCinemas != nil {
		return m.InCinemas.Before(now.AddDate(0, 0, -90))
	}
	return m.Status == "Released"
}

// releasedSince says whether a movie came out (as released means) after t.
func releasedSince(m store.Movie, t time.Time) bool {
	var out time.Time
	for _, d := range []*time.Time{m.DigitalRelease, m.PhysicalRelease} {
		if d != nil && (out.IsZero() || d.Before(out)) {
			out = *d
		}
	}
	if out.IsZero() && m.InCinemas != nil {
		out = m.InCinemas.AddDate(0, 0, 90)
	}
	return !out.IsZero() && out.After(t)
}

// AutoSearchEvery queues the automatic search once an hour, a few minutes
// after Box Office starts.
func (s *Service) AutoSearchEvery(ctx context.Context, r *jobs.Runner) {
	wait := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		if ok, _ := s.AutoDue(ctx); ok {
			r.Queue(ctx, JobSearchWanted, "Missing titles and upgrades", "{}")
		}
	}
}

// AutoDue says whether the automatic search should run, and why not.
func (s *Service) AutoDue(ctx context.Context) (bool, string) {
	a, err := LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return false, err.Error()
	}
	if !a.Enabled {
		return false, "automatic searches are off"
	}
	d, err := downloader.LoadSettings(ctx, s.Store)
	if err != nil {
		return false, err.Error()
	}
	if !d.Enabled {
		return false, "downloading is off, so there's nothing to grab with"
	}
	return true, ""
}

// jobWanted is the library patrol: it searches for the next few missing
// titles and upgrades, a twenty-fourth of the day's searches each hour,
// and never more than half of what the indexers have left today. Newly
// missing titles go first, then ones missing longer, then upgrades; a
// season with a few episodes wanted is searched as one. Near the month's
// data cap, upgrades wait, then everything does.
func (s *Service) jobWanted(ctx context.Context, _ string) (jobs.Result, error) {
	if ok, why := s.AutoDue(ctx); !ok {
		return jobs.Skipped("%s", why), nil
	}
	ctx = automatic(ctx)
	a, err := LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return jobs.Result{}, err
	}
	c, err := downloader.LoadCap(ctx, s.Store)
	if err != nil {
		return jobs.Result{}, err
	}
	if c.Level == downloader.CapMissing || c.Level == downloader.CapFull {
		return jobs.Skipped("%s", c.Why()), nil
	}
	budget := max(1, (a.PerDay+23)/24)
	ixs, notes, err := s.indexers(ctx)
	if errors.Is(err, ErrNoIndexers) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	if len(ixs) == 0 {
		return jobs.Skipped("no indexer can be searched now (%s)", strings.Join(notes, "; ")), nil
	}
	{
		room, tight, err := s.autoSearchRoom(ctx, ixs)
		if err != nil {
			return jobs.Result{}, err
		}
		if room == 0 {
			return jobs.Skipped("%s", tight), nil
		}
		if room > 0 {
			budget = min(budget, room)
		}
	}
	list, err := s.WantedList(ctx)
	if err != nil {
		return jobs.Result{}, err
	}
	now := time.Now()
	upgradesPaused := c.Level == downloader.CapUpgrades
	var due []Wanted
	held := 0
	for _, w := range list {
		if !w.due(now) {
			continue
		}
		if w.Upgrade && upgradesPaused {
			held++
			continue
		}
		due = append(due, w)
	}
	// Missing titles and upgrades take turns, so a long list of missing
	// ones can't keep upgrades from ever coming up. Each keeps its own
	// order (newly missing first).
	{
		var lack, better []Wanted
		for _, w := range due {
			if w.Upgrade {
				better = append(better, w)
			} else {
				lack = append(lack, w)
			}
		}
		due = interleave(lack, better)
	}
	// The episodes due of each season, to search for together.
	type seasonKey struct {
		series int64
		season int
	}
	seasons := map[seasonKey][]int64{}
	for _, w := range due {
		if w.Kind == "episode" {
			k := seasonKey{w.SeriesID, w.Season}
			seasons[k] = append(seasons[k], w.ID)
		}
	}
	done := map[seasonKey]bool{}
	var missing, upgrades int
	var o outcome
	searchedAny := false
	for _, w := range due {
		if o.searches >= budget || ctx.Err() != nil {
			break
		}
		if w.Kind == "movie" {
			if b, err := s.busy(ctx, "movie", w.ID, 0, ""); err != nil {
				return jobs.Result{}, err
			} else if b {
				o.busy++
				continue
			}
			res, err := s.Movie(ctx, w.ID)
			if errors.Is(err, ErrNoIndexers) {
				return jobs.Skipped("%s", err.Error()), nil
			}
			searchedAny = true
			if w.Upgrade {
				upgrades++
			} else {
				missing++
			}
			if err != nil {
				o.problems = append(o.problems, fmt.Sprintf("%s: %v", w.Subject, err))
				continue
			}
			if res.Searched > 0 {
				o.searches++
			}
			r, err := s.pick(ctx, res, "movie", w.ID, 0)
			if err != nil {
				o.problems = append(o.problems, fmt.Sprintf("%s: %v", w.Subject, err))
				continue
			}
			if r.Status == "done" && strings.HasPrefix(r.Message, "grabbed ") {
				o.grabbed++
				o.got = append(o.got, w.Subject)
			}
			continue
		}
		k := seasonKey{w.SeriesID, w.Season}
		ids := []int64{w.ID}
		if len(seasons[k]) >= seasonSearchFrom {
			if done[k] {
				continue
			}
			done[k] = true
			ids = seasons[k]
		}
		t, err := s.Store.Series(ctx, w.SeriesID)
		if err != nil {
			o.problems = append(o.problems, fmt.Sprintf("%s: %v", w.Subject, err))
			continue
		}
		var eps []store.Episode
		for _, id := range ids {
			e, err := s.Store.Episode(ctx, id)
			if err != nil {
				continue
			}
			eps = append(eps, e)
			if e.File != nil {
				upgrades++
			} else {
				missing++
			}
		}
		got, err := s.findEpisodes(ctx, t, w.Season, eps)
		if err != nil {
			return jobs.Result{}, err
		}
		if got.noIndexers {
			return jobs.Skipped("%s", ErrNoIndexers.Error()), nil
		}
		searchedAny = true
		o.add(got)
	}
	heldNote := ""
	if held > 0 {
		heldNote = fmt.Sprintf("; %s waiting: %s", count(held, "upgrade", "upgrades"), c.Why())
	}
	if !searchedAny {
		if len(list) == 0 {
			return jobs.Done("nothing is missing, and nothing needs upgrading"), nil
		}
		return jobs.Done("everything wanted was searched for recently%s%s", busyNote(o.busy), heldNote), nil
	}
	msg := fmt.Sprintf("looked for %s in %s; grabbed %d", describeWant(missing, upgrades, "title", "titles"), count(o.searches, "search", "searches"), o.grabbed)
	if len(o.got) > 0 {
		msg += " (" + strings.Join(o.got, ", ") + ")"
	}
	if o.off {
		msg += "; downloading is off, so nothing was grabbed"
	}
	msg += busyNote(o.busy) + heldNote
	if len(o.problems) > 0 {
		msg += "; " + compactProblems(o.problems)
	}
	return jobs.Done("%s", msg), nil
}

func busyNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d already downloading", n)
}

// interleave takes from a and b in turn, so neither starves the other.
func interleave[T any](a, b []T) []T {
	out := make([]T, 0, len(a)+len(b))
	for i := 0; i < len(a) || i < len(b); i++ {
		if i < len(a) {
			out = append(out, a[i])
		}
		if i < len(b) {
			out = append(out, b[i])
		}
	}
	return out
}
