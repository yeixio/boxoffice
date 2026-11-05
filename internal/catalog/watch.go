package catalog

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Which seasons of a newly added show to watch. The default keeps a long
// show from flooding the downloader: only the last few seasons that have
// aired, plus any still to come, are watched. Older seasons stay listed and
// can be switched on from the show's page.
const (
	SettingShowMonitor = "series_monitor"        // the rule for new shows; DefaultShowMonitor when unset
	SettingShowRecent  = "series_monitor_recent" // seasons the "recent" rule keeps; DefaultShowRecent when unset

	DefaultShowMonitor = MonitorRecent
	DefaultShowRecent  = 3
	MaxShowRecent      = 99
)

// The rules for a new show's episodes. Specials are never watched.
const (
	MonitorAll    = "all"    // every season
	MonitorRecent = "recent" // the last few seasons that have aired, and new ones
	MonitorFuture = "future" // only episodes not out yet
	MonitorLatest = "latest" // the newest season
	MonitorNone   = "none"   // nothing for now
)

// Monitors lists the rules, in the order the UI offers them.
var Monitors = []string{MonitorAll, MonitorRecent, MonitorFuture, MonitorLatest, MonitorNone}

// ShowDefaults say what a new show watches when the request doesn't say.
type ShowDefaults struct {
	Monitor string `json:"monitor"`
	Recent  int    `json:"recent"`
}

// LoadShowDefaults reads the defaults for new shows.
func LoadShowDefaults(ctx context.Context, st *store.Store) (ShowDefaults, error) {
	d := ShowDefaults{Monitor: DefaultShowMonitor, Recent: DefaultShowRecent}
	v, err := st.Setting(ctx, SettingShowMonitor)
	if err != nil {
		return d, err
	}
	if slices.Contains(Monitors, v) {
		d.Monitor = v
	}
	if v, err = st.Setting(ctx, SettingShowRecent); err != nil {
		return d, err
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		d.Recent = min(n, MaxShowRecent)
	}
	return d, nil
}

// SaveShowDefaults checks and writes the defaults for new shows.
func SaveShowDefaults(ctx context.Context, st *store.Store, d ShowDefaults) error {
	if !slices.Contains(Monitors, d.Monitor) {
		return badf("there's no rule called %q", d.Monitor)
	}
	if d.Recent < 1 || d.Recent > MaxShowRecent {
		return badf("pick between 1 and %d seasons", MaxShowRecent)
	}
	mon := d.Monitor
	if mon == DefaultShowMonitor {
		mon = ""
	}
	if err := st.SetSetting(ctx, SettingShowMonitor, mon); err != nil {
		return err
	}
	recent := ""
	if d.Recent != DefaultShowRecent {
		recent = strconv.Itoa(d.Recent)
	}
	return st.SetSetting(ctx, SettingShowRecent, recent)
}

// monitorFor fills in o.Monitor and o.Recent from the defaults when the
// request leaves them out, and checks the ones given.
func (s *Service) monitorFor(ctx context.Context, o *AddOptions) error {
	if o.Monitor != "" && !slices.Contains(Monitors, o.Monitor) {
		return badf("there's no rule called %q for which episodes to get", o.Monitor)
	}
	if o.Monitor != "" && (o.Monitor != MonitorRecent || o.Recent > 0) {
		o.Recent = min(o.Recent, MaxShowRecent)
		return nil
	}
	d, err := LoadShowDefaults(ctx, s.Store)
	if err != nil {
		return err
	}
	if o.Monitor == "" {
		o.Monitor = d.Monitor
	}
	if o.Recent <= 0 {
		o.Recent = d.Recent
	}
	return nil
}

// WatchedSeasons says which of a show's seasons a rule watches. Specials
// (season 0) never are. "recent" keeps the last n seasons that have
// started airing, and every season that hasn't yet; a show with n seasons
// or fewer is watched whole.
func WatchedSeasons(seasons []tmdb.SeasonInfo, monitor string, n int, now time.Time) map[int]bool {
	out := map[int]bool{}
	latest := 0
	var aired []int
	for _, se := range seasons {
		latest = max(latest, se.Number)
		if se.Number > 0 && se.AirDate != nil && !se.AirDate.After(now) {
			aired = append(aired, se.Number)
		}
	}
	slices.Sort(aired)
	cut := 0 // seasons up to and including cut are left out
	if len(aired) > n && n > 0 {
		cut = aired[len(aired)-n-1]
	}
	for _, se := range seasons {
		if se.Number <= 0 {
			out[se.Number] = false
			continue
		}
		switch monitor {
		case MonitorNone:
			out[se.Number] = false
		case MonitorLatest:
			out[se.Number] = se.Number == latest
		case MonitorRecent:
			out[se.Number] = se.Number > cut
		default: // all, future, and anything a client sends that we don't know
			out[se.Number] = true
		}
	}
	return out
}
