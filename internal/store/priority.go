package store

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"
)

// Why a download was grabbed, kept on it (downloads.reason) so the queue
// can put first what matters most. "" is one added by hand, or grabbed
// before reasons were kept and not looked at yet.
const (
	ReasonNew     = "new"     // the first copy of something just out
	ReasonBacklog = "backlog" // the first copy of something out a while
	ReasonUpgrade = "upgrade" // a better quality than the copy there
	ReasonFix     = "fix"     // a PROPER or REPACK of the copy there
	ReasonReplace = "replace" // in place of a copy reported as bad
	ReasonSecond  = "second"  // a movie's second copy at another quality
)

// The groups the download order is made of: why it was grabbed, and for
// first copies, what it is.
const (
	GroupNewEpisodes     = "new-episodes"
	GroupNewMovies       = "new-movies"
	GroupBacklogEpisodes = "backlog-episodes"
	GroupBacklogMovies   = "backlog-movies"
	GroupBooks           = "books"
	GroupMusic           = "music"
	GroupGames           = "games"
	GroupReplacements    = "replacements"
	GroupFixes           = "fixes"
	GroupSecondCopies    = "second-copies"
	GroupUpgrades        = "upgrades"
	GroupOther           = "other"
)

// DefaultPriority is the download order until someone sets another: new
// things first, then what's missing, then better copies.
var DefaultPriority = []string{
	GroupNewEpisodes, GroupNewMovies, GroupBacklogEpisodes, GroupBacklogMovies, GroupBooks, GroupMusic, GroupGames,
	GroupReplacements, GroupFixes, GroupSecondCopies, GroupUpgrades, GroupOther,
}

// Pinned says where a download was moved by hand: it stays there,
// whatever the order.
const (
	PinnedTop    = 1
	PinnedBottom = -1
)

// Settings keys for the download order.
const (
	SettingPriority        = "downloads_priority"          // JSON list of groups
	SettingPriorityNewDays = "downloads_priority_new_days" // unset is DefaultNewDays
	SettingPriorityPreempt = "downloads_priority_preempt"  // "1" stops the one on for one ahead of it
)

// DefaultNewDays is how long after it comes out a movie or episode
// counts as new: as long as the library patrol counts it new.
const DefaultNewDays = 30

// QueuePriority is the order downloads are fetched in.
type QueuePriority struct {
	Order []string `json:"order"`
	// NewDays is how long after it came out (aired, or out to buy or
	// stream) a movie or episode counts as new.
	NewDays int `json:"newDays"`
	// Preempt stops the download that's on when one ranked ahead of it
	// comes in, the way Move to top does; it carries on afterwards.
	Preempt bool `json:"preempt"`
}

// PriorityGroup says which group of the download order a download is in.
func (d Download) PriorityGroup() string {
	switch d.Reason {
	case ReasonReplace:
		return GroupReplacements
	case ReasonFix:
		return GroupFixes
	case ReasonSecond:
		return GroupSecondCopies
	case ReasonUpgrade:
		return GroupUpgrades
	}
	switch d.Kind {
	case "series":
		if d.Reason == ReasonNew {
			return GroupNewEpisodes
		}
		return GroupBacklogEpisodes
	case "movie":
		if d.Reason == ReasonNew {
			return GroupNewMovies
		}
		return GroupBacklogMovies
	case Ebook, Audiobook:
		return GroupBooks
	case AlbumKind:
		return GroupMusic
	case GameKind:
		return GroupGames
	}
	return GroupOther
}

// CleanOrder keeps the groups it knows, once each, and adds any left out
// at the end, in the default order.
func CleanOrder(order []string) []string {
	out := make([]string, 0, len(DefaultPriority))
	for _, g := range order {
		if slices.Contains(DefaultPriority, g) && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	for _, g := range DefaultPriority {
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// QueuePriority reads the download order.
func (s *Store) QueuePriority(ctx context.Context) (QueuePriority, error) {
	p := QueuePriority{NewDays: DefaultNewDays}
	v, err := s.Setting(ctx, SettingPriority)
	if err != nil {
		return p, err
	}
	var order []string
	if v != "" {
		json.Unmarshal([]byte(v), &order)
	}
	p.Order = CleanOrder(order)
	if v, err = s.Setting(ctx, SettingPriorityNewDays); err != nil {
		return p, err
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		p.NewDays = n
	}
	if v, err = s.Setting(ctx, SettingPriorityPreempt); err != nil {
		return p, err
	}
	p.Preempt = v == "1"
	return p, nil
}

// SaveQueuePriority writes the download order. Nothing stored on the
// downloads changes: the queue is sorted by it as it's read.
func (s *Store) SaveQueuePriority(ctx context.Context, p QueuePriority) error {
	order, _ := json.Marshal(CleanOrder(p.Order))
	preempt := ""
	if p.Preempt {
		preempt = "1"
	}
	for k, v := range map[string]string{SettingPriority: string(order), SettingPriorityNewDays: strconv.Itoa(max(p.NewDays, 1)), SettingPriorityPreempt: preempt} {
		if err := s.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// QueueRank is where a download goes in the queue: those moved to the top
// by hand first, then by their group in the order, then those moved to
// the bottom; within each, by position.
func QueueRank(d Download, order []string) (tier, group int) {
	switch d.Pinned {
	case PinnedTop:
		return 0, 0
	case PinnedBottom:
		return 2, 0
	}
	g := slices.Index(order, d.PriorityGroup())
	if g < 0 {
		g = len(order)
	}
	return 1, g
}

// Ahead says whether a is ranked ahead of b by the order, not only by
// when it was added.
func Ahead(a, b Download, order []string) bool {
	at, ag := QueueRank(a, order)
	bt, bg := QueueRank(b, order)
	return at < bt || at == bt && ag < bg
}

// SortQueue puts downloads in queue order.
func SortQueue(list []Download, order []string) {
	slices.SortStableFunc(list, func(a, b Download) int {
		at, ag := QueueRank(a, order)
		bt, bg := QueueRank(b, order)
		switch {
		case at != bt:
			return at - bt
		case ag != bg:
			return ag - bg
		case a.Position != b.Position:
			return a.Position - b.Position
		}
		return int(a.ID - b.ID)
	})
}

// IsNew says whether something that came out at when (aired, or out to
// buy or stream) is new: out in the last days days, or not out yet.
func IsNew(when time.Time, days int, now time.Time) bool {
	return !when.IsZero() && when.After(now.AddDate(0, 0, -days))
}

// SetDownloadReason notes why a download was grabbed.
func (s *Store) SetDownloadReason(ctx context.Context, id int64, reason string) error {
	_, err := s.exec(ctx, "UPDATE downloads SET reason = ? WHERE id = ?", reason, id)
	return err
}

// UnpinDownload puts a download moved by hand back in its place in the
// order.
func (s *Store) UnpinDownload(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "UPDATE downloads SET pinned = 0 WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}
