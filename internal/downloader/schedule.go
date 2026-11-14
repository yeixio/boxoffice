package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// The speed schedule: times of the week when downloads go slower, at full
// speed or not at all, such as slow in the evening while people stream and
// full speed overnight. Outside its times the speed limit setting holds.
// A quick change from the Downloads page (an override) beats both for a
// while: an hour, until the schedule next changes, or until it's undone.
//
// Times are the server's own (its TZ), the same clock the data cap's month
// uses.

// Settings keys.
const (
	SettingSchedule = "downloads_speed_schedule" // JSON SpeedSchedule; unset is off
	SettingOverride = "downloads_speed_override" // JSON SpeedOverride; unset is none
)

// Speed modes.
const (
	SpeedFull  = "full"  // no limit
	SpeedLimit = "limit" // held to a speed
	SpeedPause = "pause" // nothing downloads; unpacking carries on
)

// What decided the speed now.
const (
	FromSetting  = "setting"  // the speed limit setting
	FromSchedule = "schedule" // one of the schedule's times
	FromOverride = "override" // a quick change on the Downloads page
)

// MaxRanges is how many times a schedule holds.
const MaxRanges = 24

const (
	dayMinutes  = 24 * 60
	weekMinutes = 7 * dayMinutes
)

// SpeedRange is one time of the week and the speed in it.
type SpeedRange struct {
	Days  []int  `json:"days"`  // 0 Sunday to 6 Saturday: the days it starts on
	From  string `json:"from"`  // "HH:MM"
	To    string `json:"to"`    // "HH:MM"; before From runs past midnight, the same as From is all day
	Mode  string `json:"mode"`  // SpeedFull, SpeedLimit or SpeedPause
	Limit int64  `json:"limit"` // KB/s, for SpeedLimit
}

// SpeedSchedule is the week's times.
type SpeedSchedule struct {
	Enabled bool         `json:"enabled"`
	Ranges  []SpeedRange `json:"ranges"`
}

// SpeedOverride is a quick change of speed that beats the schedule.
type SpeedOverride struct {
	Mode  string     `json:"mode"`
	Limit int64      `json:"limit"`           // KB/s, for SpeedLimit
	Until *time.Time `json:"until,omitempty"` // nil: until it's undone
}

// SpeedStep is a speed: what's on now or what comes next.
type SpeedStep struct {
	Mode   string `json:"mode"`
	Limit  int64  `json:"limit"`  // KB/s, for SpeedLimit
	Source string `json:"source"` // FromSetting, FromSchedule or FromOverride
	Range  int    `json:"range"`  // the schedule's time, from FromSchedule; else -1
}

// SpeedNow is the speed downloads are held to now, why, and until when.
type SpeedNow struct {
	SpeedStep
	// Until is when it next changes, if it does within a week.
	Until *time.Time `json:"until,omitempty"`
	// Next is what it changes to then.
	Next *SpeedStep `json:"next,omitempty"`
	// Override is the quick change in force, if one is.
	Override *SpeedOverride `json:"override,omitempty"`
	// Zone and Clock say whose clock the schedule's times are on: the
	// server's time zone and its time now ("15:04").
	Zone  string `json:"zone"`
	Clock string `json:"clock"`
}

// LoadSchedule reads the schedule; with none saved it's off and empty.
func LoadSchedule(ctx context.Context, st *store.Store) (SpeedSchedule, error) {
	s := SpeedSchedule{Ranges: []SpeedRange{}}
	v, err := st.Setting(ctx, SettingSchedule)
	if err != nil || v == "" {
		return s, err
	}
	if json.Unmarshal([]byte(v), &s) != nil || s.Ranges == nil {
		s = SpeedSchedule{Ranges: []SpeedRange{}}
	}
	return s, nil
}

