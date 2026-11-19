package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/zone"
)

// ---- clean-up rules (Settings → Library → Clean-up) ----
//
// A rule picks out movies or shows by what Matinee says was watched:
// everyone who started it has finished it, someone has finished it, or
// nobody has played it, and it has been quiet for so many days. What a
// rule finds is listed as leaving soon, with the files and bytes it has on
// disk. An admin deletes or keeps each one; a rule switched to delete by
// itself deletes them once their days on the list are up.
//
// Deleting takes the title's folder off the disk for good and removes the
// title, by the same plan as removing it by hand, and is as careful:
//
//   - rules only run while Matinee's report of what was watched is recent,
//     and a "nobody" rule not at all when Matinee reports no plays;
//   - titles on Do not touch, ones an admin said to keep, and ones asked
//     for in the last 30 days are never listed;
//   - only what the list showed is deleted: if the folder has changed, the
//     title's days start over;
//   - a folder that isn't there (a share that dropped out) deletes nothing;
//   - rules delete at most so many titles a day by themselves.

const (
	// cleanupEvery is how often the rules are looked at.
	cleanupEvery = time.Hour
	// cleanupReportAge is how old Matinee's report may be for rules to
	// run. Matinee sends one every six hours at least.
	cleanupReportAge = 24 * time.Hour
	// cleanupRequested keeps titles someone asked for this recently.
	cleanupRequested = 30 * 24 * time.Hour
	// cleanupPreviewMost is how many titles a preview names.
	cleanupPreviewMost = 200

	settingCleanupCap   = "cleanup_daily_cap"
	settingCleanupToday = "cleanup_auto_today" // "2026-11-19:3": how many rules deleted by themselves that day
	// DefaultCleanupCap is how many titles a day rules delete by themselves.
	DefaultCleanupCap = 5
)

func (s *Server) cleanupRoutes() []route {
	return []route{
		{"GET /api/v1/cleanup", true, s.getCleanup},
		{"PUT /api/v1/cleanup/settings", true, s.putCleanupSettings},
		{"POST /api/v1/cleanup/preview", true, s.previewCleanup},
		{"POST /api/v1/cleanup/check", true, s.checkCleanup},
		{"POST /api/v1/cleanup/rules", true, s.addCleanupRule},
		{"PUT /api/v1/cleanup/rules/{id}", true, s.saveCleanupRule},
		{"DELETE /api/v1/cleanup/rules/{id}", true, s.deleteCleanupRule},
		{"POST /api/v1/cleanup/items/{id}/keep", true, s.keepCleanupItem},
		{"DELETE /api/v1/cleanup/items/{id}", true, s.deleteCleanupItem},
		{"DELETE /api/v1/cleanup/kept/{kind}/{id}", true, s.unkeepCleanup},
	}
}

// cleanupTitle is a title a rule picks out, with why.
type cleanupTitle struct {
	Kind    string `json:"kind"`
	TitleID int64  `json:"titleId"`
	TMDBID  int64  `json:"tmdbId"`
	Title   string `json:"title"`
	Year    int    `json:"year,omitempty"`
	Poster  string `json:"poster,omitempty"`
	// Size is what Box Office knows of its files; the leaving soon list
	// has what's in its folder.
	Size       int64      `json:"size"`
	Viewers    int        `json:"viewers"`
	FinishedBy int        `json:"finishedBy"`
	LastPlayed *time.Time `json:"lastPlayed,omitempty"`
	AddedAt    time.Time  `json:"addedAt"`

	root, path string
	ended      bool
	// quietSince is when it was added, or its newest file or episode came.
	quietSince time.Time
}

