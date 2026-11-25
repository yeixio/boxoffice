package catalog

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/tmdb"
)

// ErrNoKind means a title kind other than movie or series.
var ErrNoKind = errors.New("kind is movie or series")

// WhereToWatch is where a movie or show can be watched in one country:
// so someone who'd rather not wait for a download can play it on a service
// they already have. The networks a show is made for come first; they're
// the same everywhere.
type WhereToWatch struct {
	Networks []tmdb.Network `json:"networks"`
	// Country is whose offers these are, such as "US".
	Country string `json:"country"`
	tmdb.Offers
	// Countries are every country with any way to watch it, to pick from.
	Countries []string `json:"countries"`
}

// Watch says where a title (kind movie or series, by TMDB id) can be
// watched in country, or in tmdb.Country when that's empty. TMDB's answer
// covers every country and changes slowly, so it's kept a day.
func (s *Service) Watch(ctx context.Context, kind string, tmdbID int64, country string) (WhereToWatch, error) {
	if kind != tmdb.KindMovie && kind != tmdb.KindSeries {
		return WhereToWatch{}, ErrNoKind
	}
	c := s.TMDB(ctx)
	w, err := cached(s, "watch:"+kind+":"+strconv.FormatInt(tmdbID, 10), browseTTL, func() (tmdb.Watch, error) {
		return c.WhereToWatch(ctx, kind, tmdbID)
	})
	if err != nil {
		return WhereToWatch{}, err
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		country = tmdb.Country
	}
	out := WhereToWatch{Networks: w.Networks, Country: country, Offers: w.Countries[country], Countries: []string{}}
	if out.Networks == nil {
		out.Networks = []tmdb.Network{}
	}
	for code := range w.Countries {
		out.Countries = append(out.Countries, code)
	}
	slices.Sort(out.Countries)
	return out, nil
}
