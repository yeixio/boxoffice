// Package wikidata asks Wikidata, the free knowledge base behind
// Wikipedia, about authors: their photo, and which of their books belong
// to a series and in what order. Box Office uses it where Open Library
// has no photo or no series for a book.
//
// Wikidata's query service is free and needs no key. It asks programs to
// name themselves and not to hammer it, so the client sends a User-Agent
// and keeps its requests apart.
package wikidata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client calls the Wikidata query service.
type Client struct {
	Base      string // https://query.wikidata.org, or a test server
	UserAgent string
	HTTP      *http.Client
	Spacing   time.Duration

	mu   sync.Mutex
	last time.Time
}

// New makes a client that names itself as Box Office of this version.
func New(version string) *Client {
	return &Client{
		Base:      "https://query.wikidata.org",
		UserAgent: "BoxOffice/" + version + " ( https://github.com/yeixio/boxoffice )",
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		Spacing:   time.Second,
	}
}

var reItem = regexp.MustCompile(`^Q\d+$`)

// IsItem says whether s is a Wikidata item id, like Q892.
func IsItem(s string) bool { return reItem.MatchString(s) }

type binding map[string]struct {
	Value string `json:"value"`
}

func (c *Client) query(ctx context.Context, sparql string) ([]binding, error) {
	c.mu.Lock()
	next := c.last.Add(c.Spacing)
	if now := time.Now(); next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	select {
	case <-time.After(time.Until(next)):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/sparql?"+url.Values{"query": {sparql}, "format": {"json"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/sparql-results+json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Wikidata: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("Wikidata answered HTTP %d", res.StatusCode)
	}
	var out struct {
		Results struct {
			Bindings []binding `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("couldn't read Wikidata's answer: %w", err)
	}
	return out.Results.Bindings, nil
}

// Photo is a picture of a person from Wikimedia Commons, or "" when
// Wikidata has none.
func (c *Client) Photo(ctx context.Context, item string) (string, error) {
	if !IsItem(item) {
		return "", nil
	}
	rows, err := c.query(ctx, "SELECT ?img WHERE { wd:"+item+" wdt:P18 ?img } LIMIT 1")
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return commonsThumb(rows[0]["img"].Value), nil
}

// commonsThumb turns a Commons file link into a small picture of it.
func commonsThumb(u string) string {
	const prefix = "commons.wikimedia.org/wiki/Special:FilePath/"
	i := strings.Index(u, prefix)
	if i < 0 {
		return ""
	}
	return "https://" + u[i:] + "?width=300"
}

// Part is one of an author's books that's part of a series.
type Part struct {
	Title    string // the book's English name on Wikidata
	OLWork   string // its Open Library work, OL27513W, when Wikidata knows it
	Series   string // the series' English name
	SeriesID string // Q15228
	Position string // "1", or "" when Wikidata doesn't say
}

// Series lists an author's books that Wikidata says are part of a series.
// A book in more than one series is listed once for each.
func (c *Client) Series(ctx context.Context, author string) ([]Part, error) {
	if !IsItem(author) {
		return nil, nil
	}
	rows, err := c.query(ctx, `SELECT ?work ?workLabel ?series ?seriesLabel ?ord ?olid WHERE {
  ?work wdt:P50 wd:`+author+`; p:P179 ?st. ?st ps:P179 ?series.
  OPTIONAL { ?st pq:P1545 ?ord }
  OPTIONAL { ?work wdt:P648 ?olid }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "en". }
} LIMIT 3000`)
	if err != nil {
		return nil, err
	}
	out := make([]Part, 0, len(rows))
	for _, r := range rows {
		series := entity(r["series"].Value)
		name := strings.TrimSpace(r["seriesLabel"].Value)
		// With no English name, the label is the item id.
		if series == "" || name == "" || IsItem(name) {
			continue
		}
		title := strings.TrimSpace(r["workLabel"].Value)
		if IsItem(title) {
			title = ""
		}
		olid := r["olid"].Value
		if !strings.HasSuffix(olid, "W") {
			olid = "" // an edition or author id put in the wrong place
		}
		out = append(out, Part{Title: title, OLWork: olid, Series: name, SeriesID: series, Position: strings.TrimSpace(r["ord"].Value)})
	}
	return out, nil
}

// entity is the item id at the end of an entity link.
func entity(u string) string {
	id := u[strings.LastIndex(u, "/")+1:]
	if !IsItem(id) {
		return ""
	}
	return id
}
