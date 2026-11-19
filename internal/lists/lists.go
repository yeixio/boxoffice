// Package lists follows lists kept elsewhere (a TMDB list, a Trakt
// watchlist or list, a Plex watchlist) and adds what's on them to Box
// Office: by itself, or once the admin says yes. A title is handled once:
// one that was removed here isn't added again.
package lists

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// SettingTraktClient is the client ID of the admin's own Trakt API app.
const SettingTraktClient = "trakt_client_id"

const (
	// Every is how often a list is read again.
	Every = 6 * time.Hour
	// PerCheck is the most titles one list adds each time it's read, so a
	// long list doesn't start hundreds of searches and downloads at once.
	// The rest follow at the next checks.
	PerCheck  = 20
	firstWait = 2 * time.Minute
	look      = 15 * time.Minute
)

// Error is a mistake in what was asked, fit to show whoever asked.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Service follows the lists.
type Service struct {
	Store     *store.Store
	Catalog   *catalog.Service
	Log       *slog.Logger
	UserAgent string
	// HTTP, TraktBase and PlexFeed point at test servers.
	HTTP      *http.Client
	TraktBase string
	PlexFeed  func(string) string

	mu sync.Mutex // one check at a time
	// found remembers which TMDB title another site's id is.
	foundMu sync.Mutex
	found   map[string]ref
}

type ref struct {
	Kind string
	ID   int64
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (s *Service) agent() string {
	if s.UserAgent != "" {
		return s.UserAgent
	}
	return "BoxOffice"
}

// Preview is what a list's address turned out to be, before it's followed.
type Preview struct {
	Source string `json:"source"`
	// Address is the address tidied up, as it'll be kept.
	Address string   `json:"address"`
	Name    string   `json:"name"`
	Movies  int      `json:"movies"`
	Shows   int      `json:"shows"`
	Sample  []string `json:"sample"`
	// Capped says the list is longer than Box Office reads.
	Capped bool `json:"capped"`
}

// Look reads the list at an address and says what's on it.
func (s *Service) Look(ctx context.Context, address string) (Preview, error) {
	p, err := Place(address)
	if err != nil {
		return Preview{}, err
	}
	name, entries, err := s.fetch(ctx, p)
	if err != nil {
		return Preview{Source: p.Source}, err
	}
	out := Preview{Source: p.Source, Address: tidy(address), Name: name, Sample: []string{}, Capped: len(entries) >= most}
	for _, e := range entries {
		if e.Kind == tmdb.KindSeries {
			out.Shows++
		} else {
			out.Movies++
		}
		if len(out.Sample) < 8 && e.Title != "" {
			out.Sample = append(out.Sample, e.Title)
		}
	}
	return out, nil
}

func tidy(address string) string {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	return address
}

// Follow starts following a list. It's read for the first time at once, in
// the background.
func (s *Service) Follow(ctx context.Context, l store.List) (store.List, error) {
	p, err := Place(l.Address)
	if err != nil {
		return l, err
	}
	l.Source, l.Address = p.Source, tidy(l.Address)
	if err := s.check(ctx, &l); err != nil {
		return l, err
	}
	have, err := s.Store.Lists(ctx)
	if err != nil {
		return l, err
	}
	for _, h := range have {
		if strings.EqualFold(strings.TrimSuffix(h.Address, "/"), strings.TrimSuffix(l.Address, "/")) {
			return l, badf("That list is followed already, as %s.", h.Name)
		}
	}
	return s.Store.AddList(ctx, l)
}

// Save changes a followed list's settings.
func (s *Service) Save(ctx context.Context, l store.List) (store.List, error) {
	old, err := s.Store.List(ctx, l.ID)
	if err != nil {
		return l, err
	}
	l.Source, l.Address = old.Source, old.Address
	if err := s.check(ctx, &l); err != nil {
		return l, err
	}
	if err := s.Store.UpdateList(ctx, l); err != nil {
		return l, err
	}
	return s.Store.List(ctx, l.ID)
}

// check makes sure a list's settings can be used.
func (s *Service) check(ctx context.Context, l *store.List) error {
	l.Name = strings.TrimSpace(l.Name)
	if l.Name == "" {
		return badf("Give the list a name.")
	}
	if len(l.Name) > 80 {
		return badf("That name is too long.")
	}
	if !l.Movies && !l.Shows {
		return badf("Pick movies, shows or both.")
	}
	for _, c := range []struct {
		on      bool
		kind    string
		profile int
		root    string
	}{{l.Movies, "movie", l.MovieProfile, l.MovieRoot}, {l.Shows, "series", l.ShowProfile, l.ShowRoot}} {
		if !c.on {
			continue
		}
		if c.profile != 0 {
			if _, err := s.Store.Profile(ctx, c.profile); err != nil {
				return badf("There's no such quality profile.")
			}
		}
		if c.root != "" {
			roots, err := s.Store.RootFolders(ctx, c.kind)
			if err != nil {
				return err
			}
			ok := false
			for _, r := range roots {
				ok = ok || r.Path == c.root
			}
			if !ok {
				return badf("%s isn't one of the root folders.", c.root)
			}
		}
	}
	return nil
}

// Run reads each list when it's due, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(firstWait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		lists, err := s.Store.Lists(ctx)
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("lists: couldn't read the lists", "err", err)
		}
		for _, l := range lists {
			if ctx.Err() != nil {
				return
			}
			if !l.Enabled || l.CheckedAt != nil && time.Since(*l.CheckedAt) < Every {
				continue
			}
			s.Check(ctx, l.ID)
		}
		t.Reset(look)
	}
}