// cleanupTitles is every movie or show that has files.
func (s *Server) cleanupTitles(ctx context.Context, kind string) ([]cleanupTitle, error) {
	var out []cleanupTitle
	if kind == "movie" {
		list, err := s.Store.Movies(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range list {
			if m.File == nil {
				continue
			}
			t := cleanupTitle{Kind: kind, TitleID: m.ID, TMDBID: m.TMDBID, Title: m.Title, Year: m.Year, Poster: m.Poster,
				Size: m.File.Size, AddedAt: m.AddedAt, root: m.RootFolder, path: m.Path, ended: true, quietSince: m.AddedAt}
			if m.File.AddedAt.After(t.quietSince) {
				t.quietSince = m.File.AddedAt
			}
			out = append(out, t)
		}
		return out, nil
	}
	list, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if t.SizeOnDisk == 0 {
			continue
		}
		c := cleanupTitle{Kind: kind, TitleID: t.ID, TMDBID: t.TMDBID, Title: t.Title, Year: t.Year, Poster: t.Poster,
			Size: t.SizeOnDisk, AddedAt: t.AddedAt, root: t.RootFolder, path: t.Path, quietSince: t.AddedAt,
			ended: (t.Status == "Ended" || t.Status == "Canceled") && t.NextAiring == nil}
		if t.LastAired != nil && t.LastAired.After(c.quietSince) {
			c.quietSince = *t.LastAired
		}
		out = append(out, c)
	}
	return out, nil
}

// cleanupMatches is the titles a rule picks out now, biggest first, or why
// the rule is waiting. It changes nothing.
func (s *Server) cleanupMatches(ctx context.Context, rule store.CleanupRule, now time.Time) ([]cleanupTitle, string, error) {
	report, ok, err := s.Store.LastWatchReport(ctx, rule.Kind)
	if err != nil {
		return nil, "", err
	}
	if !ok || now.Sub(report.At) > cleanupReportAge {
		return nil, store.CleanupNoReport, nil
	}
	if rule.Watched == store.CleanupNobody && report.Titles == 0 {
		return nil, store.CleanupNoPlays, nil
	}
	watched, err := s.Store.WatchedTitles(ctx, rule.Kind)
	if err != nil {
		return nil, "", err
	}
	kept, err := s.Store.CleanupKept(ctx, rule.Kind)
	if err != nil {
		return nil, "", err
	}
	locked, err := s.Store.LockedTitles(ctx, rule.Kind)
	if err != nil {
		return nil, "", err
	}
	requests, err := s.Store.Requests(ctx, "", 5000)
	if err != nil {
		return nil, "", err
	}
	asked := map[int64]bool{}
	for _, r := range requests {
		if r.Kind == rule.Kind && (r.Status == store.RequestPending || now.Sub(r.CreatedAt) < cleanupRequested) {
			asked[r.TMDBID] = true
		}
	}
	titles, err := s.cleanupTitles(ctx, rule.Kind)
	if err != nil {
		return nil, "", err
	}
	quiet := time.Duration(rule.Days) * 24 * time.Hour
	var out []cleanupTitle
	for _, t := range titles {
		if _, keep := kept[t.TitleID]; keep || locked[t.TitleID] || asked[t.TMDBID] || (rule.EndedOnly && !t.ended) {
			continue
		}
		w := watched[t.TitleID]
		t.Viewers, t.FinishedBy, t.LastPlayed = w.Viewers, w.FinishedBy, w.LastPlayed
		// Quiet since its last play, or, for one nobody has played, since
		// it (or its newest episode) came.
		last := t.quietSince
		if w.Viewers > 0 {
			last = time.Time{}
			for _, at := range []*time.Time{w.LastPlayed, w.FinishedAt} {
				if at != nil && at.After(last) {
					last = *at
				}
			}
			if last.IsZero() {
				continue // played, but Matinee didn't say when
			}
		}
		if now.Sub(last) < quiet {
			continue
		}
		switch rule.Watched {
		case store.CleanupEveryone:
			ok = w.Viewers > 0 && w.FinishedBy == w.Viewers
		case store.CleanupAnyone:
			ok = w.FinishedBy > 0
		case store.CleanupNobody:
			ok = w.Viewers == 0
		default:
			ok = false
		}
		if ok {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b cleanupTitle) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Title, b.Title))
	})
	return out, "", nil
}

// cleanupPlan plans deleting a title's folder, as removing it by hand does.
func (s *Server) cleanupPlan(ctx context.Context, kind string, id int64) (string, library.Plan, error) {
	var title, root, path string
	if kind == "movie" {
		m, err := s.Store.Movie(ctx, id)
		if err != nil {
			return "", library.Plan{}, err
		}
		title, root, path = m.Title, m.RootFolder, m.Path
	} else {
		t, err := s.Store.Series(ctx, id)
		if err != nil {
			return "", library.Plan{}, err
		}
		title, root, path = t.Title, t.RootFolder, t.Path
	}
	p, err := library.FolderPlan(ctx, s.Store, kind, id, root, path)
	if err == nil {
		err = s.lockPlan(ctx, kind, id, &p)
	}
	if err == nil && p.Problem == "" && p.Count == 0 {
		// A share that dropped out looks like an empty library. Nothing is
		// deleted, and the title stays.
		p.Problem = "its folder isn't there right now"
	}
	return title, p, err
}

