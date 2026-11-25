package titlelist

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// JobAdd adds one title from a loaded file.
const JobAdd = "titles.add"

// Service adds the titles of a loaded file, one job a title, so a long
// list is worked through in the background and each shows on Activity.
type Service struct {
	Store   *store.Store
	Catalog *catalog.Service
	Music   *music.Service
	Books   *books.Service
	Jobs    *jobs.Runner
}

// Register adds the service's job handler to its runner.
func (s *Service) Register() { s.Jobs.Handle(JobAdd, s.job) }

type payload struct {
	Entry
	// Search looks for it once it's added.
	Search bool `json:"search,omitempty"`
}

// Queue lines up a plan's new titles to be added.
func (s *Service) Queue(ctx context.Context, p Plan, search bool) error {
	for _, e := range p.New {
		b, err := json.Marshal(payload{e, search})
		if err != nil {
			return err
		}
		if _, err := s.Jobs.Queue(ctx, JobAdd, cmpName(e), string(b)); err != nil {
			return err
		}
	}
	return nil
}

func cmpName(e Entry) string {
	if e.Title == "" {
		return e.key()
	}
	return e.Name()
}

func (s *Service) job(ctx context.Context, raw string) (jobs.Result, error) {
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return jobs.Result{}, err
	}
	err := s.Add(ctx, p.Entry, p.Search)
	if errors.Is(err, catalog.ErrExists) {
		return jobs.Skipped("it's on the list already"), nil
	}
	if err != nil {
		return jobs.Result{}, err
	}
	return jobs.Done("added from a file"), nil
}

// Add adds one title as the file describes it. A quality profile or root
// folder that isn't one here is left to the defaults and the rules for
// new titles.
func (s *Service) Add(ctx context.Context, e Entry, search bool) error {
	h, err := look(ctx, s.Store)
	if err != nil {
		return err
	}
	root := ""
	if k := rootKind(e.Kind); k != "" && h.roots[k][e.RootFolder] {
		root = e.RootFolder
	}
	switch e.Kind {
	case Movie:
		o := catalog.AddOptions{ProfileID: h.profiles[strings.ToLower(e.Profile)], RootFolder: root, Monitored: e.Monitored, Search: search}
		o.Chosen = o.ProfileID != 0 || o.RootFolder != ""
		id := e.TMDBID
		if id == 0 {
			m, err := s.Catalog.TMDB(ctx).MovieByIMDB(ctx, e.IMDBID)
			if errors.Is(err, tmdb.ErrNotFound) {
				return &Error{"TMDB has no movie with the IMDb id " + e.IMDBID}
			}
			if err != nil {
				return err
			}
			id = m.TMDBID
		}
		_, err := s.Catalog.AddMovie(ctx, id, o)
		return err
	case Show:
		return s.addShow(ctx, e, catalog.AddOptions{ProfileID: h.profiles[strings.ToLower(e.Profile)], RootFolder: root,
			Monitored: e.Monitored, KeepAhead: e.KeepAhead}, search)
	case Artist:
		_, err := s.Music.Add(ctx, music.AddOptions{MBID: e.MBID, RootFolder: root, Monitored: e.Monitored, AlbumTypes: e.AlbumTypes})
		return err
	case Author:
		return s.addAuthor(ctx, h, e)
	}
	return &Error{"there's no kind of title called " + e.Kind}
}

func (s *Service) addShow(ctx context.Context, e Entry, o catalog.AddOptions, search bool) error {
	o.Chosen = o.ProfileID != 0 || o.RootFolder != ""
	if catalog.CheckKeepAhead(o.KeepAhead) != nil {
		o.KeepAhead = 0
	}
	c := s.Catalog.TMDB(ctx)
	var info tmdb.Show
	var err error
	if e.TMDBID > 0 {
		if _, err := s.Store.SeriesByTMDB(ctx, e.TMDBID); err == nil {
			return catalog.ErrExists
		}
		info, err = c.Show(ctx, e.TMDBID)
	} else {
		info, err = c.ShowByTVDB(ctx, e.TVDBID)
	}
	if errors.Is(err, tmdb.ErrNotFound) {
		return &Error{"TMDB doesn't know that show"}
	}
	if err != nil {
		return err
	}
	// The seasons the file names are the ones watched, when they're
	// seasons TMDB numbers the same way. A file that names none, or
	// numbers them otherwise (by year, say), leaves it to the default for
	// new shows.
	var seasons map[int]bool
	for _, n := range e.Seasons {
		for _, se := range info.Seasons {
			if se.Number == n {
				if seasons == nil {
					seasons = map[int]bool{}
				}
				seasons[n] = true
			}
		}
	}
	added, err := s.Catalog.AddSeriesInfo(ctx, info, o, seasons)
	if err != nil {
		return err
	}
	if seasons != nil {
		for _, se := range added.Seasons {
			if !seasons[se.Number] {
				if err := s.Store.SetSeasonEpisodesMonitored(ctx, added.ID, se.Number, false); err != nil {
					return err
				}
			}
		}
	}
	if search && added.Monitored {
		return s.Catalog.SearchSeries(ctx, added.ID)
	}
	return nil
}

func (s *Service) addAuthor(ctx context.Context, h here, e Entry) error {
	format := h.format(e)
	if format == "" {
		return &Error{"ebooks and audiobooks are turned off, or have no root folder yet"}
	}
	a, err := s.Books.Add(ctx, books.AddOptions{Key: e.OLKey, Format: format,
		New: format == store.Ebook && e.NewEbooks || format == store.Audiobook && e.NewAudiobooks})
	if err != nil {
		return err
	}
	usable := func(f string) bool { return h.on[f] && len(h.roots[f]) > 0 }
	// New books in the other format too, where that's turned on.
	on := true
	if format == store.Ebook && e.NewAudiobooks && usable(store.Audiobook) {
		if _, err := s.Books.Update(ctx, a.ID, books.Edit{NewAudiobooks: &on}); err != nil {
			return err
		}
	}
	if len(e.Books) == 0 {
		return nil
	}
	have, err := s.Store.Books(ctx, a.ID)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for _, b := range have {
		ids[b.OLKey] = b.ID
	}
	for _, b := range e.Books {
		id, ok := ids[olKey(b.Key)]
		if !ok {
			continue
		}
		if b.Ebook && usable(store.Ebook) {
			if err := s.Store.SetBookWanted(ctx, id, store.Ebook, true); err != nil {
				return err
			}
		}
		if b.Audiobook && usable(store.Audiobook) {
			if err := s.Store.SetBookWanted(ctx, id, store.Audiobook, true); err != nil {
				return err
			}
		}
	}
	return nil
}
