package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Podnapisi (podnapisi.net) is a large subtitle library that needs no
// account, so it works as soon as subtitles are turned on. It's searched
// by title, year, season and episode, one language at a time, and what it
// finds is checked against the title so another film's never comes back.

// PodnapisiBase is the site.
var PodnapisiBase = "https://www.podnapisi.net"

// Podnapisi searches podnapisi.net.
type Podnapisi struct {
	Base      string // PodnapisiBase, or a test server
	UserAgent string
	HTTP      *http.Client
}

func (p *Podnapisi) Name() string { return "podnapisi" }

func (p *Podnapisi) base() string {
	if p.Base != "" {
		return strings.TrimSuffix(p.Base, "/")
	}
	return PodnapisiBase
}

func (p *Podnapisi) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return httpClient
}

type pnResults struct {
	Subtitles []struct {
		PID       string `xml:"pid"`
		Title     string `xml:"title"`
		Year      string `xml:"year"`
		Season    string `xml:"tvSeason"`
		Episode   string `xml:"tvEpisode"`
		Language  string `xml:"language"`
		Flags     string `xml:"flags"`
		Release   string `xml:"release"`
		Downloads string `xml:"downloads"`
	} `xml:"subtitle"`
}

// Search looks for each of the query's languages in turn.
func (p *Podnapisi) Search(ctx context.Context, q Query) ([]Found, error) {
	if q.Title == "" || (!q.Movie && (q.Season <= 0 || q.Episode <= 0)) {
		return nil, nil
	}
	want := simplify(q.Title)
	var out []Found
	for _, lang := range q.Languages {
		v := url.Values{"sXML": {"1"}, "sL": {lang}, "sK": {q.Title}}
		if q.Movie {
			if q.Year > 0 {
				v.Set("sY", strconv.Itoa(q.Year))
			}
		} else {
			v.Set("sTS", strconv.Itoa(q.Season))
			v.Set("sTE", strconv.Itoa(q.Episode))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base()+"/subtitles/search/old?"+v.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", p.UserAgent)
		resp, err := p.client().Do(req)
		if err != nil {
			return out, fmt.Errorf("Podnapisi: %w", err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return out, fmt.Errorf("Podnapisi: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return out, fmt.Errorf("Podnapisi search: %s", resp.Status)
		}
		var res pnResults
		if err := xml.Unmarshal(raw, &res); err != nil {
			return out, fmt.Errorf("Podnapisi: %w", err)
		}
		for _, s := range res.Subtitles {
			year, _ := strconv.Atoi(strings.TrimSpace(s.Year))
			season, _ := strconv.Atoi(strings.TrimSpace(s.Season))
			episode, _ := strconv.Atoi(strings.TrimSpace(s.Episode))
			switch {
			case s.PID == "" || Code(s.Language) != lang || simplify(s.Title) != want:
				continue
			case q.Movie && (season > 0 || episode > 0 || q.Year > 0 && year > 0 && (year < q.Year-1 || year > q.Year+1)):
				continue
			case !q.Movie && (season != q.Season || episode != q.Episode):
				continue
			}
			n, _ := strconv.Atoi(strings.TrimSpace(s.Downloads))
			out = append(out, Found{Provider: "podnapisi", ID: s.PID, Language: lang, Release: strings.Join(strings.Fields(s.Release), " "),
				// Flags are letters: n is for the hearing impaired, f is foreign parts only.
				SDH: strings.Contains(s.Flags, "n"), Forced: strings.Contains(s.Flags, "f"), Downloads: n})
		}
	}
	return out, nil
}

// Download fetches a subtitle, which comes zipped.
func (p *Podnapisi) Download(ctx context.Context, f Found) ([]byte, string, error) {
	data, err := fetch(ctx, p.client(), p.base()+"/subtitles/"+url.PathEscape(f.ID)+"/download?container=zip", p.UserAgent)
	if err != nil {
		return nil, "", fmt.Errorf("Podnapisi: %w", err)
	}
	return fromZip(data)
}

// fromZip takes the subtitle file out of a zip: the one in a format Box
// Office keeps, SubRip first.
func fromZip(data []byte) ([]byte, string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", fmt.Errorf("the download isn't a zip: %w", err)
	}
	var best *zip.File
	rank := func(name string) int { return slices.Index(keptExts, strings.ToLower(path.Ext(name))) }
	for _, zf := range zr.File {
		if r := rank(zf.Name); r >= 0 && zf.UncompressedSize64 <= maxSubtitleSize && (best == nil || r < rank(best.Name)) {
			best = zf
		}
	}
	if best == nil {
		return nil, "", errors.New("there's no subtitle file Box Office can use in the download")
	}
	rc, err := best.Open()
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	out, err := io.ReadAll(io.LimitReader(rc, maxSubtitleSize))
	return out, path.Base(best.Name), err
}

// simplify is a title for comparing: lower case letters and digits, with
// "&" as "and".
func simplify(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", "and")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