// Result is what reading a list did.
type Result struct {
	Added   int `json:"added"`
	Waiting int `json:"waiting"`
	// Later is how many more will be added at the next checks.
	Later int `json:"later"`
}

// Check reads a list now and handles what's new on it. Why it couldn't be
// read is kept on the list, and returned.
func (s *Service) Check(ctx context.Context, id int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.Store.List(ctx, id)
	if err != nil {
		return Result{}, err
	}
	res, err := s.read(ctx, l)
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	problem := ""
	if err != nil {
		problem = err.Error()
		s.Log.Warn("lists: couldn't read a list", "list", l.Name, "err", err)
	} else if res.Added > 0 || res.Waiting > 0 {
		s.Log.Info("lists: list read", "list", l.Name, "added", res.Added, "waiting", res.Waiting, "later", res.Later)
	}
	if serr := s.Store.ListChecked(ctx, id, problem); serr != nil && err == nil {
		err = serr
	}
	return res, err
}

func (s *Service) read(ctx context.Context, l store.List) (Result, error) {
	var res Result
	p, err := Place(l.Address)
	if err != nil {
		return res, err
	}
	_, entries, err := s.fetch(ctx, p)
	if err != nil {
		return res, err
	}
	items, err := s.Store.ListItems(ctx, l.ID)
	if err != nil {
		return res, err
	}
	was := map[ref]store.ListItem{}
	for _, it := range items {
		was[ref{it.Kind, it.TMDBID}] = it
	}
	done := map[ref]bool{}
	for _, e := range entries {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		r, err := s.resolve(ctx, e)
		if errors.Is(err, tmdb.ErrNotFound) {
			continue // TMDB doesn't know it: nothing to add
		}
		if err != nil {
			return res, err
		}
		if done[r] || r.Kind == tmdb.KindMovie && !l.Movies || r.Kind == tmdb.KindSeries && !l.Shows {
			continue
		}
		done[r] = true
		it := store.ListItem{ListID: l.ID, Kind: r.Kind, TMDBID: r.ID, Title: e.Title, Year: e.Year}
		here, err := s.here(ctx, r)
		if err != nil {
			return res, err
		}
		prev, seen := was[r]
		switch {
		case seen && prev.State == store.ListAdded && !here:
			it.State = store.ListRemoved
		case seen && prev.State != store.ListWaiting && prev.State != store.ListFailed:
			continue // handled before
		case here:
			it.State = store.ListHad
		case !seen:
			before, err := s.Store.ListAddedBefore(ctx, r.Kind, r.ID)
			if err != nil {
				return res, err
			}
			if before {
				it.State = store.ListRemoved
			}
		}
		if it.State == "" && l.Ask {
			it.State = store.ListWaiting
			res.Waiting++
			if seen && prev.State == store.ListWaiting {
				continue
			}
		}
		if it.State == "" {
			if res.Added >= PerCheck {
				res.Later++
				continue
			}
			it.State, it.Note = s.add(ctx, l, r)
			if it.State == store.ListAdded {
				res.Added++
			}
		}
		if err := s.Store.SetListItem(ctx, it); err != nil {
			return res, err
		}
	}
	return res, nil
}

