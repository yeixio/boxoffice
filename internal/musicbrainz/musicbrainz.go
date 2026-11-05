// Package musicbrainz looks artists and their albums up on MusicBrainz,
// the open music database, which is where Box Office gets its music list.
//
// MusicBrainz is free and needs no key, but asks every program to name
// itself and to send no more than one request a second; the client does
// both, and waits and tries once more when it's told to slow down.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrNotFound means MusicBrainz doesn't know the artist.
var ErrNotFound = errors.New("not found on MusicBrainz")

// Client calls MusicBrainz's web service.
type Client struct {
	Base      string // https://musicbrainz.org/ws/2, or a test server
	UserAgent string
	HTTP      *http.Client
	// Spacing is the least time between requests; MusicBrainz allows
	// one a second.
	Spacing time.Duration

	mu   sync.Mutex
	last time.Time
}

// New makes a client that names itself as Box Office of this version.
func New(version string) *Client {
	return &Client{
		Base:      "https://musicbrainz.org/ws/2",
		UserAgent: "BoxOffice/" + version + " ( https://github.com/yeixio/boxoffice )",
		HTTP:      &http.Client{Timeout: 20 * time.Second},
		Spacing:   1100 * time.Millisecond,
	}
}

// wait keeps requests at least Spacing apart.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(c.Spacing)
	now := time.Now()
	if next.Before(now) {
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
	q.Set("fmt", "json")
	for try := 0; ; try++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.Base+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", "application/json")
		res, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("couldn't reach MusicBrainz: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if err != nil {
			return fmt.Errorf("couldn't read MusicBrainz's answer: %w", err)
		}
		switch {
		case res.StatusCode == http.StatusServiceUnavailable || res.StatusCode == http.StatusTooManyRequests:
			if try == 0 {
				select {
				case <-time.After(2 * c.Spacing):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return errors.New("MusicBrainz is busy; try again in a minute")
		case res.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case res.StatusCode >= 300:
			return fmt.Errorf("MusicBrainz answered HTTP %d", res.StatusCode)
		}
		return json.Unmarshal(body, out)
	}
}

// Artist is a performer or group.
type Artist struct {
	MBID           string `json:"mbid"`
	Name           string `json:"name"`
	SortName       string `json:"sortName"`
	Type           string `json:"type,omitempty"` // Person, Group, Orchestra...
	Country        string `json:"country,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
	Began          string `json:"began,omitempty"` // a year or date, as MusicBrainz gives it
	Ended          string `json:"ended,omitempty"`
}

type mbArtist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Type           string `json:"type"`
	Country        string `json:"country"`
	Disambiguation string `json:"disambiguation"`
	LifeSpan       struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
	} `json:"life-span"`
}

func (a mbArtist) artist() Artist {
	return Artist{MBID: a.ID, Name: a.Name, SortName: a.SortName, Type: a.Type, Country: a.Country,
		Disambiguation: a.Disambiguation, Began: a.LifeSpan.Begin, Ended: a.LifeSpan.End}
}

// SearchArtists finds artists by name, best match first. "mbid:<id>" or a
// bare MusicBrainz id looks one up directly.
func (c *Client) SearchArtists(ctx context.Context, term string) ([]Artist, error) {
	term = strings.TrimSpace(term)
	if id, ok := strings.CutPrefix(term, "mbid:"); ok || IsMBID(term) {
		if !ok {
			id = term
		}
		a, err := c.Artist(ctx, strings.TrimSpace(id))
		if errors.Is(err, ErrNotFound) {
			return []Artist{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []Artist{a}, nil
	}
	var res struct {
		Artists []mbArtist `json:"artists"`
	}
	if err := c.get(ctx, "/artist", url.Values{"query": {term}, "limit": {"15"}}, &res); err != nil {
		return nil, err
	}
	out := make([]Artist, 0, len(res.Artists))
	for _, a := range res.Artists {
		out = append(out, a.artist())
	}
	return out, nil
}

// Artist looks one artist up by id.
func (c *Client) Artist(ctx context.Context, mbid string) (Artist, error) {
	if !IsMBID(mbid) {
		return Artist{}, ErrNotFound
	}
	var a mbArtist
	if err := c.get(ctx, "/artist/"+mbid, url.Values{}, &a); err != nil {
		return Artist{}, err
	}
	return a.artist(), nil
}

// Album is a release group: an album, EP or single, however many editions
// of it there are.
type Album struct {
	MBID           string   `json:"mbid"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`                     // Album, EP, Single, Broadcast, Other
	SecondaryTypes []string `json:"secondaryTypes,omitempty"` // Live, Compilation, Remix, Soundtrack...
	Released       string   `json:"released,omitempty"`       // YYYY, YYYY-MM or YYYY-MM-DD
	Disambiguation string   `json:"disambiguation,omitempty"`
}

// Studio says whether the album is a plain studio album: one people
// usually want, unlike live records, compilations and singles.
func (a Album) Studio() bool { return a.Type == "Album" && len(a.SecondaryTypes) == 0 }

// Albums lists every release group credited to an artist.
func (c *Client) Albums(ctx context.Context, artist string) ([]Album, error) {
	if !IsMBID(artist) {
		return nil, ErrNotFound
	}
	var out []Album
	for offset := 0; ; {
		var res struct {
			Count  int `json:"release-group-count"`
			Groups []struct {
				ID             string   `json:"id"`
				Title          string   `json:"title"`
				PrimaryType    string   `json:"primary-type"`
				SecondaryTypes []string `json:"secondary-types"`
				FirstRelease   string   `json:"first-release-date"`
				Disambiguation string   `json:"disambiguation"`
			} `json:"release-groups"`
		}
		q := url.Values{"artist": {artist}, "limit": {"100"}, "offset": {fmt.Sprint(offset)}}
		if err := c.get(ctx, "/release-group", q, &res); err != nil {
			return nil, err
		}
		for _, g := range res.Groups {
			t := g.PrimaryType
			if t == "" {
				t = "Other"
			}
			out = append(out, Album{MBID: g.ID, Title: g.Title, Type: t, SecondaryTypes: g.SecondaryTypes,
				Released: g.FirstRelease, Disambiguation: g.Disambiguation})
		}
		offset += len(res.Groups)
		// A few artists have thousands; 1,000 is plenty to choose from.
		if len(res.Groups) == 0 || offset >= res.Count || offset >= 1000 {
			return out, nil
		}
	}
}

// CoverURL is the Cover Art Archive's front cover for a release group, at
// 250 pixels.
func CoverURL(albumMBID string) string {
	return "https://coverartarchive.org/release-group/" + albumMBID + "/front-250"
}

// IsMBID says whether s looks like a MusicBrainz id (a UUID).
func IsMBID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}
