package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Rules for new titles: "animation from Japan goes to /media/anime", "kids'
// shows go to /media/kids with the HD 720p profile". A rule picks the root
// folder, the quality profile or both for a title nobody chose them for:
// one a member asked for, one from a followed list, one Matinee asked for.
// In Box Office's own Add dialog the rule's choice is filled in and can be
// changed.

// SettingAddRules holds the rules, as JSON, in the order they're tried.
const SettingAddRules = "add_rules"

// MaxAddRules is how many rules there can be.
const MaxAddRules = 50

// Rule sends new titles of some genres, or first made in some language,
// to a root folder and a quality profile.
type Rule struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Kind is "movie" or "series": root folders are for one or the other.
	Kind string `json:"kind"`
	// Genres are TMDB genre ids; a title with any of them fits. Empty
	// fits every genre.
	Genres []int64 `json:"genres"`
	// Language is the language a title was first made in, as TMDB gives
	// it ("ja"); empty fits every language.
	Language string `json:"language"`
	// RootFolder and ProfileID are what the rule picks; either may be
	// left out, to keep what would have been used anyway.
	RootFolder string `json:"rootFolder"`
	ProfileID  int    `json:"profileId"`
}

// fits says whether a title of these genres and language is one the rule
// is for.
func (r Rule) fits(kind string, genres []tmdb.Genre, language string) bool {
	if r.Kind != kind {
		return false
	}
	if r.Language != "" && !strings.EqualFold(r.Language, language) {
		return false
	}
	return len(r.Genres) == 0 || slices.ContainsFunc(genres, func(g tmdb.Genre) bool { return slices.Contains(r.Genres, g.ID) })
}

// AddRules lists the rules in the order they're tried.
func (s *Service) AddRules(ctx context.Context) ([]Rule, error) {
	v, err := s.Store.Setting(ctx, SettingAddRules)
	if err != nil {
		return nil, err
	}
	rules := []Rule{}
	if v == "" {
		return rules, nil
	}
	if err := json.Unmarshal([]byte(v), &rules); err != nil {
		// Rules that can't be read are no rules: titles go where they
		// would without any.
		s.Log.Warn("the rules for new titles can't be read", "err", err)
		return []Rule{}, nil
	}
	return rules, nil
}

// SaveAddRules replaces the rules, after checking each one.
func (s *Service) SaveAddRules(ctx context.Context, rules []Rule) ([]Rule, error) {
	if len(rules) > MaxAddRules {
		return nil, badf("that's more than %d rules", MaxAddRules)
	}
	profiles, err := s.Store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	var next int64
	for _, r := range rules {
		next = max(next, r.ID)
	}
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		r.Name, r.Language = strings.TrimSpace(r.Name), strings.ToLower(strings.TrimSpace(r.Language))
		if r.Genres == nil {
			r.Genres = []int64{}
		}
		switch {
		case r.Name == "":
			return nil, badf("give the rule a name")
		case len(r.Name) > 80:
			return nil, badf("the rule's name is too long")
		case r.Kind != "movie" && r.Kind != "series":
			return nil, badf("%s: say whether it's for movies or shows", r.Name)
		case len(r.Genres) == 0 && r.Language == "":
			return nil, badf("%s: pick at least one genre or a language, or it would take every title", r.Name)
		case len(r.Language) > 3:
			return nil, badf("%s: %q isn't a language code", r.Name, r.Language)
		case r.RootFolder == "" && r.ProfileID == 0:
			return nil, badf("%s: pick a folder or a quality profile for it to use", r.Name)
		}
		if r.RootFolder != "" {
			r.RootFolder = filepath.Clean(r.RootFolder)
			folders, err := s.Store.RootFolders(ctx, r.Kind)
			if err != nil {
				return nil, err
			}
			if !slices.ContainsFunc(folders, func(f store.RootFolder) bool { return f.Path == r.RootFolder }) {
				return nil, badf("%s: %s isn't one of the root folders for %s", r.Name, r.RootFolder, plural(r.Kind))
			}
		}
		if r.ProfileID != 0 {
			i := slices.IndexFunc(profiles, func(p quality.Profile) bool { return p.ID == r.ProfileID })
			switch {
			case i < 0:
				return nil, badf("%s: there's no quality profile %d", r.Name, r.ProfileID)
			case profiles[i].Locked:
				return nil, badf("%s: Do not touch is for titles Box Office leaves alone, so a rule can't put new ones on it", r.Name)
			}
		}
		if r.ID <= 0 {
			next++
			r.ID = next
		}
		out = append(out, r)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return out, s.Store.SetSetting(ctx, SettingAddRules, string(b))
}

