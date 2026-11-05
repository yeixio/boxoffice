package downloader

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// The monthly data cap: an internet plan's allowance, which Box Office
// keeps under by pausing upgrades first, then searches for missing titles,
// then downloading, as the month's downloads near it.

// Settings keys.
const (
	SettingCapGB       = "data_cap_gb"       // a month, in GB (1000³ bytes); "off" for none; DefaultCapGB when unset
	SettingCapUpgrades = "data_cap_upgrades" // percent of the cap at which upgrades pause
	SettingCapMissing  = "data_cap_missing"  // percent at which automatic searches for missing titles pause
	SettingCapDay      = "data_cap_day"      // the day of the month the plan's month starts
)

// Defaults: a 3 TB month, upgrades pausing at 70% (2.1 TB) and missing
// titles at 90% (2.7 TB), leaving the rest for everything else at home.
const (
	DefaultCapGB       = 3000
	DefaultCapUpgrades = 70
	DefaultCapMissing  = 90
)

const gb = 1000 * 1000 * 1000

// CapSettings are the data cap.
type CapSettings struct {
	Enabled    bool `json:"enabled"`
	GB         int  `json:"gb"`         // a month
	UpgradesAt int  `json:"upgradesAt"` // percent
	MissingAt  int  `json:"missingAt"`  // percent
	ResetDay   int  `json:"resetDay"`   // 1 to 28
}

// LoadCapSettings reads the data cap, with the defaults for what's unset.
func LoadCapSettings(ctx context.Context, st *store.Store) (CapSettings, error) {
	c := CapSettings{Enabled: true, GB: DefaultCapGB, UpgradesAt: DefaultCapUpgrades, MissingAt: DefaultCapMissing, ResetDay: 1}
	read := func(key string, into *int, lo, hi int) error {
		v, err := st.Setting(ctx, key)
		if err != nil {
			return err
		}
		if key == SettingCapGB && v == "off" {
			c.Enabled = false
			return nil
		}
		if n, err := strconv.Atoi(v); err == nil && n >= lo && n <= hi {
			*into = n
		}
		return nil
	}
	for _, r := range []struct {
		key    string
		into   *int
		lo, hi int
	}{{SettingCapGB, &c.GB, 1, 1e7}, {SettingCapUpgrades, &c.UpgradesAt, 1, 100}, {SettingCapMissing, &c.MissingAt, 1, 100}, {SettingCapDay, &c.ResetDay, 1, 28}} {
		if err := read(r.key, r.into, r.lo, r.hi); err != nil {
			return c, err
		}
	}
	c.UpgradesAt = min(c.UpgradesAt, c.MissingAt)
	return c, nil
}

// SaveCapSettings writes the data cap; defaults are saved as unset.
func SaveCapSettings(ctx context.Context, st *store.Store, c CapSettings) error {
	val := func(n, def int) string {
		if n == def {
			return ""
		}
		return strconv.Itoa(n)
	}
	capGB := val(c.GB, DefaultCapGB)
	if !c.Enabled {
		capGB = "off"
	}
	for k, v := range map[string]string{SettingCapGB: capGB, SettingCapUpgrades: val(c.UpgradesAt, DefaultCapUpgrades),
		SettingCapMissing: val(c.MissingAt, DefaultCapMissing), SettingCapDay: val(c.ResetDay, 1)} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// Cap levels: what the month's downloads so far have paused.
const (
	CapOK       = ""
	CapUpgrades = "upgrades" // automatic upgrades wait
	CapMissing  = "missing"  // all automatic searches and grabs wait
	CapFull     = "full"     // downloading waits
)

// CapState is the month so far.
type CapState struct {
	CapSettings
	Used   int64     `json:"used"`  // bytes downloaded this month
	Limit  int64     `json:"limit"` // bytes; 0 with no cap
	Since  time.Time `json:"since"` // the month started
	Resets time.Time `json:"resets"`
	Level  string    `json:"level"`
}

// monthStart is the start of the plan's month t is in, and the next one's.
func monthStart(t time.Time, day int) (time.Time, time.Time) {
	t = t.Local()
	start := time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.Local)
	if t.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start, start.AddDate(0, 1, 0)
}

