package catalog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Discover is the home page: rows of titles from TMDB (trending, popular,
// coming soon, and ones like what's already in the library), and genres
// and networks to browse, each title marked with where it stands here.
//
// TMDB's lists change slowly and each row is a trip out, so pages are kept
// a while (listTTL); genres and networks for a day.

// Where a title from TMDB stands here.
const (
	StatusAvailable = "available" // on disk (for a show, every watched episode that's aired)
	StatusPartial   = "partial"   // a show with some episodes on disk
	StatusWanted    = "wanted"    // on the list and watched for, nothing on disk yet
	StatusPaused    = "paused"    // on the list, not watched for
)

const (
	listTTL   = time.Hour
	browseTTL = 24 * time.Hour
	// rowPages caps how far a row's own page goes; TMDB's lists run to
	// hundreds of pages.
	rowPages = 20
	// forYouSeeds is how many recent movies and shows (each) the For you
	// row is based on, and becauseRows how many of them get their own row.
	forYouSeeds = 5
	becauseRows = 2
	forYouMax   = 40
	// personPage is how many of someone's titles a page of their row
	// holds, and castInSearch how many a search for their name adds.
	personPage   = 20
	castInSearch = 20
)

// Networks are the TV networks and streaming services to browse by, as
// TMDB's ids. Names and logos come from TMDB.
var Networks = []int64{213, 49, 2552, 2739, 1024, 453, 3186, 4330, 3353, 174, 88, 4, 2, 6, 16, 19}

// DiscoverTitle is a title from TMDB and where it stands here.
type DiscoverTitle struct {
	tmdb.Title
	// Status is one of the Status constants, or empty when it's not on
	// the list; ID is its id here when it is.
	Status string `json:"status,omitempty"`
	ID     int64  `json:"id,omitempty"`
}

// DiscoverRow is one row of the home page.
type DiscoverRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Because is the library title a "because-" row is based on.
	Because string          `json:"because,omitempty"`
	Items   []DiscoverTitle `json:"items"`
	// Person is who a person- row is the filmography of.
	Person *tmdb.Person `json:"person,omitempty"`
	// More says GET /discover/rows/{id} has more than the row shows.
	More bool `json:"more,omitempty"`
}

// DiscoverPage is a page of one row.
type DiscoverPage struct {
	DiscoverRow
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

// Browse is what can be browsed by.
type Browse struct {
	MovieGenres  []tmdb.Genre   `json:"movieGenres"`
	SeriesGenres []tmdb.Genre   `json:"seriesGenres"`
	Networks     []tmdb.Network `json:"networks"`
}

// DiscoverHome is the whole home page.
type DiscoverHome struct {
	Rows   []DiscoverRow `json:"rows"`
	Browse Browse        `json:"browse"`
}

type cacheEntry struct {
	val any
	at  time.Time
}

type discoverCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

// cached is fn's answer for key, kept for ttl. Failures aren't kept.
func cached[T any](s *Service, key string, ttl time.Duration, fn func() (T, error)) (T, error) {
	c := &s.discover
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return e.val.(T), nil
	}
	c.mu.Unlock()
	v, err := fn()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	for k, e := range c.entries {
		if time.Since(e.at) > browseTTL {
			delete(c.entries, k)
		}
	}
	c.entries[key] = cacheEntry{v, time.Now()}
	return v, nil
}

// owned indexes the list by kind and TMDB id.
type owned map[string]store.Owned

func ownedKey(kind string, tmdbID int64) string { return kind + ":" + strconv.FormatInt(tmdbID, 10) }

func (s *Service) owned(ctx context.Context) (owned, []store.Owned, error) {
	list, err := s.Store.OwnedTitles(ctx)
	if err != nil {
		return nil, nil, err
	}
	m := owned{}
	for _, o := range list {
		m[ownedKey(o.Kind, o.TMDBID)] = o
	}
	return m, list, nil
}

// Status says where a title on the list stands.
func Status(o store.Owned) string {
	switch {
	case o.Kind == "movie" && o.Have > 0, o.Kind == "series" && o.Wanted > 0 && o.Have >= o.Wanted:
		return StatusAvailable
	case o.Files > 0:
		return StatusPartial
	case o.Monitored:
		return StatusWanted
	}
	return StatusPaused
}