// RunCleanup looks at the clean-up rules every hour until ctx ends.
func (s *Server) RunCleanup(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.Cleanup(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("couldn't look at the clean-up rules", "err", err)
		}
		t.Reset(cleanupEvery)
	}
}

// Cleanup looks at every rule that's on: it lists what the rule finds as
// leaving soon, takes off the list what it no longer finds, and, for a
// rule that deletes by itself, deletes what has been listed long enough.
func (s *Server) Cleanup(ctx context.Context) error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	rules, err := s.Store.CleanupRules(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := s.Store.CleanupItems(ctx)
		if err != nil {
			return err
		}
		listed := map[int64]bool{} // by any rule
		var mine []store.CleanupItem
		for _, i := range items {
			if i.Kind == rule.Kind {
				listed[i.TitleID] = true
			}
			if i.RuleID == rule.ID {
				mine = append(mine, i)
			}
		}
		if !rule.Enabled {
			for _, i := range mine {
				if err := s.Store.DropCleanupItem(ctx, i.ID); err != nil {
					return err
				}
			}
			continue
		}
		matches, waiting, err := s.cleanupMatches(ctx, rule, now)
		if err != nil {
			return err
		}
		if err := s.Store.CleanupChecked(ctx, rule.ID, waiting); err != nil {
			return err
		}
		if waiting != "" {
			// What's listed stays listed, and nothing is deleted.
			continue
		}
		found := map[int64]bool{}
		for _, m := range matches {
			found[m.TitleID] = true
			if listed[m.TitleID] {
				continue
			}
			_, p, err := s.cleanupPlan(ctx, m.Kind, m.TitleID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if p.Problem != "" {
				continue // it can't be deleted, so it isn't leaving
			}
			leaves := now.Add(time.Duration(rule.GraceDays) * 24 * time.Hour)
			if _, err := s.Store.ListCleanupItem(ctx, store.CleanupItem{RuleID: rule.ID, Kind: m.Kind, TitleID: m.TitleID, Files: p.Count, Size: p.Size, LeavesAt: leaves}); err != nil {
				return err
			}
			s.Log.Info("listed a title as leaving soon", "title", m.Title, "rule", rule.ID, "files", p.Count, "bytes", p.Size, "leaves", leaves.Format(time.DateOnly))
		}
		var due []store.CleanupItem
		for _, i := range mine {
			if !found[i.TitleID] {
				// Someone played it, asked for it, or it was set to stay.
				if err := s.Store.DropCleanupItem(ctx, i.ID); err != nil {
					return err
				}
			} else if rule.Auto && !i.LeavesAt.After(now) {
				due = append(due, i)
			}
		}
		for _, i := range due {
			left, err := s.cleanupLeftToday(ctx, now)
			if err != nil {
				return err
			}
			if left <= 0 {
				break // the rest go tomorrow
			}
			if err := s.deleteCleanup(ctx, i, rule, i.Files, i.Size, "A clean-up rule deleted it."); err != nil {
				s.Log.Warn("a clean-up rule couldn't delete a title", "kind", i.Kind, "title", i.TitleID, "err", err)
				continue
			}
			if err := s.cleanupCountToday(ctx, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupSoon looks at the rules now, without making the page wait:
// listing reads each title's folder, which is slow on some disks.
func (s *Server) cleanupSoon(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		if err := s.Cleanup(ctx); err != nil {
			s.Log.Warn("couldn't look at the clean-up rules", "err", err)
		}
	}()
}

// cleanupCap is how many titles a day rules delete by themselves.
func (s *Server) cleanupCap(ctx context.Context) (int, error) {
	v, err := s.Store.Setting(ctx, settingCleanupCap)
	if err != nil || v == "" {
		return DefaultCleanupCap, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return DefaultCleanupCap, nil
	}
	return n, nil
}

func (s *Server) cleanupToday(ctx context.Context, now time.Time) (int, error) {
	v, err := s.Store.Setting(ctx, settingCleanupToday)
	if err != nil {
		return 0, err
	}
	day, n, _ := strings.Cut(v, ":")
	if day != zone.In(now).Format(time.DateOnly) {
		return 0, nil
	}
	count, _ := strconv.Atoi(n)
	return count, nil
}

// cleanupLeftToday is how many more titles rules may delete by themselves
// today.
func (s *Server) cleanupLeftToday(ctx context.Context, now time.Time) (int, error) {
	most, err := s.cleanupCap(ctx)
	if err != nil {
		return 0, err
	}
	done, err := s.cleanupToday(ctx, now)
	return most - done, err
}

func (s *Server) cleanupCountToday(ctx context.Context, now time.Time) error {
	done, err := s.cleanupToday(ctx, now)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(ctx, settingCleanupToday, zone.In(now).Format(time.DateOnly)+":"+strconv.Itoa(done+1))
}

// deleteCleanup deletes a listed title's folder and removes the title, if
// its folder still holds the files and bytes that were shown. If it holds
// something else, the list is brought up to date and the title's days
// start over. The caller holds cleanupMu.
func (s *Server) deleteCleanup(ctx context.Context, i store.CleanupItem, rule store.CleanupRule, files int, size int64, why string) error {
	title, p, err := s.cleanupPlan(ctx, i.Kind, i.TitleID)
	if errors.Is(err, store.ErrNotFound) {
		return s.Store.DropCleanupItem(ctx, i.ID)
	}
	if err != nil {
		return err
	}
	again := time.Now().Add(time.Duration(rule.GraceDays) * 24 * time.Hour)
	if p.Problem != "" {
		s.Store.RelistCleanupItem(ctx, i.ID, i.Files, i.Size, i.LeavesAt, p.Problem)
		return errorf(http.StatusConflict, "Nothing was deleted: "+p.Problem+".")
	}
	if !p.Matches(files, size) {
		s.Store.RelistCleanupItem(ctx, i.ID, p.Count, p.Size, again, "")
		return errorf(http.StatusConflict, fmt.Sprintf("Nothing was deleted: what's on disk changed since it was listed (now %d files, %s). The list shows it now; take another look first.", p.Count, library.Bytes(p.Size)))
	}
	if err := s.carryOutShown(ctx, p, title, files, size, why); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			s.Store.RelistCleanupItem(ctx, i.ID, i.Files, i.Size, i.LeavesAt, he.msg)
		}
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if i.Kind == "movie" {
		err = s.Store.DeleteMovie(ctx, i.TitleID)
	} else {
		err = s.Store.DeleteSeries(ctx, i.TitleID)
	}
	if err != nil {
		return err
	}
	s.cancelDownloads(ctx, i.Kind, []int64{i.TitleID}, title)
	return s.Store.DropCleanupItem(ctx, i.ID)
}

// ---- the page ----

type cleanupRuleView struct {
	store.CleanupRule
	// Leaving is how many titles it has listed, and LeavingSize their bytes.
	Leaving     int   `json:"leaving"`
	LeavingSize int64 `json:"leavingSize"`
}

type cleanupItemView struct {
	store.CleanupItem
	// About is the title, and what was watched of it.
	About cleanupTitle `json:"about"`
	// Auto says its rule deletes it by itself once LeavesAt comes.
	Auto bool `json:"auto"`
}

type cleanupKeptView struct {
	Kind    string    `json:"kind"`
	TitleID int64     `json:"titleId"`
	Title   string    `json:"title"`
	Year    int       `json:"year,omitempty"`
	KeptAt  time.Time `json:"keptAt"`
}

type cleanupReport struct {
	store.WatchReport
	// Fresh says it's recent enough for rules to run.
	Fresh bool `json:"fresh"`
}

type cleanupView struct {
	Rules []cleanupRuleView `json:"rules"`
	Items []cleanupItemView `json:"items"`
	Kept  []cleanupKeptView `json:"kept"`
	// Reports is Matinee's last report of what was watched, for movie and
	// for series; one is missing until Matinee has sent it.
	Reports map[string]cleanupReport `json:"reports"`
	// DailyCap is how many titles a day rules delete by themselves, and
	// DeletedToday how many they have today.
	DailyCap     int `json:"dailyCap"`
	DeletedToday int `json:"deletedToday"`
}

func (s *Server) getCleanup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now()
	out := cleanupView{Rules: []cleanupRuleView{}, Items: []cleanupItemView{}, Kept: []cleanupKeptView{}, Reports: map[string]cleanupReport{}}
	rules, err := s.Store.CleanupRules(ctx)
	if err != nil {
		return err
	}
	items, err := s.Store.CleanupItems(ctx)
	if err != nil {
		return err
	}
	byRule := map[int64]*cleanupRuleView{}
	for _, rule := range rules {
		out.Rules = append(out.Rules, cleanupRuleView{CleanupRule: rule})
	}
	for i := range out.Rules {
		byRule[out.Rules[i].ID] = &out.Rules[i]
	}
	titles := map[string]map[int64]cleanupTitle{}
	for _, kind := range []string{"movie", "series"} {
		if report, ok, err := s.Store.LastWatchReport(ctx, kind); err != nil {
			return err
		} else if ok {
			out.Reports[kind] = cleanupReport{report, now.Sub(report.At) <= cleanupReportAge}
		}
		list, err := s.cleanupTitles(ctx, kind)
		if err != nil {
			return err
		}
		watched, err := s.Store.WatchedTitles(ctx, kind)
		if err != nil {
			return err
		}
		titles[kind] = map[int64]cleanupTitle{}
		for _, t := range list {
			w := watched[t.TitleID]
			t.Viewers, t.FinishedBy, t.LastPlayed = w.Viewers, w.FinishedBy, w.LastPlayed
			titles[kind][t.TitleID] = t
		}
		kept, err := s.Store.CleanupKept(ctx, kind)
		if err != nil {
			return err
		}
		for id, at := range kept {
			// One whose title has gone since isn't worth showing.
			if t, ok := titles[kind][id]; ok {
				out.Kept = append(out.Kept, cleanupKeptView{kind, id, t.Title, t.Year, at})
			}
		}
	}
	slices.SortFunc(out.Kept, func(a, b cleanupKeptView) int { return strings.Compare(a.Title, b.Title) })
	for _, i := range items {
		t, ok := titles[i.Kind][i.TitleID]
		rule := byRule[i.RuleID]
		if !ok || rule == nil {
			continue
		}
		rule.Leaving++
		rule.LeavingSize += i.Size
		out.Items = append(out.Items, cleanupItemView{i, t, rule.Auto})
	}
	if out.DailyCap, err = s.cleanupCap(ctx); err != nil {
		return err
	}
	if out.DeletedToday, err = s.cleanupToday(ctx, now); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) putCleanupSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		DailyCap int `json:"dailyCap"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.DailyCap < 1 || in.DailyCap > 1000 {
		return errorf(http.StatusBadRequest, "Pick how many titles a day rules may delete by themselves, from 1 to 1000.")
	}
	if err := s.Store.SetSetting(r.Context(), settingCleanupCap, strconv.Itoa(in.DailyCap)); err != nil {
		return err
	}
	return s.getCleanup(w, r)
}

// checkCleanupRule says what's wrong with a rule as typed, and fills in
// what was left out.
func checkCleanupRule(in *store.CleanupRule) error {
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) > 100 {
		return errorf(http.StatusBadRequest, "Give the rule a shorter name.")
	}
	if in.Kind != "movie" && in.Kind != "series" {
		return errorf(http.StatusBadRequest, "A rule is for movies (movie) or shows (series).")
	}
	if !slices.Contains([]string{store.CleanupEveryone, store.CleanupAnyone, store.CleanupNobody}, in.Watched) {
		return errorf(http.StatusBadRequest, "A rule goes by everyone, anyone or nobody having watched a title.")
	}
	least := 1
	if in.Watched == store.CleanupNobody {
		least = 30 // a title nobody has got to yet isn't one nobody wants
	}
	if in.Days < least || in.Days > 3650 {
		return errorf(http.StatusBadRequest, fmt.Sprintf("Pick how many quiet days, from %d to 3650.", least))
	}
	if in.GraceDays == 0 {
		in.GraceDays = 14
	}
	least = 1
	if in.Auto {
		least = 7 // time to notice before anything deletes itself
	}
	if in.GraceDays < least || in.GraceDays > 365 {
		return errorf(http.StatusBadRequest, fmt.Sprintf("Pick how many days titles are listed as leaving soon, from %d to 365.", least))
	}
	if in.Kind == "movie" {
		in.EndedOnly = false
	}
	return nil
}

type cleanupPreview struct {
	// Waiting says why the rule wouldn't run now (see store.CleanupNoReport).
	Waiting string `json:"waiting,omitempty"`
	Count   int    `json:"count"`
	Size    int64  `json:"size"`
	// Titles names the biggest, up to cleanupPreviewMost.
	Titles []cleanupTitle `json:"titles"`
}

// previewCleanup is POST /api/v1/cleanup/preview: what a rule would list
// now. It changes nothing.
func (s *Server) previewCleanup(w http.ResponseWriter, r *http.Request) error {
	var in store.CleanupRule
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := checkCleanupRule(&in); err != nil {
		return err
	}
	matches, waiting, err := s.cleanupMatches(r.Context(), in, time.Now())
	if err != nil {
		return err
	}
	out := cleanupPreview{Waiting: waiting, Count: len(matches), Titles: []cleanupTitle{}}
	for i, m := range matches {
		out.Size += m.Size
		if i < cleanupPreviewMost {
			out.Titles = append(out.Titles, m)
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) checkCleanup(w http.ResponseWriter, r *http.Request) error {
	if err := s.Cleanup(r.Context()); err != nil {
		return err
	}
	return s.getCleanup(w, r)
}

func (s *Server) addCleanupRule(w http.ResponseWriter, r *http.Request) error {
	var in store.CleanupRule
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := checkCleanupRule(&in); err != nil {
		return err
	}
	in.Enabled = true
	rule, err := s.Store.AddCleanupRule(r.Context(), in)
	if err != nil {
		return err
	}
	s.Log.Info("added a clean-up rule", "rule", rule.ID, "kind", rule.Kind, "watched", rule.Watched, "days", rule.Days, "auto", rule.Auto)
	s.cleanupSoon(r.Context())
	return s.getCleanup(w, r)
}

func (s *Server) saveCleanupRule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	old, err := s.Store.CleanupRule(r.Context(), id)
	if err != nil {
		return err
	}
	in := old
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.ID = id
	if err := checkCleanupRule(&in); err != nil {
		return err
	}
	s.cleanupMu.Lock()
	err = s.Store.UpdateCleanupRule(r.Context(), in)
	s.cleanupMu.Unlock()
	if err != nil {
		return err
	}
	s.Log.Info("changed a clean-up rule", "rule", id, "kind", in.Kind, "watched", in.Watched, "days", in.Days, "auto", in.Auto, "enabled", in.Enabled)
	s.cleanupSoon(r.Context())
	return s.getCleanup(w, r)
}

func (s *Server) deleteCleanupRule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s.cleanupMu.Lock()
	err = s.Store.DeleteCleanupRule(r.Context(), id)
	s.cleanupMu.Unlock()
	if err != nil {
		return err
	}
	return s.getCleanup(w, r)
}

// keepCleanupItem is POST /api/v1/cleanup/items/{id}/keep: the title
// stays, and no rule lists it again.
func (s *Server) keepCleanupItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	i, err := s.Store.CleanupItem(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.Store.KeepFromCleanup(r.Context(), i.Kind, i.TitleID); err != nil {
		return err
	}
	return s.getCleanup(w, r)
}

// deleteCleanupItem is DELETE /api/v1/cleanup/items/{id}: an admin deletes
// a listed title now. Like every deletion it must say the files and bytes
// the admin was shown (expectFiles, expectSize).
func (s *Server) deleteCleanupItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	files, size, err := shown(r)
	if err != nil {
		return err
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	i, err := s.Store.CleanupItem(r.Context(), id)
	if err != nil {
		return err
	}
	rule, err := s.Store.CleanupRule(r.Context(), i.RuleID)
	if err != nil {
		return err
	}
	if err := s.deleteCleanup(r.Context(), i, rule, files, size, "It was on the leaving soon list."); err != nil {
		return err
	}
	return s.getCleanup(w, r)
}

func (s *Server) unkeepCleanup(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	kind := r.PathValue("kind")
	if kind != "movie" && kind != "series" {
		return errorf(http.StatusNotFound, "No such kind of title.")
	}
	if err := s.Store.UnkeepFromCleanup(r.Context(), kind, id); err != nil {
		return err
	}
	return s.getCleanup(w, r)
}
