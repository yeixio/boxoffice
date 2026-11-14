// Package subtitles fetches subtitles for movies and episodes in the
// languages chosen in Settings → Subtitles, as Bazarr did: for each file
// as it's downloaded, and slowly through the rest of the library.
//
// It looks inside each file first (with ffprobe) and beside it, so a
// language a file already has is never fetched, then asks the providers
// (Podnapisi, which needs no account, and OpenSubtitles with an API key)
// and saves the best match beside the video as "Movie.en.srt", which
// Matinee and every other player pick up. One that isn't found is looked
// for again later, more often while the file is new, since subtitles for
// new releases turn up in the days after. Nothing is ever deleted or
// replaced, and titles on the Do not touch profile are left alone.
package subtitles

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Timings: the first pass waits a little after a start, then new files
// are looked up every few minutes, with a pause between files so
// providers aren't hammered.
var (
	firstWait    = 3 * time.Minute
	every        = 15 * time.Minute
	pause        = 5 * time.Second
	probeTimeout = time.Minute
)

// maxTries is how many of a language's best matches are tried before
// it's taken as not found this time.
const maxTries = 3

// Service fetches subtitles in the background.
type Service struct {
	Store     *store.Store
	Log       *slog.Logger
	UserAgent string // "BoxOffice/1.2.3"
	// Providers makes the providers for the settings; nil makes
	// Podnapisi and OpenSubtitles as they're set up.
	Providers func(Config) []Provider
	// Probe lists the languages of the subtitles inside a video; ffprobe
	// when nil.
	Probe func(ctx context.Context, path string) ([]string, error)
	// Now is the time; time.Now when nil.
	Now func() time.Time

	mu      sync.Mutex
	running bool
	done    int
	lastErr string
	wake    chan struct{}
	// urgent are files to look up at once, by hand or just downloaded,
	// whatever the day's limit.
	urgent map[int64]bool
	// usedUp is the UTC day each provider's downloads ran out, as
	// OpenSubtitles counts them.
	usedUp map[string]string
	os     *OpenSubtitles
	osKey  OpenSubtitlesLogin
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) wakeCh() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

// Wake starts a pass now, or right after the one that's running.
func (s *Service) Wake() {
	select {
	case s.wakeCh() <- struct{}{}:
	default:
	}
}

// FindNow looks up files' missing subtitles at once, whatever the day's
// limit and when they were last looked for.
func (s *Service) FindNow(ctx context.Context, fileIDs ...int64) error {
	if len(fileIDs) == 0 {
		return nil
	}
	if err := s.Store.RetrySubtitles(ctx, fileIDs...); err != nil {
		return err
	}
	s.mu.Lock()
	if s.urgent == nil {
		s.urgent = map[int64]bool{}
	}
	for _, id := range fileIDs {
		s.urgent[id] = true
	}
	s.mu.Unlock()
	s.Wake()
	return nil
}

// Imported is for a download's Imported hook: a new movie or episode file
// gets its subtitles straight away.
func (s *Service) Imported(ctx context.Context, d store.Download) {
	if d.ImportedTo == "" || (d.Kind != "movie" && d.Kind != "series") {
		return
	}
	c, err := LoadConfig(ctx, s.Store)
	if err != nil || !c.On {
		return
	}
	files, err := s.Store.SubtitleFiles(ctx)
	if err != nil {
		return
	}
	var ids []int64
	for _, f := range files {
		pack := d.Kind == "series" && f.Kind == "episode" && (slices.Contains(d.EpisodeIDs, f.OwnerID) || filepath.Dir(f.Path) == d.ImportedTo)
		if f.Path == d.ImportedTo || pack {
			ids = append(ids, f.ID)
		}
	}
	s.FindNow(ctx, ids...)
}

// Reported looks for a title's missing subtitles at once, for a report
// in Matinee that they're missing or wrong: a movie's (kind movie), or a
// show's, its season's or its episode's (0 for all).
func (s *Service) Reported(ctx context.Context, kind string, tmdbID int64, season, episode int) {
	var files []store.SubtitleFile
	switch kind {
	case "movie":
		m, err := s.Store.MovieByTMDB(ctx, tmdbID)
		if err != nil {
			return
		}
		files, _ = s.Store.SubtitleFilesOf(ctx, "movie", m.ID)
	default:
		t, err := s.Store.SeriesByTMDB(ctx, tmdbID)
		if err != nil {
			return
		}
		all, _ := s.Store.SubtitleFilesOf(ctx, "series", t.ID)
		for _, f := range all {
			if (season == 0 || f.Season == season) && (episode == 0 || f.Episode == episode) {
				files = append(files, f)
			}
		}
	}
	var ids []int64
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	if c, err := LoadConfig(ctx, s.Store); err == nil && c.On {
		s.FindNow(ctx, ids...)
	}
}