// mark adds where each title stands; dropOwned leaves out the ones already
// on the list.
func (own owned) mark(titles []tmdb.Title, dropOwned bool) []DiscoverTitle {
	out := []DiscoverTitle{}
	seen := map[string]bool{}
	for _, t := range titles {
		k := ownedKey(t.Kind, t.TMDBID)
		if seen[k] {
			continue
		}
		seen[k] = true
		v := DiscoverTitle{Title: t}
		if o, ok := own[k]; ok {
			if dropOwned {
				continue
			}
			v.Status, v.ID = Status(o), o.ID
		}
		out = append(out, v)
	}
	return out
}

func kindName(kind string) string {
	if kind == tmdb.KindMovie {
		return "movies"
	}
	return "shows"
}

// source is where a row's titles come from.
type source struct {
	title     string
	because   string
	dropOwned bool
	person    *tmdb.Person
	load      func(page int) (tmdb.Page, error)
}

// ErrNoRow means there's no row by that id.
var ErrNoRow = errors.New("there's no such row")

// source reads a row's id. Rows: trending; popular-movies, popular-series;
// upcoming-movies, upcoming-series; for-you; because-{movie,series}-{tmdb
// id} (a title on the list); like-{movie,series}-{tmdb id} (any title);
// genre-{movie,series}-{genre id}; network-{network id}; person-{tmdb
// person id} (what someone is in).
func (s *Service) source(ctx context.Context, id string, own owned, list []store.Owned) (source, error) {
	c := s.TMDB(ctx)
	page := func(key string, fn func(page int) (tmdb.Page, error)) func(int) (tmdb.Page, error) {
		return func(p int) (tmdb.Page, error) {
			return cached(s, fmt.Sprintf("%s/%d", key, p), listTTL, func() (tmdb.Page, error) { return fn(p) })
		}
	}
	kinds := map[string]string{"movies": tmdb.KindMovie, "movie": tmdb.KindMovie, "series": tmdb.KindSeries}
	switch id {
	case "trending":
		return source{title: "Trending this week", load: page(id, func(p int) (tmdb.Page, error) { return c.Trending(ctx, p) })}, nil
	case "popular-movies", "popular-series":
		k := kinds[strings.TrimPrefix(id, "popular-")]
		return source{title: "Popular " + kindName(k), load: page(id, func(p int) (tmdb.Page, error) { return c.Popular(ctx, k, p) })}, nil
	case "upcoming-movies":
		return source{title: "Coming to cinemas", load: page(id, func(p int) (tmdb.Page, error) { return c.Upcoming(ctx, tmdb.KindMovie, p, time.Now()) })}, nil
	case "upcoming-series":
		return source{title: "New shows coming up", load: page(id, func(p int) (tmdb.Page, error) { return c.Upcoming(ctx, tmdb.KindSeries, p, time.Now()) })}, nil
	case "for-you":
		return source{title: "Recommended for you", dropOwned: true, load: func(p int) (tmdb.Page, error) {
			if p > 1 {
				return tmdb.Page{Titles: []tmdb.Title{}, Pages: 1}, nil
			}
			return s.forYou(ctx, list)
		}}, nil
	}
	parts := strings.Split(id, "-")
	num := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
	switch {
	case len(parts) == 3 && parts[0] == "because" && kinds[parts[1]] != "" && num(parts[2]) > 0:
		k, tid := kinds[parts[1]], num(parts[2])
		o, ok := own[ownedKey(k, tid)]
		if !ok {
			return source{}, ErrNoRow
		}
		return source{title: "Because you have " + o.Title, because: o.Title, dropOwned: true,
			load: page(id, func(p int) (tmdb.Page, error) { return c.Recommendations(ctx, k, tid, p) })}, nil
	case len(parts) == 3 && parts[0] == "like" && kinds[parts[1]] != "" && num(parts[2]) > 0:
		// Titles like any movie or show, here or not, such as one someone
		// watched in Matinee; the caller words the row.
		k, tid := kinds[parts[1]], num(parts[2])
		return source{title: "More like this", dropOwned: true,
			load: page("because-"+parts[1]+"-"+parts[2], func(p int) (tmdb.Page, error) { return c.Recommendations(ctx, k, tid, p) })}, nil
	case len(parts) == 3 && parts[0] == "genre" && kinds[parts[1]] != "" && num(parts[2]) > 0:
		k, gid := kinds[parts[1]], num(parts[2])
		b, err := s.browse(ctx)
		if err != nil {
			return source{}, err
		}
		gs := b.MovieGenres
		if k == tmdb.KindSeries {
			gs = b.SeriesGenres
		}
		i := slices.IndexFunc(gs, func(g tmdb.Genre) bool { return g.ID == gid })
		if i < 0 {
			return source{}, ErrNoRow
		}
		return source{title: gs[i].Name + " " + kindName(k), load: page(id, func(p int) (tmdb.Page, error) { return c.ByGenre(ctx, k, gid, p) })}, nil
	case len(parts) == 2 && parts[0] == "person" && num(parts[1]) > 0:
		// Everything someone is in, such as an actor found by searching.
		f, err := s.filmography(ctx, num(parts[1]))
		if errors.Is(err, tmdb.ErrNotFound) {
			return source{}, ErrNoRow
		}
		if err != nil {
			return source{}, err
		}
		return source{title: f.Person.Name, person: &f.Person, load: func(p int) (tmdb.Page, error) {
			pages := max((len(f.Titles)+personPage-1)/personPage, 1)
			lo := min((p-1)*personPage, len(f.Titles))
			return tmdb.Page{Titles: f.Titles[lo:min(lo+personPage, len(f.Titles))], Pages: pages}, nil
		}}, nil
	case len(parts) == 2 && parts[0] == "network" && num(parts[1]) > 0:
		nid := num(parts[1])
		if !slices.Contains(Networks, nid) {
			return source{}, ErrNoRow
		}
		n, err := cached(s, id, browseTTL, func() (tmdb.Network, error) { return c.Network(ctx, nid) })
		if err != nil {
			return source{}, err
		}
		return source{title: "Shows on " + n.Name, load: page(id, func(p int) (tmdb.Page, error) { return c.ByNetwork(ctx, nid, p) })}, nil
	}
	return source{}, ErrNoRow
}

