package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"slices"
)

// Kinds of episode order, as TMDB numbers its episode groups' types.
const (
	OrderAired      = 1
	OrderAbsolute   = 2
	OrderDVD        = 3
	OrderDigital    = 4
	OrderStoryArc   = 5
	OrderProduction = 6
	OrderTV         = 7
)

// Order is one of the other orders TMDB lists a show's episodes in (an
// episode group): the DVD sets' order, say, or one straight count.
type Order struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
	Seasons  int    `json:"seasons"`
	Episodes int    `json:"episodes"`
}

// Orders lists a show's other episode orders, the biggest of each kind
// first.
func (c *Client) Orders(ctx context.Context, showID int64) ([]Order, error) {
	var res struct {
		Results []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Type     int    `json:"type"`
			Groups   int    `json:"group_count"`
			Episodes int    `json:"episode_count"`
		} `json:"results"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/episode_groups", showID), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Order, 0, len(res.Results))
	for _, g := range res.Results {
		if g.ID != "" && g.Episodes > 0 {
			out = append(out, Order{ID: g.ID, Name: g.Name, Kind: g.Type, Seasons: g.Groups, Episodes: g.Episodes})
		}
	}
	slices.SortStableFunc(out, func(a, b Order) int {
		if a.Kind != b.Kind {
			return a.Kind - b.Kind
		}
		return b.Episodes - a.Episodes
	})
	return out, nil
}

// OrderPlace is one episode's place in an order: the season and number
// the order gives it, and the ones it aired as.
type OrderPlace struct {
	Season, Number           int
	AiredSeason, AiredNumber int
}

// OrderPlaces lists the episodes of an order. Each of its groups is a
// season, numbered from 1 in the order's own sequence; a group of
// nothing but specials is season 0.
func (c *Client) OrderPlaces(ctx context.Context, id string) ([]OrderPlace, error) {
	type episode struct {
		Season int `json:"season_number"`
		Number int `json:"episode_number"`
		Order  int `json:"order"`
	}
	var res struct {
		Groups []struct {
			Order    int       `json:"order"`
			Episodes []episode `json:"episodes"`
		} `json:"groups"`
	}
	if err := c.get(ctx, "/tv/episode_group/"+url.PathEscape(id), nil, &res); err != nil {
		return nil, err
	}
	slices.SortStableFunc(res.Groups, func(a, b struct {
		Order    int       `json:"order"`
		Episodes []episode `json:"episodes"`
	}) int {
		return a.Order - b.Order
	})
	var out []OrderPlace
	season, specials := 0, 0
	for _, g := range res.Groups {
		if len(g.Episodes) == 0 {
			continue
		}
		eps := g.Episodes
		slices.SortStableFunc(eps, func(a, b episode) int { return a.Order - b.Order })
		if !slices.ContainsFunc(eps, func(e episode) bool { return e.Season != 0 }) {
			for _, e := range eps {
				specials++
				out = append(out, OrderPlace{Season: 0, Number: specials, AiredSeason: e.Season, AiredNumber: e.Number})
			}
			continue
		}
		season++
		for i, e := range eps {
			out = append(out, OrderPlace{Season: season, Number: i + 1, AiredSeason: e.Season, AiredNumber: e.Number})
		}
	}
	return out, nil
}