// RuleFor is the first rule that fits a title, with only what it can still
// pick: a folder that's no longer a root folder, or a profile that's gone,
// is left out. ok is false when no rule picks anything.
func (s *Service) RuleFor(ctx context.Context, kind string, genres []tmdb.Genre, language string) (Rule, bool, error) {
	rules, err := s.AddRules(ctx)
	if err != nil || len(rules) == 0 {
		return Rule{}, false, err
	}
	for _, r := range rules {
		if !r.fits(kind, genres, language) {
			continue
		}
		if r.RootFolder != "" {
			folders, err := s.Store.RootFolders(ctx, kind)
			if err != nil {
				return Rule{}, false, err
			}
			if !slices.ContainsFunc(folders, func(f store.RootFolder) bool { return f.Path == r.RootFolder }) {
				s.Log.Warn("a rule's folder isn't a root folder any more", "rule", r.Name, "folder", r.RootFolder)
				r.RootFolder = ""
			}
		}
		if r.ProfileID != 0 {
			if p, err := s.Store.Profile(ctx, r.ProfileID); err != nil || p.Locked {
				s.Log.Warn("a rule's quality profile is gone", "rule", r.Name)
				r.ProfileID = 0
			}
		}
		if r.RootFolder == "" && r.ProfileID == 0 {
			continue
		}
		return r, true, nil
	}
	return Rule{}, false, nil
}

// applyRule lets the first rule that fits pick the folder and profile of a
// title being added, unless a person chose them or its files are already
// somewhere.
func (s *Service) applyRule(ctx context.Context, kind string, genres []tmdb.Genre, language string, o *AddOptions) error {
	if o.Chosen || o.Path != "" {
		return nil
	}
	r, ok, err := s.RuleFor(ctx, kind, genres, language)
	if err != nil || !ok {
		return err
	}
	if r.RootFolder != "" {
		o.RootFolder = r.RootFolder
	}
	if r.ProfileID != 0 {
		o.ProfileID = r.ProfileID
	}
	s.Log.Info("a rule picked where a new title goes", "rule", r.Name, "folder", o.RootFolder)
	return nil
}

// RuleForTitle is the rule that would pick where a title goes, looking the
// title up on TMDB. With no rules, TMDB isn't asked.
func (s *Service) RuleForTitle(ctx context.Context, kind string, tmdbID int64) (Rule, bool, error) {
	if rules, err := s.AddRules(ctx); err != nil || !slices.ContainsFunc(rules, func(r Rule) bool { return r.Kind == kind }) {
		return Rule{}, false, err
	}
	if kind == "movie" {
		m, err := s.TMDB(ctx).Movie(ctx, tmdbID)
		if err != nil {
			return Rule{}, false, err
		}
		return s.RuleFor(ctx, kind, m.Genres, m.Language)
	}
	t, err := s.TMDB(ctx).Show(ctx, tmdbID)
	if err != nil {
		return Rule{}, false, err
	}
	return s.RuleFor(ctx, kind, t.Genres, t.Language)
}

// Genres lists TMDB's genres for movies and for shows, kept a day.
func (s *Service) Genres(ctx context.Context) (movies, shows []tmdb.Genre, err error) {
	b, err := s.browse(ctx)
	return b.MovieGenres, b.SeriesGenres, err
}