// seeds are the titles the For you row is based on: the latest movies and
// shows added that have something on disk.
func seeds(list []store.Owned) (movies, shows []store.Owned) {
	have := slices.DeleteFunc(slices.Clone(list), func(o store.Owned) bool { return o.Files == 0 || o.TMDBID == 0 })
	slices.SortStableFunc(have, func(a, b store.Owned) int { return cmp.Or(b.AddedAt.Compare(a.AddedAt), cmp.Compare(b.ID, a.ID)) })
	for _, o := range have {
		switch {
		case o.Kind == "movie" && len(movies) < forYouSeeds:
			movies = append(movies, o)
		case o.Kind == "series" && len(shows) < forYouSeeds:
			shows = append(shows, o)
		}
	}
	return movies, shows
}

// forYou pools TMDB's recommendations for the seeds: titles recommended
// for more of them, and higher up, come first.
func (s *Service) forYou(ctx context.Context, list []store.Owned) (tmdb.Page, error) {
	movies, shows := seeds(list)
	all := append(movies, shows...)
	c := s.TMDB(ctx)
	pages := make([]tmdb.Page, len(all))
	errs := make([]error, len(all))
	var wg sync.WaitGroup
	for i, o := range all {
		wg.Go(func() {
			key := fmt.Sprintf("because-%s-%d/1", o.Kind, o.TMDBID)
			pages[i], errs[i] = cached(s, key, listTTL, func() (tmdb.Page, error) { return c.Recommendations(ctx, o.Kind, o.TMDBID, 1) })
		})
	}
	wg.Wait()
	type scored struct {
		t     tmdb.Title
		score float64
	}
	by := map[string]*scored{}
	var order []*scored
	ok := false
	for i, p := range pages {
		if errs[i] != nil {
			if errors.Is(errs[i], tmdb.ErrNoKey) {
				return tmdb.Page{}, errs[i]
			}
			continue
		}
		ok = true
		for rank, t := range p.Titles {
			k := ownedKey(t.Kind, t.TMDBID)
			sc := by[k]
			if sc == nil {
				sc = &scored{t: t}
				by[k] = sc
				order = append(order, sc)
			}
			sc.score += 1 - float64(rank)/float64(len(p.Titles)+1)
		}
	}
	if !ok && len(all) > 0 {
		return tmdb.Page{}, cmp.Or(errs...)
	}
	slices.SortStableFunc(order, func(a, b *scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(b.t.Popularity, a.t.Popularity))
	})
	out := tmdb.Page{Titles: []tmdb.Title{}, Pages: 1}
	for _, sc := range order {
		out.Titles = append(out.Titles, sc.t)
	}
	return out, nil
}