// Run looks up new files every few minutes until ctx ends.
func (s *Service) Run(ctx context.Context) {
	wake := s.wakeCh()
	t := time.NewTimer(firstWait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		if err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("subtitles: pass failed", "err", err)
		}
		t.Reset(every)
	}
}

// Pass looks up every file that's due, new downloads first, until the
// day's limit.
func (s *Service) Pass(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running, s.done, s.lastErr = true, 0, ""
	s.mu.Unlock()
	err := s.pass(ctx)
	s.mu.Lock()
	s.running = false
	if err != nil {
		s.lastErr = err.Error()
	}
	s.mu.Unlock()
	return err
}

// job is a file and the languages it needs looked up.
type job struct {
	f      store.SubtitleFile
	langs  []string
	fresh  bool // never looked at in one of them
	urgent bool
	due    time.Time
}

func (s *Service) pass(ctx context.Context) error {
	c, err := LoadConfig(ctx, s.Store)
	if err != nil {
		return err
	}
	if !c.On || len(c.Languages) == 0 {
		return nil
	}
	providers := s.providers(c)
	if len(providers) == 0 {
		return errors.New("there's nowhere to look: turn on Podnapisi or enter an OpenSubtitles API key")
	}
	if err := s.Store.PruneSubtitles(ctx); err != nil {
		return err
	}
	jobs, err := s.due(ctx, c)
	if err != nil {
		return err
	}
	use, err := s.Store.SubtitleUseOn(ctx, store.DataDay(s.now()))
	if err != nil {
		return err
	}
	left := c.DailyLimit - use[""].Searches
	for i, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !j.urgent && left <= 0 {
			break
		}
		if i > 0 && pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
		if err := s.look(ctx, c, providers, j); err != nil {
			return err
		}
		s.Store.AddSubtitleUse(ctx, "", 1, 0)
		left--
		s.mu.Lock()
		s.done++
		delete(s.urgent, j.f.ID)
		s.mu.Unlock()
	}
	return nil
}

