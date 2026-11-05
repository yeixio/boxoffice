// Package search looks for releases on the indexers, decides which are
// right for a title, and hands the best to the downloader.
package search

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Release is one result from an indexer.
type Release struct {
	Title       string    `json:"title"`
	GUID        string    `json:"guid"`
	Link        string    `json:"-"` // where the NZB is; it carries the indexer's key, so it isn't shown
	InfoURL     string    `json:"infoUrl,omitempty"`
	Size        int64     `json:"size"`
	PublishedAt time.Time `json:"publishedAt,omitzero"`
	Grabs       int       `json:"grabs,omitempty"`
	Categories  []int     `json:"categories,omitempty"`
	IndexerID   int64     `json:"indexerId"`
	Indexer     string    `json:"indexer"`
	Protocol    string    `json:"protocol"`
	Password    string    `json:"-"`
	// What the indexer says it's of, when it says (extended results).
	IMDBID string `json:"-"`
	TMDBID int64  `json:"-"`
	TVDBID int64  `json:"-"`
}

// Query is what to search for.
type Query struct {
	Type    string // movie, tvsearch or search
	Text    string
	IMDBID  string
	TMDBID  int64
	TVDBID  int64
	Season  int
	Episode int
	Cats    []int
	Offset  int // to page through results
}