// browse lists the genres and networks, kept a day. A network TMDB can't
// describe is left out.
func (s *Service) browse(ctx context.Context) (Browse, error) {
	return cached(s, "browse", browseTTL, func() (Browse, error) {
		c := s.TMDB(ctx)
		b := Browse{Networks: []tmdb.Network{}}
		var err error
		if b.MovieGenres, err = c.Genres(ctx, tmdb.KindMovie); err != nil {
			return b, err
		}
		if b.SeriesGenres, err = c.Genres(ctx, tmdb.KindSeries); err != nil {
			return b, err
		}
		nets := make([]tmdb.Network, len(Networks))
		errs := make([]error, len(Networks))
		var wg sync.WaitGroup
		for i, id := range Networks {
			wg.Go(func() { nets[i], errs[i] = c.Network(ctx, id) })
		}
		wg.Wait()
		for i, n := range nets {
			if errs[i] == nil && n.Name != "" {
				b.Networks = append(b.Networks, n)
			}
		}
		return b, nil
	})
}

// homeRows are the rows of the home page after For you and the because
// rows, in order.
var homeRows = []string{"trending", "popular-movies", "popular-series", "upcoming-movies", "upcoming-series"}

// Home is the home page. A row TMDB can't fill is left out; if none can
// be filled, the first failure is the answer.
func (s *Service) Home(ctx context.Context) (DiscoverHome, error) {
	own, list, err := s.owned(ctx)
	if err != nil {
		return DiscoverHome{}, err
	}
	ids := []string{"for-you"}
	movies, shows := seeds(list)
	for i := range becauseRows {
		for _, ss := range [][]store.Owned{movies, shows} {
			if i < len(ss) {
				ids = append(ids, fmt.Sprintf("because-%s-%d", ss[i].Kind, ss[i].TMDBID))
			}
		}
	}
	// For you leads; the because rows go between the lists, so the page
	// doesn't open on four rows of the same thing.
	order := []string{ids[0], homeRows[0]}
	rest := ids[1:]
	for i, id := range homeRows[1:] {
		order = append(order, id)
		if i < len(rest) {
			order = append(order, rest[i])
		}
	}
	if len(rest) > len(homeRows)-1 {
		order = append(order, rest[len(homeRows)-1:]...)
	}

	rows := make([]DiscoverRow, len(order))
	errs := make([]error, len(order))
	var wg sync.WaitGroup
	for i, id := range order {
		wg.Go(func() {
			p, err := s.rowPage(ctx, id, 1, own, list)
			rows[i], errs[i] = p.DiscoverRow, err
		})
	}
	var b Browse
	var berr error
	wg.Go(func() { b, berr = s.browse(ctx) })
	wg.Wait()

	out := DiscoverHome{Rows: []DiscoverRow{}, Browse: b}
	var first error
	for i, r := range rows {
		if errs[i] != nil {
			if errors.Is(errs[i], tmdb.ErrNoKey) {
				return out, errs[i]
			}
			s.Log.Warn("discover row", "row", order[i], "err", errs[i])
			first = cmp.Or(first, errs[i])
			continue
		}
		if len(r.Items) > 0 {
			out.Rows = append(out.Rows, r)
		}
	}
	if berr != nil {
		s.Log.Warn("discover genres and networks", "err", berr)
		out.Browse = Browse{MovieGenres: []tmdb.Genre{}, SeriesGenres: []tmdb.Genre{}, Networks: []tmdb.Network{}}
	}
	if len(out.Rows) == 0 && first != nil {
		return out, first
	}
	return out, nil
}

// Row is a page (from 1) of one row.
func (s *Service) Row(ctx context.Context, id string, page int) (DiscoverPage, error) {
	own, list, err := s.owned(ctx)
	if err != nil {
		return DiscoverPage{}, err
	}
	return s.rowPage(ctx, id, page, own, list)
}

func (s *Service) rowPage(ctx context.Context, id string, page int, own owned, list []store.Owned) (DiscoverPage, error) {
	page = min(max(page, 1), rowPages)
	src, err := s.source(ctx, id, own, list)
	if err != nil {
		return DiscoverPage{}, err
	}
	p, err := src.load(page)
	if err != nil {
		return DiscoverPage{}, err
	}
	items := own.mark(p.Titles, src.dropOwned)
	if id == "for-you" && len(items) > forYouMax {
		items = items[:forYouMax]
	}
	pages := min(max(p.Pages, 1), rowPages)
	return DiscoverPage{
		DiscoverRow: DiscoverRow{ID: id, Title: src.title, Because: src.because, Person: src.person, Items: items, More: pages > page},
		Page:        page, Pages: pages,
	}, nil
}

