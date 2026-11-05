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
	a := AutoSettings{Enabled: v != "off", PerDay: DefaultPerDay}
	if p, err := strconv.Atoi(n); err == nil && p > 0 {
		a.PerDay = min(p, MaxPerDay)
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
	return st.SetSetting(ctx, SettingAutoPerDay, per)
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
}

// due says whether it's time to search for it again.
func (w Wanted) due(now time.Time) bool {
	return w.Searched.IsZero() || now.Sub(w.Searched) >= againAfter(w.Misses)
}

// WantedList is every movie and episode that's missing or due an upgrade,
// in the order automatic searches take them: missing before upgrades, then
// never searched, then the longest ago.
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
		if !m.Monitored || !ok {
			continue
		}
		w := Wanted{Kind: "movie", ID: m.ID, Subject: movieSubject(m), Cutoff: p.Cutoff, Searched: searched[m.ID].At, Misses: searched[m.ID].Misses}
		switch {
		case m.File == nil:
			if !released(m, now) {
				continue
			}
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
		if !t.Monitored || !ok {
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
				Cutoff: p.Cutoff, Searched: searched[e.ID].At, Misses: searched[e.ID].Misses}
			switch {
			case e.File == nil:
			case p.CutoffUnmet(e.File.Quality):
				w.Upgrade, w.Have = true, e.File.Quality
			default:
				continue
			}
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b Wanted) int {
		if a.Upgrade != b.Upgrade {
			if !a.Upgrade {
				return -1
			}
			return 1
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

// jobWanted searches for the next few missing titles and upgrades: a
// twenty-fourth of the day's searches each hour.
func (s *Service) jobWanted(ctx context.Context, _ string) (jobs.Result, error) {
	if ok, why := s.AutoDue(ctx); !ok {
		return jobs.Skipped("%s", why), nil
	}
	a, err := LoadAutoSettings(ctx, s.Store)
	if err != nil {
		return jobs.Result{}, err
	}
	budget := max(1, (a.PerDay+23)/24)
	list, err := s.WantedList(ctx)
	if err != nil {
		return jobs.Result{}, err
	}
	now := time.Now()
	var missing, upgrades, grabbed, busy int
	var got, problems []string
	searched := 0
	for _, w := range list {
		if searched >= budget || ctx.Err() != nil {
			break
		}
		if !w.due(now) {
			continue
		}
		kind, titleID, episodeID := "movie", w.ID, int64(0)
		if w.Kind == "episode" {
			kind, titleID, episodeID = "series", w.SeriesID, w.ID
		}
		if b, err := s.busy(ctx, kind, titleID, episodeID, ""); err != nil {
			return jobs.Result{}, err
		} else if b {
			busy++
			continue
		}
		searched++
		if w.Upgrade {
			upgrades++
		} else {
			missing++
		}
		var res Results
		if w.Kind == "movie" {
			res, err = s.Movie(ctx, w.ID)
		} else {
			res, err = s.Episode(ctx, w.ID)
		}
		if errors.Is(err, ErrNoIndexers) {
			return jobs.Skipped("%s", err.Error()), nil
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", w.Subject, err))
			continue
		}
		r, err := s.pick(ctx, res, kind, titleID, episodeID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", w.Subject, err))
			continue
		}
		if r.Status == "done" && strings.HasPrefix(r.Message, "grabbed ") {
			grabbed++
			got = append(got, w.Subject)
		}
	}
	if searched == 0 {
		if len(list) == 0 {
			return jobs.Done("nothing is missing, and nothing needs upgrading"), nil
		}
		return jobs.Done("everything wanted was searched for recently%s", busyNote(busy)), nil
	}
	msg := fmt.Sprintf("looked for %s; grabbed %d", describeWant(missing, upgrades, "title", "titles"), grabbed)
	if len(got) > 0 {
		msg += " (" + strings.Join(got, ", ") + ")"
	}
	msg += busyNote(busy)
	if len(problems) > 0 {
		msg += "; " + strings.Join(problems, "; ")
	}
	return jobs.Done("%s", msg), nil
}

func busyNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d already downloading", n)
}