// SaveSchedule checks the schedule and writes it.
func SaveSchedule(ctx context.Context, st *store.Store, s SpeedSchedule) error {
	if err := s.Check(); err != nil {
		return err
	}
	if s.Ranges == nil {
		s.Ranges = []SpeedRange{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, SettingSchedule, string(b))
}

// LoadOverride reads the quick change; nil when there's none or it ran out.
func LoadOverride(ctx context.Context, st *store.Store, now time.Time) (*SpeedOverride, error) {
	v, err := st.Setting(ctx, SettingOverride)
	if err != nil || v == "" {
		return nil, err
	}
	var o SpeedOverride
	if json.Unmarshal([]byte(v), &o) != nil || !validMode(o.Mode) || (o.Until != nil && !now.Before(*o.Until)) {
		return nil, nil
	}
	return &o, nil
}

// SaveOverride writes the quick change; nil undoes it.
func SaveOverride(ctx context.Context, st *store.Store, o *SpeedOverride) error {
	if o == nil {
		return st.SetSetting(ctx, SettingOverride, "")
	}
	if !validMode(o.Mode) {
		return fmt.Errorf("pick full speed, a speed limit or pause")
	}
	if o.Mode == SpeedLimit && o.Limit < 1 {
		return fmt.Errorf("enter the speed to hold downloads to")
	}
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, SettingOverride, string(b))
}

// LoadSpeedNow says what speed downloads are held to at now.
func LoadSpeedNow(ctx context.Context, st *store.Store, set Settings, now time.Time) (SpeedNow, error) {
	sched, err := LoadSchedule(ctx, st)
	if err != nil {
		return SpeedNow{}, err
	}
	o, err := LoadOverride(ctx, st, now)
	if err != nil {
		return SpeedNow{}, err
	}
	return Speed(set.SpeedLimit, sched, o, now), nil
}

// Speed works out the speed at now from the speed limit setting (KB/s, 0
// for none), the schedule and a quick change, if there is one.
func Speed(base int64, s SpeedSchedule, o *SpeedOverride, now time.Time) SpeedNow {
	now = now.Local()
	var week []int8
	if s.Enabled {
		week = s.table()
	}
	if o != nil && o.Until != nil && !now.Before(*o.Until) {
		o = nil
	}
	at := func(t time.Time) SpeedStep {
		if o != nil && (o.Until == nil || t.Before(*o.Until)) {
			return SpeedStep{Mode: o.Mode, Limit: limitOf(o.Mode, o.Limit), Source: FromOverride, Range: -1}
		}
		if week != nil {
			if i := int(week[minuteOf(t)]) - 1; i >= 0 {
				r := s.Ranges[i]
				return SpeedStep{Mode: r.Mode, Limit: limitOf(r.Mode, r.Limit), Source: FromSchedule, Range: i}
			}
		}
		if base > 0 {
			return SpeedStep{Mode: SpeedLimit, Limit: base, Source: FromSetting, Range: -1}
		}
		return SpeedStep{Mode: SpeedFull, Source: FromSetting, Range: -1}
	}
	zone, _ := now.Zone()
	if name := time.Local.String(); name != "" && name != "Local" {
		zone = name
	}
	out := SpeedNow{SpeedStep: at(now), Override: o, Zone: zone, Clock: now.Format("15:04")}
	// When it changes: a minute at a time over the next week. The speed
	// is the same while its source and range are, so the step only has to
	// differ in those or in the speed itself.
	start := now.Truncate(time.Minute)
	for i := 1; i <= weekMinutes; i++ {
		t := start.Add(time.Duration(i) * time.Minute)
		if next := at(t); next != out.SpeedStep {
			if next.Mode == out.Mode && next.Limit == out.Limit && next.Source == out.Source {
				continue // two times in a row at the same speed
			}
			if out.Source == FromSetting && next.Source == FromSetting {
				continue
			}
			out.Until, out.Next = &t, &next
			break
		}
	}
	return out
}

func limitOf(mode string, kb int64) int64 {
	if mode == SpeedLimit {
		return kb
	}
	return 0
}

