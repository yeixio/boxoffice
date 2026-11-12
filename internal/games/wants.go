package games

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// ErrOnDisk means a game wanted is in its system's folder already.
var ErrOnDisk = errors.New("it's in your games already")

// Searcher queues a search for a game wanted, which grabs the best
// release.
type Searcher func(ctx context.Context, w store.GameWant) error

// Want adds a version of a game from its system's list to the games
// wanted, and looks for it. Wanting it again gives the want there is.
func (s *Service) Want(ctx context.Context, system, name string) (store.GameWant, error) {
	if err := s.on(ctx); err != nil {
		return store.GameWant{}, err
	}
	sys, ok := gamedat.LookupSystem(system)
	if !ok {
		return store.GameWant{}, fmt.Errorf("Box Office doesn't know a system called %q", system)
	}
	roots, err := s.Store.RootFolders(ctx, mediatypes.Game)
	if err != nil {
		return store.GameWant{}, err
	}
	if len(roots) == 0 {
		return store.GameWant{}, errors.New("add a games root folder in Settings first, so Box Office has somewhere to put games")
	}
	// Somewhere for it to go: not a system on Do not touch.
	if _, err := s.Folder(ctx, system); err != nil {
		return store.GameWant{}, err
	}
	g, err := s.CatalogGameByName(ctx, system, name)
	if err != nil {
		return store.GameWant{}, err
	}
	w := store.GameWant{System: sys.Key, Name: g.Name, Title: g.Title, Year: g.Year, Maker: g.Maker, CRC: g.CRC, Size: g.Size, Art: g.Art}
	if !sys.Arcade {
		w.Group = gamedat.TitleKey(g.Title)
	}
	if on, err := s.Store.GameOnDisk(ctx, w.System, w.Name, w.Group); err != nil {
		return store.GameWant{}, err
	} else if on {
		return store.GameWant{}, ErrOnDisk
	}
	added, err := s.Store.AddGameWant(ctx, w)
	if errors.Is(err, store.ErrExists) {
		return s.wantByName(ctx, w.System, w.Name)
	}
	if err != nil {
		return store.GameWant{}, err
	}
	if s.Search != nil {
		if err := s.Search(ctx, added); err != nil {
			s.log().Warn("looking for a game wanted", "game", added.Name, "err", err)
		}
	}
	return added, nil
}

func (s *Service) wantByName(ctx context.Context, system, name string) (store.GameWant, error) {
	ws, err := s.Store.GameWants(ctx)
	if err != nil {
		return store.GameWant{}, err
	}
	for _, w := range ws {
		if w.System == system && w.Name == name {
			return w, nil
		}
	}
	return store.GameWant{}, store.ErrNotFound
}

// LookFor searches for a game wanted now, and grabs the best release.
func (s *Service) LookFor(ctx context.Context, id int64) error {
	if err := s.on(ctx); err != nil {
		return err
	}
	w, err := s.Store.GameWant(ctx, id)
	if err != nil {
		return err
	}
	if s.Search == nil {
		return errors.New("searching isn't set up")
	}
	return s.Search(ctx, w)
}

// WantView is a game wanted, with how the looking for it is going.
type WantView struct {
	store.GameWant
	SystemName  string `json:"systemName"`
	SystemShort string `json:"systemShort"`
	Arcade      bool   `json:"arcade"`
	// Searched is when it was last searched for, and Misses how many
	// searches in a row found nothing to take.
	Searched time.Time `json:"searched,omitzero"`
	Misses   int       `json:"misses,omitempty"`
	// Download is the download on its way, if there's one.
	Download *store.Download `json:"download,omitempty"`
}

// Wants lists the games wanted, newest first.
func (s *Service) Wants(ctx context.Context) ([]WantView, error) {
	ws, err := s.Store.GameWants(ctx)
	if err != nil {
		return nil, err
	}
	searched, err := s.Store.Searched(ctx, store.GameKind)
	if err != nil {
		return nil, err
	}
	out := make([]WantView, 0, len(ws))
	for _, w := range ws {
		v := WantView{GameWant: w, SystemName: w.System, SystemShort: w.System, Searched: searched[w.ID].At, Misses: searched[w.ID].Misses}
		if sys, ok := gamedat.LookupSystem(w.System); ok {
			v.SystemName, v.SystemShort, v.Arcade = sys.Name, sys.Short, sys.Arcade
		}
		if ds, err := s.Store.Pending(ctx, store.GameKind, w.ID); err == nil && len(ds) > 0 {
			d := ds[len(ds)-1]
			v.Download = &d
		}
		out = append(out, v)
	}
	return out, nil
}

// WantArt is the picture of a game wanted, as ArtFile.
func (s *Service) WantArt(ctx context.Context, id int64) (string, error) {
	w, err := s.Store.GameWant(ctx, id)
	if err != nil {
		return "", err
	}
	return s.ArtFile(ctx, store.Game{Name: w.Name, Title: w.Title, Art: w.Art}, w.System)
}
