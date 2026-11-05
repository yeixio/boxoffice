// Package openlibrary looks authors and their books up on Open Library,
// the Internet Archive's open catalogue of books, which is where Box Office
// gets its list of books for ebooks and audiobooks alike.
//
// Open Library is free and needs no key. It asks programs to name
// themselves and not to hammer it, so the client sends a User-Agent and
// keeps its requests apart.
package openlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNotFound means Open Library doesn't know the author.
var ErrNotFound = errors.New("not found on Open Library")

// Client calls Open Library.
type Client struct {
	Base      string // https://openlibrary.org, or a test server
	UserAgent string
	HTTP      *http.Client
	Spacing   time.Duration

	mu   sync.Mutex
	last time.Time
}

// New makes a client that names itself as Box Office of this version.
func New(version string) *Client {
	return &Client{
		Base:      "https://openlibrary.org",
		UserAgent: "BoxOffice/" + version + " ( https://github.com/yeixio/boxoffice )",
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		Spacing:   time.Second,
	}
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(c.Spacing)
	if now := time.Now(); next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	select {
	case <-time.After(time.Until(next)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	for try := 0; ; try++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		u := c.Base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", "application/json")
		res, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("couldn't reach Open Library: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		if err != nil {
			return fmt.Errorf("couldn't read Open Library's answer: %w", err)
		}
		switch {
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
			if try == 0 {
				select {
				case <-time.After(3 * c.Spacing):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return fmt.Errorf("Open Library is busy (HTTP %d); try again in a minute", res.StatusCode)
		case res.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case res.StatusCode >= 300:
			return fmt.Errorf("Open Library answered HTTP %d", res.StatusCode)
		}
		return json.Unmarshal(body, out)
	}
}

var reAuthorKey = regexp.MustCompile(`^OL\d+A$`)

// IsAuthorKey says whether s is an Open Library author key, like OL23919A.
func IsAuthorKey(s string) bool { return reAuthorKey.MatchString(s) }

// Author is a writer.
type Author struct {
	Key     string `json:"key"` // OL23919A
	Name    string `json:"name"`
	Born    string `json:"born,omitempty"`
	Died    string `json:"died,omitempty"`
	TopWork string `json:"topWork,omitempty"`
	Works   int    `json:"works,omitempty"`
	Bio     string `json:"bio,omitempty"`
	Photo   string `json:"photo,omitempty"`
}

// SearchAuthors finds authors by name. An author key (OL23919A) looks
// one up directly.
func (c *Client) SearchAuthors(ctx context.Context, term string) ([]Author, error) {
	term = strings.TrimSpace(term)
	if k := strings.ToUpper(term); IsAuthorKey(k) {
		a, err := c.Author(ctx, k)
		if errors.Is(err, ErrNotFound) {
			return []Author{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []Author{a}, nil
	}
	var res struct {
		Docs []struct {
			Key       string `json:"key"`
			Name      string `json:"name"`
			BirthDate string `json:"birth_date"`
			DeathDate string `json:"death_date"`
			TopWork   string `json:"top_work"`
			WorkCount int    `json:"work_count"`
		} `json:"docs"`
	}
	if err := c.get(ctx, "/search/authors.json", url.Values{"q": {term}, "limit": {"15"}}, &res); err != nil {
		return nil, err
	}
	out := make([]Author, 0, len(res.Docs))
	for _, d := range res.Docs {
		out = append(out, Author{Key: d.Key, Name: d.Name, Born: d.BirthDate, Died: d.DeathDate, TopWork: d.TopWork, Works: d.WorkCount,
			Photo: photoURL(d.Key)})
	}
	return out, nil
}

// Author looks one author up.
func (c *Client) Author(ctx context.Context, key string) (Author, error) {
	if !IsAuthorKey(key) {
		return Author{}, ErrNotFound
	}
	var a struct {
		Name      string          `json:"name"`
		BirthDate string          `json:"birth_date"`
		DeathDate string          `json:"death_date"`
		Bio       json.RawMessage `json:"bio"`
	}
	if err := c.get(ctx, "/authors/"+key+".json", nil, &a); err != nil {
		return Author{}, err
	}
	return Author{Key: key, Name: a.Name, Born: a.BirthDate, Died: a.DeathDate, Bio: text(a.Bio), Photo: photoURL(key)}, nil
}

// text reads a field Open Library gives either as a string or as
// {"type": "/type/text", "value": "..."}.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v struct {
		Value string `json:"value"`
	}
	json.Unmarshal(raw, &v)
	return v.Value
}

// Book is a work: one book, however many editions and formats of it there
// are.
type Book struct {
	Key      string `json:"key"` // OL45804W
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Year     int    `json:"year,omitempty"`
	CoverID  int64  `json:"coverId,omitempty"`
	Editions int    `json:"editions"`
}

// MaxBooks is the most books listed for one author; a few prolific ones
// have thousands of entries, most of them collections and translations.
const MaxBooks = 500

// Books lists an author's books, those with the most editions first: the
// books people know an author for come first, and the oddments last.
func (c *Client) Books(ctx context.Context, author string) ([]Book, error) {
	if !IsAuthorKey(author) {
		return nil, ErrNotFound
	}
	var out []Book
	for offset := 0; ; {
		var res struct {
			NumFound int `json:"numFound"`
			Docs     []struct {
				Key          string `json:"key"`
				Title        string `json:"title"`
				Subtitle     string `json:"subtitle"`
				FirstPublish int    `json:"first_publish_year"`
				CoverI       int64  `json:"cover_i"`
				EditionCount int    `json:"edition_count"`
			} `json:"docs"`
		}
		q := url.Values{"q": {"author_key:" + author}, "fields": {"key,title,subtitle,first_publish_year,cover_i,edition_count"},
			"sort": {"editions"}, "limit": {"100"}, "offset": {fmt.Sprint(offset)}}
		if err := c.get(ctx, "/search.json", q, &res); err != nil {
			return nil, err
		}
		for _, d := range res.Docs {
			out = append(out, Book{Key: strings.TrimPrefix(d.Key, "/works/"), Title: d.Title, Subtitle: d.Subtitle,
				Year: d.FirstPublish, CoverID: d.CoverI, Editions: d.EditionCount})
		}
		offset += len(res.Docs)
		if len(res.Docs) == 0 || offset >= res.NumFound || offset >= MaxBooks {
			return out, nil
		}
	}
}

// CoverURL is a book cover from Open Library's cover store, medium size.
func CoverURL(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", id)
}

func photoURL(key string) string {
	return "https://covers.openlibrary.org/a/olid/" + key + "-M.jpg?default=false"
}
