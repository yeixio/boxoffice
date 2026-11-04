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
}

type rss struct {
	Channel struct {
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
	res, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s can't be reached: %w", ix.Name, scrub(err, ix.APIKey))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, fmt.Errorf("%s turned down the API key", ix.Name)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("%s answered %s", ix.Name, res.Status)
	}
	var ae apiError
	if xml.Unmarshal(body, &ae) == nil && ae.XMLName.Local == "error" {
		switch ae.Code {
		case 100, 101, 102:
			return nil, fmt.Errorf("%s turned down the API key: %s", ix.Name, ae.Description)
		}
		return nil, fmt.Errorf("%s said: %s", ix.Name, ae.Description)
	}
	return body, nil
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
	p := url.Values{"t": {q.Type}, "extended": {"1"}, "limit": {"100"}}
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
		return nil, err
	}
	return parseRSS(body, ix)
}

func parseRSS(body []byte, ix store.Indexer) ([]Release, error) {
	var feed rss
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("%s sent something that isn't a list of releases", ix.Name)
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
			}
		}
		if r.GUID == "" {
			r.GUID = r.Link
		}
		if r.Title != "" && r.Link != "" {
			out = append(out, r)
		}
	}
	return out, nil
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
	res, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s can't be reached: %w", ix.Name, scrub(err, ix.APIKey))
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("%s wouldn't give the NZB: %s", ix.Name, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}