// LoadCap says how much of the month's cap is used, and what's paused.
func LoadCap(ctx context.Context, st *store.Store) (CapState, error) {
	set, err := LoadCapSettings(ctx, st)
	if err != nil {
		return CapState{}, err
	}
	c := CapState{CapSettings: set}
	c.Since, c.Resets = monthStart(time.Now(), set.ResetDay)
	use, err := st.DataUsage(ctx, store.DataDay(c.Since))
	if err != nil {
		return c, err
	}
	for _, n := range use {
		c.Used += n
	}
	if !set.Enabled {
		return c, nil
	}
	c.Limit = int64(set.GB) * gb
	switch {
	case c.Used >= c.Limit:
		c.Level = CapFull
	case c.Used*100 >= c.Limit*int64(set.MissingAt):
		c.Level = CapMissing
	case c.Used*100 >= c.Limit*int64(set.UpgradesAt):
		c.Level = CapUpgrades
	}
	return c, nil
}

// Why says what the cap has paused, for people; "" when nothing.
func (c CapState) Why() string {
	used := fmt.Sprintf("%s of this month's %s data cap is used", humanBytes(c.Used), humanBytes(c.Limit))
	resets := c.Resets.Format("January 2")
	switch c.Level {
	case CapFull:
		return fmt.Sprintf("%s, so downloading waits until %s", used, resets)
	case CapMissing:
		return fmt.Sprintf("%s, so automatic searches wait until %s", used, resets)
	case CapUpgrades:
		return fmt.Sprintf("%s, so upgrades wait until %s", used, resets)
	}
	return ""
}

// ---- counting ----

// dataCounter adds up the bytes downloaded from each news server, and
// saves them now and then.
type dataCounter struct {
	mu sync.Mutex
	by map[int64]*atomic.Int64
}

func (d *dataCounter) of(serverID int64) *atomic.Int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.by == nil {
		d.by = map[int64]*atomic.Int64{}
	}
	n, ok := d.by[serverID]
	if !ok {
		n = &atomic.Int64{}
		d.by[serverID] = n
	}
	return n
}

func (d *dataCounter) flush(ctx context.Context, st *store.Store) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, n := range d.by {
		v := n.Swap(0)
		if v == 0 {
			continue
		}
		if err := st.AddDataUsage(ctx, id, v); err != nil {
			n.Add(v)
			return err
		}
	}
	return nil
}

// ---- block accounts ----

// QuotaState is how a news server's block account stands.
type QuotaState struct {
	Used    int64  `json:"used"`              // since the quota was set
	Left    int64  `json:"left"`              // -1 for no quota
	Problem string `json:"problem,omitempty"` // why it's not used, if it isn't
}

// quotas is how each server's block account stands, by server.
func quotas(ctx context.Context, st *store.Store, list []store.UsenetServer) (map[int64]QuotaState, error) {
	out := map[int64]QuotaState{}
	now := time.Now()
	for _, u := range list {
		q := QuotaState{Left: -1}
		if u.Quota > 0 {
			since := u.QuotaSince
			if since.IsZero() {
				since = now
			}
			use, err := st.DataUsage(ctx, store.DataDay(since))
			if err != nil {
				return nil, err
			}
			q.Used = use[u.ID]
			q.Left = max(u.Quota-q.Used, 0)
			if q.Left == 0 {
				q.Problem = fmt.Sprintf("its %s block is used up; add more in its settings", humanBytes(u.Quota))
			}
		}
		if !u.Expires.IsZero() && now.After(u.Expires) {
			q.Problem = "its account expired on " + u.Expires.Local().Format("January 2, 2006") + "; renew it and change the date"
		}
		out[u.ID] = q
	}
	return out, nil
}

// Quotas is how each news server's block account stands, by server.
func (e *Engine) Quotas(ctx context.Context) (map[int64]QuotaState, error) {
	list, err := e.Store.UsenetServers(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.data.flush(ctx, e.Store); err != nil {
		return nil, err
	}
	return quotas(ctx, e.Store, list)
}
