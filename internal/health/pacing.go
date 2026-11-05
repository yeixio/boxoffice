package health

import (
	"context"
	"fmt"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// indexers warns of an indexer resting after errors, or near its daily
// limit.
func (c *Checker) indexers(ctx context.Context) ([]Issue, error) {
	ixs, err := c.Store.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	sts, err := c.Store.IndexerStatuses(ctx)
	if err != nil {
		return nil, err
	}
	now := c.clock()
	use, err := c.Store.IndexerUsageOn(ctx, store.UsageDay(now))
	if err != nil {
		return nil, err
	}
	var out []Issue
	link := &Link{To: "/settings#indexers", Label: "Open Settings"}
	for _, ix := range ixs {
		if !ix.EnableSearch && !ix.EnableRSS {
			continue
		}
		st := sts[ix.ID]
		if st.Resting(now) {
			out = append(out, Issue{
				ID: fmt.Sprintf("indexer-resting-%d", ix.ID), Level: "warning", Title: ix.Name + " is resting after errors",
				Detail: fmt.Sprintf("Its last answer was: %s. After %s in a row, Box Office waits until %s before asking it again, longer each time, so a struggling indexer isn't hammered.",
					st.LastError, plural(st.Failures, "error", "errors"), st.RestingUntil.Local().Format("Jan 2 15:04")),
				Steps: []string{
					"Check the indexer's website: it may be down, or your account may need attention (an expired membership, a changed API key).",
					"If its daily limit ran out, it rests an hour at a time until it resets, usually at midnight UTC.",
					"Once it's fixed, open it in Settings, under Indexers, and press Try again now.",
				},
				Link: link,
			})
			continue
		}
		limit := ix.DailyQueries
		if limit <= 0 {
			limit = st.APIMax
		}
		if u := use[ix.ID]; limit > 0 && u.Queries*10 >= limit*9 {
			out = append(out, Issue{
				ID: fmt.Sprintf("indexer-limit-%d", ix.ID), Level: "notice", Title: ix.Name + " is near its daily search limit",
				Detail: fmt.Sprintf("%d of its %d searches today are used. Automatic searches only ever use half of what's left; it resets at midnight UTC.", u.Queries, limit),
				Steps:  []string{"If this happens most days, lower Searches a day in Settings, under Missing titles and upgrades."},
				Link:   &Link{To: "/settings#automatic-searches", Label: "Open Settings"},
			})
		}
	}
	return out, nil
}

// data warns as the month's downloads near the data cap, and of block
// accounts used up or expiring.
func (c *Checker) data(ctx context.Context) ([]Issue, error) {
	var out []Issue
	dc, err := downloader.LoadCap(ctx, c.Store)
	if err != nil {
		return nil, err
	}
	if dc.Level != downloader.CapOK {
		level := "notice"
		if dc.Level == downloader.CapFull {
			level = "warning"
		}
		out = append(out, Issue{
			ID: "data-cap", Level: level, Title: "Near this month's data cap",
			Detail: titleCase(dc.Why()) + ". Downloads you start by hand still go ahead until the cap itself is reached.",
			Steps: []string{
				"Nothing needs doing: it all carries on when the month resets.",
				"If your plan's allowance is bigger, or your month starts on another day, change it in Settings, under Downloads.",
			},
			Link: &Link{To: "/settings#downloads", Label: "Open Settings"},
		})
	}
	servers, err := c.Store.UsenetServers(ctx)
	if err != nil {
		return nil, err
	}
	now := c.clock()
	var since string
	for _, u := range servers {
		if !u.Enabled {
			continue
		}
		link := &Link{To: "/settings#news-servers", Label: "Open Settings"}
		if !u.Expires.IsZero() {
			switch left := u.Expires.Sub(now); {
			case left <= 0:
				out = append(out, Issue{ID: fmt.Sprintf("server-expired-%d", u.ID), Level: "warning", Title: u.Name + "'s account has expired",
					Detail: "Box Office stopped using it on " + u.Expires.Add(-time.Second).Local().Format("January 2") + ".",
					Steps:  []string{"Renew the account on the provider's website, then change the expiry date in its settings, or turn the server off."}, Link: link})
			case left < 7*24*time.Hour:
				out = append(out, Issue{ID: fmt.Sprintf("server-expiring-%d", u.ID), Level: "notice", Title: u.Name + "'s account expires soon",
					Detail: "It expires on " + u.Expires.Add(-time.Second).Local().Format("January 2") + ".",
					Steps:  []string{"Renew it on the provider's website if you want to keep it, then change the date in its settings."}, Link: link})
			}
		}
		if u.Quota > 0 {
			if since = store.DataDay(u.QuotaSince); u.QuotaSince.IsZero() {
				since = store.DataDay(now)
			}
			use, err := c.Store.DataUsage(ctx, since)
			if err != nil {
				return nil, err
			}
			left := u.Quota - use[u.ID]
			switch {
			case left <= 0:
				out = append(out, Issue{ID: fmt.Sprintf("server-quota-%d", u.ID), Level: "warning", Title: u.Name + "'s block is used up",
					Detail: "Box Office stopped using it.", Steps: []string{"Buy more data from the provider, then enter what's left in the server's settings."}, Link: link})
			case left*10 < u.Quota:
				out = append(out, Issue{ID: fmt.Sprintf("server-quota-%d", u.ID), Level: "notice", Title: u.Name + "'s block is nearly used up",
					Detail: fmt.Sprintf("About %.1f GB of it is left.", float64(left)/1e9), Steps: []string{"Buy more data from the provider before it runs out, then enter what's left in the server's settings."}, Link: link})
			}
		}
	}
	return out, nil
}