type rss struct {
	Channel struct {
		Limits *struct {
			APICurrent  int `xml:"apicurrent,attr"`
			APIMax      int `xml:"apimax,attr"`
			GrabCurrent int `xml:"grabcurrent,attr"`
			GrabMax     int `xml:"grabmax,attr"`
		} `xml:"apilimits"`
		Items []struct {
			Title     string `xml:"title"`
			GUID      string `xml:"guid"`
			Link      string `xml:"link"`
			Comments  string `xml:"comments"`
			PubDate   string `xml:"pubDate"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
			Categories []string `xml:"category"`
		} `xml:"item"`
	} `xml:"channel"`
}

type apiError struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// Client talks Newznab (and Torznab, its torrent twin) to an indexer.
type Client struct {
	HTTP *http.Client
	// Spacing is the least time between two requests to one indexer, to
	// be kind to it; 0 for none.
	Spacing time.Duration

	mu   sync.Mutex
	next map[string]time.Time // by indexer address
}

// DefaultSpacing is the wait between two requests to one indexer.
const DefaultSpacing = 2 * time.Second

// Limits are an indexer's daily limits and how much of them is used, as it
// reports them (Newznab's apilimits); 0 where it doesn't say.
type Limits struct {
	APIMax, APIUsed, GrabMax, GrabUsed int
}

// IndexerError is an indexer turning a request down or failing.
type IndexerError struct {
	Msg string
	// Limited means the indexer's daily limit is used up, or it asked Box
	// Office to slow down; RetryAfter is when it said to come back, if it
	// said.
	Limited    bool
	RetryAfter time.Duration
}

func (e *IndexerError) Error() string { return e.Msg }

// wait holds a request until the indexer's spacing is over.
func (c *Client) wait(ctx context.Context, ix store.Indexer) error {
	if c.Spacing <= 0 {
		return nil
	}
	c.mu.Lock()
	if c.next == nil {
		c.next = map[string]time.Time{}
	}
	now := time.Now()
	at := c.next[ix.URL]
	if at.Before(now) {
		at = now
	}
	c.next[ix.URL] = at.Add(c.Spacing)
	c.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return nil
}

// retryAfter reads a Retry-After header: seconds, or a date.
func retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(h); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 45 * time.Second}
}

// endpoint is the indexer's API address.
func endpoint(ix store.Indexer) (*url.URL, error) {
	base := strings.TrimRight(ix.URL, "/")
	if base == "" {
		return nil, errors.New("the indexer has no address")
	}
	p := ix.APIPath
	if p == "" {
		p = "/api"
	}
	u, err := url.Parse(base + "/" + strings.TrimLeft(p, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the indexer's address %q isn't a web address", ix.URL)
	}
	return u, nil
}

func (c *Client) get(ctx context.Context, ix store.Indexer, params url.Values) ([]byte, error) {
	u, err := endpoint(ix)
	if err != nil {
		return nil, err
	}
	if ix.APIKey != "" {
		params.Set("apikey", ix.APIKey)
	}
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BoxOffice")
	if err := c.wait(ctx, ix); err != nil {
		return nil, err
	}
	res, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &IndexerError{Msg: fmt.Sprintf("%s can't be reached: %v", ix.Name, scrub(err, ix.APIKey))}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, &IndexerError{Msg: fmt.Sprintf("%s stopped answering: %v", ix.Name, scrub(err, ix.APIKey))}
	}
	if err := statusError(ix, res); err != nil {
		return nil, err
	}
	var ae apiError
	if xml.Unmarshal(body, &ae) == nil && ae.XMLName.Local == "error" {
		return nil, newznabError(ix, ae)
	}
	return body, nil
}

func statusError(ix store.Indexer, res *http.Response) error {
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403:
		return &IndexerError{Msg: fmt.Sprintf("%s turned down the API key", ix.Name)}
	case res.StatusCode == http.StatusTooManyRequests:
		return &IndexerError{Msg: fmt.Sprintf("%s asked Box Office to slow down", ix.Name), Limited: true, RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	case res.StatusCode >= 400:
		return &IndexerError{Msg: fmt.Sprintf("%s answered %s", ix.Name, res.Status), RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	}
	return nil
}

// newznabError explains a Newznab error code: 100 to 102 are the key,
// 500 the daily API limit, 501 the daily download limit.
func newznabError(ix store.Indexer, ae apiError) error {
	switch ae.Code {
	case 100, 101, 102:
		return &IndexerError{Msg: fmt.Sprintf("%s turned down the API key: %s", ix.Name, ae.Description)}
	case 500:
		return &IndexerError{Msg: fmt.Sprintf("%s's daily search limit is used up (%s)", ix.Name, ae.Description), Limited: true}
	case 501:
		return &IndexerError{Msg: fmt.Sprintf("%s's daily download limit is used up (%s)", ix.Name, ae.Description), Limited: true}
	}
	return &IndexerError{Msg: fmt.Sprintf("%s said: %s", ix.Name, ae.Description)}
}

// scrub takes the API key out of an error, which may quote the URL.
func scrub(err error, key string) error {
	if key == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "********"))
}

// Search asks one indexer.
func (c *Client) Search(ctx context.Context, ix store.Indexer, q Query) ([]Release, error) {
	rs, _, err := c.Query(ctx, ix, q)
	return rs, err
}

// Query asks one indexer, and says what it reports of its limits.
func (c *Client) Query(ctx context.Context, ix store.Indexer, q Query) ([]Release, Limits, error) {
	p := url.Values{"t": {q.Type}, "extended": {"1"}, "limit": {"100"}}
	if q.Offset > 0 {
		p.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Text != "" {
		p.Set("q", q.Text)
	}
	if q.IMDBID != "" {
		p.Set("imdbid", strings.TrimPrefix(q.IMDBID, "tt"))
	}
	if q.TMDBID > 0 {
		p.Set("tmdbid", strconv.FormatInt(q.TMDBID, 10))
	}
	if q.TVDBID > 0 {
		p.Set("tvdbid", strconv.FormatInt(q.TVDBID, 10))
	}
	if q.Season > 0 {
		p.Set("season", strconv.Itoa(q.Season))
	}
	if q.Episode > 0 {
		p.Set("ep", strconv.Itoa(q.Episode))
	}
	if len(q.Cats) > 0 {
		var s []string
		for _, c := range q.Cats {
			s = append(s, strconv.Itoa(c))
		}
		p.Set("cat", strings.Join(s, ","))
	}
	body, err := c.get(ctx, ix, p)
	if err != nil {
		return nil, Limits{}, err
	}
	return parseFeed(body, ix)
}

func parseRSS(body []byte, ix store.Indexer) ([]Release, error) {
	rs, _, err := parseFeed(body, ix)
	return rs, err
}

func parseFeed(body []byte, ix store.Indexer) ([]Release, Limits, error) {
	var feed rss
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&feed); err != nil {
		return nil, Limits{}, &IndexerError{Msg: fmt.Sprintf("%s sent something that isn't a list of releases", ix.Name)}
	}
	var lim Limits
	if l := feed.Channel.Limits; l != nil {
		lim = Limits{APIMax: l.APIMax, APIUsed: l.APICurrent, GrabMax: l.GrabMax, GrabUsed: l.GrabCurrent}
	}
	var out []Release
	for _, it := range feed.Channel.Items {
		r := Release{Title: strings.TrimSpace(it.Title), GUID: it.GUID, Link: it.Link, InfoURL: it.Comments, Size: it.Enclosure.Length,
			IndexerID: ix.ID, Indexer: ix.Name, Protocol: ix.Protocol}
		if it.Enclosure.URL != "" {
			r.Link = it.Enclosure.URL
		}
		if t, err := time.Parse(time.RFC1123Z, it.PubDate); err == nil {
			r.PublishedAt = t.UTC()
		} else if t, err := time.Parse(time.RFC1123, it.PubDate); err == nil {
			r.PublishedAt = t.UTC()
		}
		for _, a := range it.Attrs {
			switch a.Name {
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && n > 0 {
					r.Size = n
				}
			case "grabs":
				r.Grabs, _ = strconv.Atoi(a.Value)
			case "category":
				if n, err := strconv.Atoi(a.Value); err == nil {
					r.Categories = append(r.Categories, n)
				}
			case "password":
				if a.Value != "0" && a.Value != "1" {
					r.Password = a.Value
				}
			case "usenetdate":
				if t, err := time.Parse(time.RFC1123Z, a.Value); err == nil {
					r.PublishedAt = t.UTC()
				}
			case "imdb", "imdbid":
				if v := strings.TrimPrefix(strings.TrimSpace(a.Value), "tt"); v != "" && v != "0" {
					r.IMDBID = "tt" + v
				}
			case "tmdbid":
				r.TMDBID, _ = strconv.ParseInt(a.Value, 10, 64)
			case "tvdbid":
				r.TVDBID, _ = strconv.ParseInt(a.Value, 10, 64)
			}
		}
		if r.GUID == "" {
			r.GUID = r.Link
		}
		if r.Title != "" && r.Link != "" {
			out = append(out, r)
		}
	}
	return out, lim, nil
}

// Category is one of an indexer's categories.
type Category struct {
	ID   int        `json:"id"`
	Name string     `json:"name"`
	Subs []Category `json:"subs,omitempty"`
}

// Caps is what an indexer says it can do.
type Caps struct {
	Categories  []Category `json:"categories"`
	MovieSearch bool       `json:"movieSearch"`
	TVSearch    bool       `json:"tvSearch"`
}

// Caps asks an indexer what it can do: the way to check its address and
// key, and to find its categories.
func (c *Client) Caps(ctx context.Context, ix store.Indexer) (Caps, error) {
	body, err := c.get(ctx, ix, url.Values{"t": {"caps"}})
	if err != nil {
		return Caps{}, err
	}
	var x struct {
		XMLName   xml.Name
		Searching struct {
			Movie struct {
				Available string `xml:"available,attr"`
			} `xml:"movie-search"`
			TV struct {
				Available string `xml:"available,attr"`
			} `xml:"tv-search"`
		} `xml:"searching"`
		Categories []struct {
			ID   int    `xml:"id,attr"`
			Name string `xml:"name,attr"`
			Subs []struct {
				ID   int    `xml:"id,attr"`
				Name string `xml:"name,attr"`
			} `xml:"subcat"`
		} `xml:"categories>category"`
	}
	if err := xml.Unmarshal(body, &x); err != nil || x.XMLName.Local != "caps" {
		return Caps{}, fmt.Errorf("%s doesn't answer like a Newznab or Torznab indexer; check the address and API path", ix.Name)
	}
	caps := Caps{MovieSearch: x.Searching.Movie.Available == "yes", TVSearch: x.Searching.TV.Available == "yes"}
	for _, cat := range x.Categories {
		cc := Category{ID: cat.ID, Name: cat.Name}
		for _, s := range cat.Subs {
			cc.Subs = append(cc.Subs, Category{ID: s.ID, Name: s.Name})
		}
		caps.Categories = append(caps.Categories, cc)
	}
	return caps, nil
}

// FetchNZB downloads a release's NZB.
func (c *Client) FetchNZB(ctx context.Context, ix store.Indexer, r Release) ([]byte, error) {
	u, err := url.Parse(r.Link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("the release has no NZB link")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BoxOffice")
	if err := c.wait(ctx, ix); err != nil {
		return nil, err
	}
	res, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &IndexerError{Msg: fmt.Sprintf("%s can't be reached: %v", ix.Name, scrub(err, ix.APIKey))}
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		if err := statusError(ix, res); res.StatusCode == http.StatusTooManyRequests {
			return nil, err
		}
		return nil, fmt.Errorf("%s wouldn't give the NZB: %s", ix.Name, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	// An indexer out of downloads for the day answers with an error, not
	// an NZB.
	if len(data) < 4096 {
		var ae apiError
		if xml.Unmarshal(data, &ae) == nil && ae.XMLName.Local == "error" {
			return nil, newznabError(ix, ae)
		}
	}
	return data, nil
}