// due lists the files to look up, in order: those asked for now, then
// those never looked at, newest first, then missing ones whose time has
// come, longest waiting first.
func (s *Service) due(ctx context.Context, c Config) ([]job, error) {
	files, err := s.Store.SubtitleFiles(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.Subtitles(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	urgent := map[int64]bool{}
	for id := range s.urgent {
		urgent[id] = true
	}
	s.mu.Unlock()
	now := s.now()
	var jobs []job
	for _, f := range files {
		if f.Locked {
			continue
		}
		j := job{f: f, urgent: urgent[f.ID]}
		have := map[string]store.Subtitle{}
		for _, r := range rows[f.ID] {
			have[r.Language] = r
		}
		for _, lang := range c.Languages {
			r, ok := have[lang]
			switch {
			case !ok:
				j.langs, j.fresh = append(j.langs, lang), true
			case store.SubtitleHas(r.State):
			case j.urgent || !r.NextAt.After(now):
				j.langs = append(j.langs, lang)
				if j.due.IsZero() || r.NextAt.Before(j.due) {
					j.due = r.NextAt
				}
			}
		}
		if len(j.langs) > 0 {
			jobs = append(jobs, j)
		}
	}
	slices.SortStableFunc(jobs, func(a, b job) int {
		if a.urgent != b.urgent {
			return first(a.urgent)
		}
		if a.fresh != b.fresh {
			return first(a.fresh)
		}
		if a.fresh {
			return cmp.Or(b.f.AddedAt.Compare(a.f.AddedAt), cmp.Compare(b.f.ID, a.f.ID))
		}
		return cmp.Or(a.due.Compare(b.due), cmp.Compare(a.f.ID, b.f.ID))
	})
	return jobs, nil
}

func first(a bool) int {
	if a {
		return -1
	}
	return 1
}

// look finds one file's subtitles in the languages it needs.
func (s *Service) look(ctx context.Context, c Config, providers []Provider, j job) error {
	f := j.f
	if _, err := os.Stat(f.Path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.Log.Debug("subtitles: can't reach the file for now", "path", f.Path, "err", err)
		}
		return nil // gone (the library will notice) or a share that's not answering
	}
	now := s.now()
	known, err := s.Store.SubtitlesOf(ctx, f.ID)
	if err != nil {
		return err
	}
	prev := map[string]store.Subtitle{}
	for _, r := range known[f.ID] {
		prev[r.Language] = r
	}
	save := func(x store.Subtitle) error {
		x.FileID, x.CheckedAt = f.ID, now
		return s.Store.SaveSubtitle(ctx, x)
	}

	// What it has already: inside it (looked at the first time only, as
	// that doesn't change), and beside it.
	inside := map[string]bool{}
	if j.fresh {
		if langs, err := s.probe(ctx, f.Path); err == nil {
			for _, l := range langs {
				inside[l] = true
			}
		} else {
			s.Log.Debug("subtitles: couldn't look inside", "path", f.Path, "err", err)
		}
	}
	beside, _ := OnDisk(f.Path)
	var need []string
	for _, lang := range j.langs {
		switch {
		case inside[lang]:
			if err := save(store.Subtitle{Language: lang, State: store.SubEmbedded}); err != nil {
				return err
			}
		case beside[lang]:
			if err := save(store.Subtitle{Language: lang, State: store.SubOnDisk}); err != nil {
				return err
			}
		default:
			need = append(need, lang)
		}
	}
	if len(need) == 0 {
		return nil
	}

	q := Query{Movie: f.Kind != "episode", TMDBID: f.TMDBID, IMDBID: f.IMDBID, Title: f.Title, Year: f.Year,
		Season: f.Season, Episode: f.Episode, Languages: need,
		Release: s.Store.ReleaseOf(ctx, store.File{ID: f.ID, Kind: f.Kind, OwnerID: f.OwnerID, Path: f.Path})}
	for _, p := range providers {
		if p.Name() == "opensubtitles" {
			q.Hash, _ = FileHash(f.Path)
		}
	}
	var found []Found
	var failed []error
	searched := 0
	for _, p := range providers {
		if s.isUsedUp(p.Name()) {
			continue
		}
		res, err := p.Search(ctx, q)
		s.Store.AddSubtitleUse(ctx, p.Name(), 1, 0)
		searched++
		found = append(found, res...)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.Log.Warn("subtitles: search failed", "provider", p.Name(), "title", f.Title, "err", err)
			if !errors.Is(err, ErrBadKey) && !errors.Is(err, ErrBadLogin) {
				err = fmt.Errorf("%s couldn't be reached", Title(p.Name()))
			}
			failed = append(failed, err)
		}
	}
	byName := map[string]Provider{}
	for _, p := range providers {
		byName[p.Name()] = p
	}
	for _, lang := range need {
		ranked := Rank(found, lang, q.Release, c.PreferSDH)
		blocked, done := false, false
		tries := 0
		for _, cand := range ranked {
			if tries >= maxTries {
				break
			}
			if s.isUsedUp(cand.Provider) {
				blocked = true
				continue
			}
			tries++
			data, name, err := byName[cand.Provider].Download(ctx, cand)
			if errors.Is(err, ErrQuota) {
				s.useUp(cand.Provider)
				blocked = true
				tries--
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.Log.Warn("subtitles: download failed", "provider", cand.Provider, "title", f.Title, "err", err)
				continue
			}
			s.Store.AddSubtitleUse(ctx, cand.Provider, 0, 1)
			path, err := Save(f.Path, lang, cand.SDH, name, data)
			if err != nil {
				s.Log.Warn("subtitles: couldn't save", "path", f.Path, "language", lang, "err", err)
				continue
			}
			s.Log.Info("subtitles: fetched", "title", f.Title, "language", lang, "provider", cand.Provider, "path", path)
			if err := save(store.Subtitle{Language: lang, State: store.SubFetched, Provider: cand.Provider, Path: path,
				Release: cand.Release, MatchesFile: cand.MatchesFile, SDH: cand.SDH}); err != nil {
				return err
			}
			done = true
			break
		}
		if done {
			continue
		}
		p := prev[lang]
		switch {
		case blocked:
			// Found, but the provider's downloads are used up for today.
			next := now.UTC().Truncate(24 * time.Hour).Add(24*time.Hour + 10*time.Minute)
			err = save(store.Subtitle{Language: lang, State: store.SubWaiting, Tries: p.Tries, NextAt: next,
				Note: "OpenSubtitles' downloads for today are used up; it'll be fetched tomorrow"})
		case searched == 0 || (len(failed) == searched && len(ranked) == 0):
			// No provider could be asked: not the subtitle's fault.
			note := "the subtitle sites couldn't be reached"
			if len(failed) > 0 {
				note = failed[0].Error()
			}
			err = save(store.Subtitle{Language: lang, State: store.SubWaiting, Tries: p.Tries, NextAt: now.Add(3 * time.Hour), Note: note})
		default:
			tries := p.Tries + 1
			err = save(store.Subtitle{Language: lang, State: store.SubMissing, Tries: tries, NextAt: now.Add(RetryAfter(tries, now.Sub(f.AddedAt)))})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RetryAfter is how long to wait before looking again for a subtitle not
// found tries times, for a file added age ago. Subtitles for a new
// release turn up within days, so a new file is looked at again every few
// hours at first; after that the waits grow to a month.
func RetryAfter(tries int, age time.Duration) time.Duration {
	if age < 14*24*time.Hour && tries <= 8 {
		return 6 * time.Hour
	}
	switch {
	case tries <= 1:
		return 24 * time.Hour
	case tries == 2:
		return 3 * 24 * time.Hour
	case tries == 3:
		return 7 * 24 * time.Hour
	case tries == 4:
		return 14 * 24 * time.Hour
	}
	return 30 * 24 * time.Hour
}

func (s *Service) isUsedUp(provider string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedUp[provider] == s.now().UTC().Format(time.DateOnly)
}

func (s *Service) useUp(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usedUp == nil {
		s.usedUp = map[string]string{}
	}
	if s.usedUp[provider] == "" || s.usedUp[provider] != s.now().UTC().Format(time.DateOnly) {
		s.Log.Info("subtitles: downloads used up for today", "provider", provider)
	}
	s.usedUp[provider] = s.now().UTC().Format(time.DateOnly)
}

// providers are the providers the settings turn on.
func (s *Service) providers(c Config) []Provider {
	if s.Providers != nil {
		return s.Providers(c)
	}
	var out []Provider
	if k := c.OpenSubtitles; k.APIKey != "" {
		out = append(out, s.openSubtitles(k))
	}
	if c.Podnapisi {
		out = append(out, &Podnapisi{UserAgent: s.UserAgent})
	}
	return out
}

// openSubtitles keeps one client while the login stays the same, so it
// signs in twice a day at most.
func (s *Service) openSubtitles(k OpenSubtitlesLogin) *OpenSubtitles {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.os == nil || s.osKey != k {
		s.os, s.osKey = NewOpenSubtitles(k, s.UserAgent), k
	}
	return s.os
}

// Title is how a provider is named to people.
func Title(provider string) string {
	switch provider {
	case "opensubtitles":
		return "OpenSubtitles"
	case "podnapisi":
		return "Podnapisi"
	}
	return provider
}

// NewOpenSubtitles makes a client for a login.
func NewOpenSubtitles(k OpenSubtitlesLogin, ua string) *OpenSubtitles {
	return &OpenSubtitles{APIKey: k.APIKey, Username: k.Username, Password: k.Password, UserAgent: ua}
}

// ---- looking inside files ----

func (s *Service) ffprobe() string {
	p, _ := exec.LookPath("ffprobe")
	return p
}

func (s *Service) probe(ctx context.Context, path string) ([]string, error) {
	if s.Probe != nil {
		return s.Probe(ctx, path)
	}
	tool := s.ffprobe()
	if tool == "" {
		return nil, errors.New("ffprobe isn't installed")
	}
	return Inside(ctx, tool, path)
}

// Inside lists the languages of the subtitles inside a video, with
// ffprobe. Forced ones, which only cover the foreign parts, don't count.
func Inside(ctx context.Context, ffprobe, path string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=index:stream_tags=language,title:stream_disposition=forced", "-of", "json", "-i", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return ParseInside(out)
}

// ParseInside reads ffprobe's list of subtitle streams.
func ParseInside(out []byte) ([]string, error) {
	var r struct {
		Streams []struct {
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	var langs []string
	for _, st := range r.Streams {
		if st.Disposition["forced"] == 1 {
			continue
		}
		var lang, title string
		for k, v := range st.Tags {
			switch {
			case equalFold(k, "language"):
				lang = v
			case equalFold(k, "title"):
				title = v
			}
		}
		if containsFold(title, "forced") {
			continue
		}
		if c := Code(lang); c != "" && !slices.Contains(langs, c) {
			langs = append(langs, c)
		}
	}
	return langs, nil
}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

func containsFold(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }
