package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Where a title can be watched, so people can play it on a service they
// already pay for: the networks a show is made for, and the streaming
// services, rentals and shops that have it in each country. TMDB gets the
// services from JustWatch, and asks that JustWatch is named wherever
// they're shown.

// Provider is a streaming service or shop, such as Netflix or Apple TV.
type Provider struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
}

// Offers are the ways to watch a title in one country.
type Offers struct {
	// Link is TMDB's page of where to watch it there, which leads on to
	// each service.
	Link string `json:"link,omitempty"`
	// Stream is on a subscription; Free costs nothing (some with ads);
	// Rent and Buy are paid for one title at a time.
	Stream []Provider `json:"stream,omitempty"`
	Free   []Provider `json:"free,omitempty"`
	Rent   []Provider `json:"rent,omitempty"`
	Buy    []Provider `json:"buy,omitempty"`
}

// Empty says there's no way to watch it there.
func (o Offers) Empty() bool {
	return len(o.Stream)+len(o.Free)+len(o.Rent)+len(o.Buy) == 0
}

// Watch is where a title can be watched.
type Watch struct {
	// Networks are what a show is made for or first airs on, such as HBO
	// or Netflix. Movies have none.
	Networks []Network `json:"networks,omitempty"`
	// Countries are the offers by country code, such as "US".
	Countries map[string]Offers `json:"countries"`
}

type apiProvider struct {
	ID       int64  `json:"provider_id"`
	Name     string `json:"provider_name"`
	LogoPath string `json:"logo_path"`
	Priority int    `json:"display_priority"`
}

type apiOffers struct {
	Link     string        `json:"link"`
	Flatrate []apiProvider `json:"flatrate"`
	Free     []apiProvider `json:"free"`
	Ads      []apiProvider `json:"ads"`
	Rent     []apiProvider `json:"rent"`
	Buy      []apiProvider `json:"buy"`
}

type apiWatch struct {
	Results map[string]apiOffers `json:"results"`
}

// providers puts TMDB's lists together in JustWatch's order, each service
// once.
func providers(lists ...[]apiProvider) []Provider {
	var all []apiProvider
	for _, l := range lists {
		all = append(all, l...)
	}
	slices.SortStableFunc(all, func(a, b apiProvider) int { return a.Priority - b.Priority })
	seen := map[int64]bool{}
	var out []Provider
	for _, p := range all {
		if seen[p.ID] || p.Name == "" {
			continue
		}
		seen[p.ID] = true
		out = append(out, Provider{ID: p.ID, Name: p.Name, Logo: Image(p.LogoPath, "w92")})
	}
	return out
}

func (w apiWatch) countries() map[string]Offers {
	out := make(map[string]Offers, len(w.Results))
	for code, o := range w.Results {
		offers := Offers{Link: o.Link, Stream: providers(o.Flatrate), Free: providers(o.Free, o.Ads), Rent: providers(o.Rent), Buy: providers(o.Buy)}
		if offers.Empty() {
			continue
		}
		out[strings.ToUpper(code)] = offers
	}
	return out
}

// WhereToWatch is where a movie or show (kind) can be watched: in every
// country at once, since TMDB answers with them all.
func (c *Client) WhereToWatch(ctx context.Context, kind string, id int64) (Watch, error) {
	if kind == KindSeries {
		var res struct {
			Networks []struct {
				ID       int64  `json:"id"`
				Name     string `json:"name"`
				LogoPath string `json:"logo_path"`
			} `json:"networks"`
			Watch apiWatch `json:"watch/providers"`
		}
		if err := c.get(ctx, fmt.Sprintf("/tv/%d", id), url.Values{"append_to_response": {"watch/providers"}}, &res); err != nil {
			return Watch{}, err
		}
		w := Watch{Countries: res.Watch.countries()}
		for _, n := range res.Networks {
			w.Networks = append(w.Networks, Network{ID: n.ID, Name: n.Name, Logo: Image(n.LogoPath, "w300")})
		}
		return w, nil
	}
	var res apiWatch
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/watch/providers", id), nil, &res); err != nil {
		return Watch{}, err
	}
	return Watch{Countries: res.countries()}, nil
}
