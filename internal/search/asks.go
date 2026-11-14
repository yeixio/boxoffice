package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// JobSearchAsked searches for what someone asked for in Matinee: another
// edition of a movie, or a better or replacement copy of a movie or an
// episode. Its payload is an AskedJob.
const JobSearchAsked = "search-asked"

// Edition modes (see store.ModeReplace and store.ModeAlongside).
const (
	ModeReplace   = store.ModeReplace
	ModeAlongside = store.ModeAlongside
)

// Ask is what a search is for, beyond the title's profile.
type Ask struct {
	// Edition is the movie edition wanted, and Mode how it goes in.
	Edition string `json:"edition,omitempty"`
	Mode    string `json:"mode,omitempty"`
	// Want is store.WantBetter or store.WantReplace, or "".
	Want string `json:"want,omitempty"`
	// Language is the language (ISO 639-1) a replacement's sound must be
	// in, for a copy reported in the wrong language; "" for any.
	Language string `json:"language,omitempty"`
}

// with fills in from what was asked before and is still wanted: the
// edition the movie's copy should be, and a better or replacement copy.
func (a Ask) with(tw store.TitleWant) Ask {
	if a.Want == "" {
		a.Want = tw.Want
	}
	if a.Language == "" && a.Want == store.WantReplace && tw.Want == store.WantReplace {
		a.Language = tw.Language
	}
	if a.Edition == "" && tw.Edition != "" {
		// The copy there should be this edition; a better copy of it
		// replaces that one.
		a.Edition = tw.Edition
	}
	return a
}

// profile is the profile to judge releases by, and whether the title
// counts as having a copy already. Another edition alongside, or one to
// replace a different edition, doesn't compete with the copy there; a bad
// copy counts as none; a better copy is looked for past the cutoff.
func (a Ask) profile(p quality.Profile, has bool) (quality.Profile, bool) {
	switch {
	case a.Mode == ModeAlongside, a.Mode == ModeReplace:
		has = false
	case a.Want == store.WantReplace:
		has = false
	case a.Want == store.WantBetter && !p.Locked && len(p.Allowed) > 0:
		best := p.Allowed[0]
		for _, q := range p.Allowed {
			if quality.Rank(q) > quality.Rank(best) {
				best = q
			}
		}
		p.Cutoff, p.UpgradeOK = best, true
	}
	return p, has
}

// judgeLanguage turns down a release whose name says its sound is only in
// other languages than the one asked for, and puts first those that say
// it's in that one.
func (a Ask) judgeLanguage(c *Candidate) {
	if a.Language == "" {
		return
	}
	ok, named := quality.ReleaseInLanguage(c.Title, a.Language)
	if !ok {
		langs, _ := quality.ReleaseLanguages(c.Title)
		names := make([]string, len(langs))
		for i, l := range langs {
			names[i] = quality.LanguageName(l)
		}
		c.reject(fmt.Sprintf("its name says it's in %s, and %s was asked for", strings.Join(names, " and "), quality.LanguageName(a.Language)))
	}
	if named {
		c.langRank = 1
	}
}

// judgeReplacement keeps a replacement for a bad copy near that copy's
// quality (have), when Settings say so (on): a release with a bigger
// picture, or a remux for a copy that wasn't one, is turned down, and the
// copy's own quality is put first, then its picture size, then smaller
// ones. Better copies and other editions aren't held to it.
func (a Ask) judgeReplacement(c *Candidate, have int, on bool) {
	if !on || a.Want != store.WantReplace || a.Mode != "" {
		return
	}
	fit, why := quality.ReplacementFit(have, c.Quality)
	if fit == quality.FitNone {
		c.reject(why)
		return
	}
	c.fitRank = fit
}

// replaceSameQuality says whether replacements keep the quality of the
// copy they replace (SettingReplaceSameQuality): on unless turned off.
func (s *Service) replaceSameQuality(ctx context.Context) bool {
	v, err := s.Store.Setting(ctx, SettingReplaceSameQuality)
	return err != nil || v != "off"
}

// AskedJob is JobSearchAsked's payload.
type AskedJob struct {
	Kind string `json:"kind"` // movie or episode
	ID   int64  `json:"id"`
	Ask
}

// Payload is the job's payload.
func (j AskedJob) Payload() string {
	b, _ := json.Marshal(j)
	return string(b)
}

func (s *Service) jobAsked(ctx context.Context, payload string) (jobs.Result, error) {
	var j AskedJob
	if err := json.Unmarshal([]byte(payload), &j); err != nil || j.ID == 0 {
		return jobs.Result{}, errors.New("the job doesn't say which title")
	}
	// Someone asked for this copy in Matinee: it's theirs, like a grab by
	// hand.
	ctx = byHand(ctx)
	if j.Want == store.WantReplace {
		// A copy reported as bad: it counts toward the day's
		// replacements, as reports can come in bursts.
		ctx = reported(ctx)
	}
	var res Results
	var err error
	switch j.Kind {
	case "movie":
		res, err = s.MovieFor(ctx, j.ID, j.Ask)
	case "episode":
		res, err = s.EpisodeFor(ctx, j.ID, j.Ask)
	default:
		return jobs.Result{}, fmt.Errorf("can't search for a %s", j.Kind)
	}
	if errors.Is(err, ErrNoIndexers) || errors.Is(err, store.ErrLocked) {
		return jobs.Skipped("%s", err.Error()), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	if j.Kind == "movie" {
		return s.pick(ctx, res, "movie", j.ID, 0)
	}
	e, err := s.Store.Episode(ctx, j.ID)
	if err != nil {
		return jobs.Result{}, err
	}
	return s.pick(ctx, res, "series", e.SeriesID, e.ID)
}

// EditionOption is one edition of a movie found on the indexers.
type EditionOption struct {
	Edition  string `json:"edition"`
	Releases int    `json:"releases"` // how many releases of it can be taken
	Quality  string `json:"quality,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Owned    bool   `json:"owned,omitempty"`
}

// MovieEditions searches for a movie and says which editions of it can be
// had, best copy of each first, and which the library has.
func (s *Service) MovieEditions(ctx context.Context, id int64, owned []string) ([]EditionOption, []string, error) {
	res, err := s.MovieFor(ctx, id, Ask{Mode: ModeAlongside})
	if err != nil {
		return nil, nil, err
	}
	out := []EditionOption{}
	idx := map[string]int{}
	for _, c := range res.Releases {
		if c.Edition == "" || !c.Accepted {
			continue
		}
		i, ok := idx[c.Edition]
		if !ok {
			q, _ := quality.ByID(c.Quality)
			out = append(out, EditionOption{Edition: c.Edition, Quality: q.Name, Size: c.Size})
			i = len(out) - 1
			idx[c.Edition] = i
		}
		out[i].Releases++
	}
	for _, o := range owned {
		i := slices.IndexFunc(out, func(e EditionOption) bool { return quality.SameEdition(e.Edition, o) })
		if i < 0 {
			out = append(out, EditionOption{Edition: o, Owned: true})
		} else {
			out[i].Owned = true
		}
	}
	return out, res.Errors, nil
}