// Search finds movies and shows by name on TMDB, both at once, marked
// with where each stands here. A search for an actor's name finds what
// they're in too: their best-known titles follow the ones named like it,
// or lead when the search is their whole name.
func (s *Service) Search(ctx context.Context, term string) ([]DiscoverTitle, error) {
	own, _, err := s.owned(ctx)
	if err != nil {
		return nil, err
	}
	var movies []LookupMovie
	var shows []LookupShow
	var cast Filmography
	var merr, serr error
	var wg sync.WaitGroup
	wg.Go(func() { movies, merr = s.LookupMovies(ctx, term) })
	wg.Go(func() { shows, serr = s.LookupShows(ctx, term) })
	wg.Go(func() { cast = s.castOf(ctx, term) })
	wg.Wait()
	if merr != nil && serr != nil {
		return nil, merr
	}
	var titles []tmdb.Title
	for _, m := range movies {
		titles = append(titles, tmdb.Title{Kind: tmdb.KindMovie, TMDBID: m.TMDBID, Title: m.Title, Year: m.Year, Overview: m.Overview,
			Poster: m.Poster, Backdrop: m.Backdrop, Rating: m.Rating})
	}
	for _, t := range shows {
		titles = append(titles, tmdb.Title{Kind: tmdb.KindSeries, TMDBID: t.TMDBID, Title: t.Title, Year: t.Year, Overview: t.Overview,
			Poster: t.Poster, Backdrop: t.Backdrop, Rating: t.Rating})
	}
	if len(cast.Titles) > 0 {
		theirs := cast.Titles[:min(len(cast.Titles), castInSearch)]
		if cast.Whole {
			titles = append(slices.Clone(theirs), titles...)
		} else {
			titles = append(titles, theirs...)
		}
		seen := map[string]bool{}
		titles = slices.DeleteFunc(titles, func(t tmdb.Title) bool {
			k := ownedKey(t.Kind, t.TMDBID)
			if seen[k] {
				return true
			}
			seen[k] = true
			return false
		})
	}
	return own.mark(titles, false), nil
}

// LookupPeople finds actors and other people by name on TMDB, for
// searches; GET /discover/rows/person-{tmdbId} is what each is in. People
// TMDB knows for nothing are left out.
func (s *Service) LookupPeople(ctx context.Context, term string) ([]tmdb.Person, error) {
	if len(strings.TrimSpace(term)) < 2 {
		return []tmdb.Person{}, nil
	}
	found, err := cached(s, "people/"+strings.ToLower(strings.TrimSpace(term)), listTTL, func() ([]tmdb.Person, error) {
		return s.TMDB(ctx).SearchPeople(ctx, term)
	})
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(found), func(p tmdb.Person) bool { return len(p.KnownFor) == 0 }), nil
}

// Filmography is what someone found by a search is in. Whole says the
// search was their whole name rather than part of it.
type Filmography struct {
	tmdb.Filmography
	Whole bool
}

func (s *Service) filmography(ctx context.Context, personID int64) (tmdb.Filmography, error) {
	return cached(s, fmt.Sprintf("person-%d", personID), listTTL, func() (tmdb.Filmography, error) {
		return s.TMDB(ctx).Credits(ctx, personID)
	})
}

// castOf is the filmography of the actor a search names, if it names one:
// the best-known person TMDB finds whose name has every word of the
// search (or a word starting with it). Anything going wrong just means
// no one.
func (s *Service) castOf(ctx context.Context, term string) Filmography {
	people, err := s.LookupPeople(ctx, term)
	if err != nil || len(people) == 0 {
		return Filmography{}
	}
	p := people[0]
	whole, ok := namesPerson(term, p.Name)
	if !ok || p.Department != "Acting" && !whole {
		return Filmography{}
	}
	f, err := s.filmography(ctx, p.TMDBID)
	if err != nil {
		s.Log.Debug("cast search", "person", p.TMDBID, "err", err)
		return Filmography{}
	}
	return Filmography{Filmography: f, Whole: whole}
}

// namesPerson says whether every word of a search starts a word of
// someone's name, and whether it's their whole name.
func namesPerson(term, name string) (whole, ok bool) {
	tw, nw := words(term), words(name)
	if len(tw) == 0 {
		return false, false
	}
	for _, w := range tw {
		if !slices.ContainsFunc(nw, func(n string) bool { return strings.HasPrefix(n, w) }) {
			return false, false
		}
	}
	return slices.Equal(tw, nw), true
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
