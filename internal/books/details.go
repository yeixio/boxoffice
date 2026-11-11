package books

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/wikidata"
)

// DetailsAge is how long an author's photo and series are kept before
// they're looked up again.
const DetailsAge = 30 * 24 * time.Hour

// seriesLookups is how many of an author's books, the best known first,
// are looked up one by one on Open Library for their series. Wikidata
// answers for all of them at once.
const seriesLookups = 25

// Kick has the details loop look up authors soon, after one is added or
// refreshed.
func (s *Service) kick() {
	select {
	case s.wakeCh() <- struct{}{}:
	default:
	}
}

// DetailsEvery looks up the photos and series of authors that have none
// yet, or whose are a month old: at start, every few hours, and soon
// after an author is added or refreshed. It runs on its own, not in the
// job queue, so it never holds up searches.
func (s *Service) DetailsEvery(ctx context.Context) {
	tick := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return
	}
	for {
		s.detailAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wakeCh():
		}
	}
}

func (s *Service) wakeCh() chan struct{} {
	s.wakeOnce.Do(func() { s.wake = make(chan struct{}, 1) })
	return s.wake
}

func (s *Service) detailAll(ctx context.Context) {
	if s.on(ctx, "") != nil {
		return
	}
	as, err := s.Store.AuthorsToDetail(ctx, time.Now().Add(-DetailsAge))
	if err != nil {
		s.log().Warn("listing authors to look up", "err", err)
		return
	}
	for _, a := range as {
		if ctx.Err() != nil {
			return
		}
		if err := s.Details(ctx, a); err != nil {
			s.log().Warn("looking up an author's photo and series", "author", a.Name, "err", err)
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Details looks up an author's photo and which of their books are part
// of a series, and saves them.
func (s *Service) Details(ctx context.Context, a store.Author) error {
	ol, err := s.OpenLibrary.Author(ctx, a.OLKey)
	if err != nil {
		return err
	}
	photo := ""
	if ol.HasPhoto {
		photo = ol.Photo
	}
	if photo == "" && s.Wikidata != nil && ol.Wikidata != "" {
		if photo, err = s.Wikidata.Photo(ctx, ol.Wikidata); err != nil {
			s.log().Debug("no photo from Wikidata", "author", a.Name, "err", err)
		}
	}
	bs, err := s.Store.Books(ctx, a.ID)
	if err != nil {
		return err
	}
	places, err := s.findSeries(ctx, bs, ol.Wikidata)
	if err != nil {
		return err
	}
	if err := s.Store.SetAuthorSeries(ctx, a.ID, places); err != nil {
		return err
	}
	return s.Store.SetAuthorDetails(ctx, a.ID, photo, ol.Wikidata)
}

// findSeries works out which books (listed best known first) are part of
// a series: what Open Library says first, then what Wikidata says, then
// books named after one of those series, like The Lord of the Rings, the
// three of them in one.
func (s *Service) findSeries(ctx context.Context, bs []store.Book, item string) (map[int64]store.SeriesPlace, error) {
	places := map[int64]store.SeriesPlace{}
	names := map[string]string{} // Open Library series key → name
	for i, b := range bs {
		if i >= seriesLookups {
			break
		}
		parts, err := s.OpenLibrary.WorkSeries(ctx, b.OLKey)
		if errors.Is(err, openlibrary.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, p := range parts {
			name, ok := names[p.Key]
			if !ok {
				if name, err = s.OpenLibrary.SeriesName(ctx, p.Key); err != nil && !errors.Is(err, openlibrary.ErrNotFound) {
					return nil, err
				}
				names[p.Key] = name
			}
			if name != "" {
				places[b.ID] = store.SeriesPlace{Series: name, Position: p.Position}
				break
			}
		}
	}

	if s.Wikidata != nil && item != "" {
		parts, err := s.Wikidata.Series(ctx, item)
		if err != nil {
			// Open Library's answer stands on its own.
			s.log().Debug("no series from Wikidata", "err", err)
		}
		placeWikidata(bs, parts, places)
	}

	// A book named after a series is that series in one book.
	series := map[string]string{}
	for _, p := range places {
		series[fold(p.Series)] = p.Series
	}
	for _, b := range bs {
		if _, ok := places[b.ID]; ok {
			continue
		}
		if name, ok := series[fold(b.Title)]; ok {
			places[b.ID] = store.SeriesPlace{Series: name}
		}
	}
	return places, nil
}

// placeWikidata adds the series Wikidata knows of to books that have none
// yet. A book Wikidata puts in several series goes in the one that says
// where it falls, then the smallest: a trilogy, not the whole world it's
// set in.
func placeWikidata(bs []store.Book, parts []wikidata.Part, places map[int64]store.SeriesPlace) {
	byKey := map[string]int64{}
	byTitle := map[string]int64{}
	for _, b := range bs {
		byKey[b.OLKey] = b.ID
		if t := fold(b.Title); t != "" {
			if _, ok := byTitle[t]; !ok {
				byTitle[t] = b.ID
			}
		}
	}
	size := map[string]int{}
	type pick struct {
		part wikidata.Part
		book int64
	}
	var picks []pick
	for _, p := range parts {
		id, ok := byKey[p.OLWork]
		if !ok {
			id, ok = byTitle[fold(p.Title)]
		}
		if !ok {
			continue
		}
		size[p.SeriesID]++
		picks = append(picks, pick{p, id})
	}
	best := map[int64]wikidata.Part{}
	for _, p := range picks {
		if _, ok := places[p.book]; ok {
			continue
		}
		was, ok := best[p.book]
		if !ok || better(p.part, was, size) {
			best[p.book] = p.part
		}
	}
	for id, p := range best {
		places[id] = store.SeriesPlace{Series: p.Series, Position: p.Position}
	}
}

func better(p, than wikidata.Part, size map[string]int) bool {
	if (p.Position != "") != (than.Position != "") {
		return p.Position != ""
	}
	if size[p.SeriesID] != size[than.SeriesID] {
		return size[p.SeriesID] < size[than.SeriesID]
	}
	return p.SeriesID < than.SeriesID
}

// fold is a title for matching: lower case, letters and digits only, and
// no "The" or "A" in front.
func fold(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, the := range []string{"the ", "a ", "an "} {
		t = strings.TrimPrefix(t, the)
	}
	var b strings.Builder
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