func validMode(m string) bool { return m == SpeedFull || m == SpeedLimit || m == SpeedPause }

func minuteOf(t time.Time) int { return int(t.Weekday())*dayMinutes + t.Hour()*60 + t.Minute() }

// table says, for each minute of the week from Sunday 00:00, which of the
// schedule's times has it, plus 1; 0 for none. Where two overlap (Check
// won't save that) the first wins.
func (s SpeedSchedule) table() []int8 {
	week := make([]int8, weekMinutes)
	for i, r := range s.Ranges {
		for _, p := range r.spans() {
			for m := p.start; m < p.end; m++ {
				if w := m % weekMinutes; week[w] == 0 {
					week[w] = int8(i + 1)
				}
			}
		}
	}
	return week
}

type span struct{ start, end int } // minutes of the week, end not included; may run past the week's end

func (r SpeedRange) spans() []span {
	from, err1 := clock(r.From)
	to, err2 := clock(r.To)
	if err1 != nil || err2 != nil {
		return nil
	}
	n := (to - from + dayMinutes) % dayMinutes
	if n == 0 {
		n = dayMinutes
	}
	out := make([]span, 0, len(r.Days))
	for _, d := range r.Days {
		if d >= 0 && d <= 6 {
			out = append(out, span{d*dayMinutes + from, d*dayMinutes + from + n})
		}
	}
	return out
}

// clock reads "HH:MM" as minutes into the day.
func clock(v string) (int, error) {
	h, m, ok := strings.Cut(v, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || len(m) != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q isn't a time; use 24-hour HH:MM, like 07:30 or 23:00", v)
	}
	return hh*60 + mm, nil
}

var dayNames = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Check says what's wrong with the schedule, if anything: a time that
// can't be read, a range with no days or no speed, or two ranges that
// both cover some minute of the week.
func (s SpeedSchedule) Check() error {
	if len(s.Ranges) > MaxRanges {
		return fmt.Errorf("a schedule holds up to %d times", MaxRanges)
	}
	var week [weekMinutes]int8 // which range has each minute, plus 1
	for i, r := range s.Ranges {
		n := i + 1
		if _, err := clock(r.From); err != nil {
			return fmt.Errorf("time %d: %w", n, err)
		}
		if _, err := clock(r.To); err != nil {
			return fmt.Errorf("time %d: %w", n, err)
		}
		if len(r.Days) == 0 {
			return fmt.Errorf("time %d: pick at least one day", n)
		}
		days := slices.Clone(r.Days)
		slices.Sort(days)
		if len(slices.Compact(days)) != len(r.Days) || days[0] < 0 || days[len(days)-1] > 6 {
			return fmt.Errorf("time %d: the days are 0 (Sunday) to 6 (Saturday), each once", n)
		}
		if !validMode(r.Mode) {
			return fmt.Errorf("time %d: pick full speed, a speed limit or pause", n)
		}
		if r.Mode == SpeedLimit && r.Limit < 1 {
			return fmt.Errorf("time %d: enter the speed to hold downloads to", n)
		}
		for _, p := range r.spans() {
			for m := p.start; m < p.end; m++ {
				w := m % weekMinutes
				if other := week[w]; other != 0 {
					return fmt.Errorf("times %d and %d both cover %s at %02d:%02d; change one so each time of the week has one speed",
						other, n, dayNames[w/dayMinutes], w%dayMinutes/60, w%60)
				}
				week[w] = int8(n)
			}
		}
	}
	return nil
}

// Why says, for the Downloads page, why nothing downloads while the
// schedule or a quick change pauses it.
func (s SpeedNow) Why() string {
	who := "the speed schedule"
	if s.Source == FromOverride {
		who = "a speed change on the Downloads page"
	}
	if s.Until != nil {
		return fmt.Sprintf("%s pauses downloading until %s", who, s.Until.Local().Format("15:04"))
	}
	return who + " pauses downloading"
}