// here says whether a title is on Box Office's list.
func (s *Service) here(ctx context.Context, r ref) (bool, error) {
	var err error
	if r.Kind == tmdb.KindMovie {
		_, err = s.Store.MovieByTMDB(ctx, r.ID)
	} else {
		_, err = s.Store.SeriesByTMDB(ctx, r.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// add adds a title the way its list says, and gives what became of it.
func (s *Service) add(ctx context.Context, l store.List, r ref) (state, note string) {
	var err error
	if r.Kind == tmdb.KindMovie {
		_, err = s.Catalog.AddMovie(ctx, r.ID, catalog.AddOptions{ProfileID: l.MovieProfile, RootFolder: l.MovieRoot, Monitored: true, Search: l.Search})
	} else {
		_, err = s.Catalog.AddSeries(ctx, r.ID, catalog.AddOptions{ProfileID: l.ShowProfile, RootFolder: l.ShowRoot, Monitored: true, Search: l.Search})
	}
	switch {
	case err == nil:
		return store.ListAdded, ""
	case errors.Is(err, catalog.ErrExists):
		return store.ListHad, ""
	}
	return store.ListFailed, err.Error()
}

// resolve finds which TMDB title a list's entry is.
func (s *Service) resolve(ctx context.Context, e Entry) (ref, error) {
	if e.TMDBID != 0 && e.Kind != "" {
		return ref{e.Kind, e.TMDBID}, nil
	}
	source, id := "imdb_id", e.IMDBID
	if id == "" && e.TVDBID != 0 {
		source, id = "tvdb_id", fmt.Sprint(e.TVDBID)
	}
	if id == "" {
		return ref{}, tmdb.ErrNotFound
	}
	key := source + ":" + id + ":" + e.Kind
	s.foundMu.Lock()
	r, ok := s.found[key]
	s.foundMu.Unlock()
	if ok {
		return r, nil
	}
	movie, show, err := s.Catalog.TMDB(ctx).Find(ctx, source, id)
	if err != nil {
		return ref{}, err
	}
	switch {
	case e.Kind == tmdb.KindMovie && movie != 0, e.Kind == "" && movie != 0:
		r = ref{tmdb.KindMovie, movie}
	case e.Kind != tmdb.KindMovie && show != 0:
		r = ref{tmdb.KindSeries, show}
	default:
		return ref{}, tmdb.ErrNotFound
	}
	s.foundMu.Lock()
	if s.found == nil {
		s.found = map[string]ref{}
	}
	s.found[key] = r
	s.foundMu.Unlock()
	return r, nil
}

// Decide answers for a title that's waiting (or was skipped, removed or
// couldn't be added): add it now, or leave it out.
func (s *Service) Decide(ctx context.Context, listID int64, kind string, tmdbID int64, add bool) (store.ListItem, error) {
	l, err := s.Store.List(ctx, listID)
	if err != nil {
		return store.ListItem{}, err
	}
	items, err := s.Store.ListItems(ctx, listID)
	if err != nil {
		return store.ListItem{}, err
	}
	for _, it := range items {
		if it.Kind != kind || it.TMDBID != tmdbID {
			continue
		}
		if it.State == store.ListAdded || it.State == store.ListHad {
			return it, nil
		}
		if add {
			it.State, it.Note = s.add(ctx, l, ref{kind, tmdbID})
		} else {
			it.State, it.Note = store.ListSkipped, ""
		}
		return it, s.Store.SetListItem(ctx, it)
	}
	return store.ListItem{}, store.ErrNotFound
}
