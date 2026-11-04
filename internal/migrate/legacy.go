package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// legacy reads from a Radarr or Sonarr. It only ever reads, apart from
// asking the app to make a backup of itself.
type legacy struct {
	app  string // radarr or sonarr
	base *url.URL
	key  string
	http *http.Client
}

func normalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("enter the address")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q isn't an http(s) address", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u, nil
}

func newLegacy(app, rawURL, key string) (*legacy, error) {
	if app != "radarr" && app != "sonarr" {
		return nil, fmt.Errorf("Box Office can't migrate from %q yet", app)
	}
	u, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("enter the API key (in the app under Settings → General)")
	}
	return &legacy{app: app, base: u, key: strings.TrimSpace(key), http: &http.Client{
		Timeout: 2 * time.Minute,
		// The key would go along with a redirect, maybe to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (l *legacy) name() string {
	if l.app == "radarr" {
		return "Radarr"
	}
	return "Sonarr"
}

func (l *legacy) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	err := l.try(ctx, method, path, q, body, out)
	var rd *redirectError
	if errors.As(err, &rd) && rd.base != nil {
		// The app runs under a URL base, such as /radarr, and points there.
		l.base = rd.base
		return l.try(ctx, method, path, q, body, out)
	}
	return err
}

// redirectError is the app sending a request elsewhere. base is set when
// it's the same app at another URL base on the same host.
type redirectError struct {
	msg  string
	base *url.URL
}

func (e *redirectError) Error() string { return e.msg }

// redirectBase works out the URL base a redirect points at: the same host,
// and the same request under another path, as an app with a URL base
// answers a request without it.
func redirectBase(from *url.URL, location, path string) (*url.URL, string) {
	to, err := from.Parse(location)
	if err != nil {
		return nil, location
	}
	to.RawQuery, to.Fragment = "", ""
	if to.Scheme != from.Scheme || to.Host != from.Host || !strings.HasSuffix(to.Path, path) {
		return nil, to.Redacted()
	}
	base := *to
	base.Path = strings.TrimRight(strings.TrimSuffix(to.Path, path), "/")
	if base.Path == strings.TrimRight(strings.TrimSuffix(from.Path, path), "/") {
		return nil, base.Redacted()
	}
	return &base, base.Redacted()
}

func (l *legacy) try(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := *l.base
	u.Path += path
	u.RawQuery = q.Encode()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", l.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := l.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("couldn't reach %s at %s: %w", l.name(), l.base.Redacted(), err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403:
		return fmt.Errorf("%s didn't accept the API key", l.name())
	case res.StatusCode >= 300 && res.StatusCode < 400:
		base, to := redirectBase(req.URL, res.Header.Get("Location"), path)
		return &redirectError{base: base, msg: fmt.Sprintf("%s at %s sends Box Office to %s instead. Enter that address.", l.name(), l.base.Redacted(), to)}
	case res.StatusCode >= 300:
		return fmt.Errorf("%s answered HTTP %d for %s", l.name(), res.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 256<<20)).Decode(out); err != nil {
		if strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			return fmt.Errorf("%s doesn't look like %s's address", l.base.Redacted(), l.name())
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

func (l *legacy) get(ctx context.Context, path string, q url.Values, out any) error {
	return l.do(ctx, "GET", "/api/v3"+path, q, nil, out)
}

type status struct {
	AppName   string `json:"appName"`
	Version   string `json:"version"`
	BoxOffice string `json:"boxOffice"`
}

func (l *legacy) status(ctx context.Context) (status, error) {
	var s status
	if err := l.get(ctx, "/system/status", nil, &s); err != nil {
		return s, err
	}
	if !strings.EqualFold(s.AppName, l.name()) {
		if s.AppName == "" {
			return s, fmt.Errorf("%s doesn't look like %s's address", l.base.Redacted(), l.name())
		}
		return s, fmt.Errorf("that address is %s, not %s", s.AppName, l.name())
	}
	if s.BoxOffice != "" {
		return s, errors.New("that address is Box Office itself")
	}
	return s, nil
}

type lQuality struct {
	Quality struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"quality"`
}

type lProfileItem struct {
	ID      int            `json:"id"` // set on groups
	Name    string         `json:"name"`
	Allowed bool           `json:"allowed"`
	Items   []lProfileItem `json:"items"`
	Quality *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"quality"`
}

type lProfile struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	UpgradeAllowed bool           `json:"upgradeAllowed"`
	Cutoff         int            `json:"cutoff"`
	Items          []lProfileItem `json:"items"`
}

type lRoot struct {
	Path string `json:"path"`
}

type lImage struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
}

func pickImage(images []lImage, kind string) string {
	for _, im := range images {
		if im.CoverType == kind && strings.HasPrefix(im.RemoteURL, "https://") {
			return im.RemoteURL
		}
	}
	return ""
}

type lMovie struct {
	ID               int        `json:"id"`
	TMDBID           int64      `json:"tmdbId"`
	IMDBID           string     `json:"imdbId"`
	Title            string     `json:"title"`
	Year             int        `json:"year"`
	Overview         string     `json:"overview"`
	Runtime          int        `json:"runtime"`
	Images           []lImage   `json:"images"`
	InCinemas        *time.Time `json:"inCinemas"`
	DigitalRelease   *time.Time `json:"digitalRelease"`
	PhysicalRelease  *time.Time `json:"physicalRelease"`
	Monitored        bool       `json:"monitored"`
	QualityProfileID int        `json:"qualityProfileId"`
	Path             string     `json:"path"`
	RootFolderPath   string     `json:"rootFolderPath"`
	HasFile          bool       `json:"hasFile"`
	MovieFile        *struct {
		Path    string   `json:"path"`
		Size    int64    `json:"size"`
		Quality lQuality `json:"quality"`
	} `json:"movieFile"`
}

type lSeries struct {
	ID               int      `json:"id"`
	TVDBID           int64    `json:"tvdbId"`
	TMDBID           int64    `json:"tmdbId"`
	IMDBID           string   `json:"imdbId"`
	Title            string   `json:"title"`
	Year             int      `json:"year"`
	Overview         string   `json:"overview"`
	Status           string   `json:"status"`
	Network          string   `json:"network"`
	Runtime          int      `json:"runtime"`
	Images           []lImage `json:"images"`
	Monitored        bool     `json:"monitored"`
	QualityProfileID int      `json:"qualityProfileId"`
	Path             string   `json:"path"`
	RootFolderPath   string   `json:"rootFolderPath"`
	Seasons          []struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
	} `json:"seasons"`
}

type lEpisode struct {
	SeasonNumber  int        `json:"seasonNumber"`
	EpisodeNumber int        `json:"episodeNumber"`
	Title         string     `json:"title"`
	Overview      string     `json:"overview"`
	AirDateUTC    *time.Time `json:"airDateUtc"`
	Monitored     bool       `json:"monitored"`
	EpisodeFileID int        `json:"episodeFileId"`
}

type lEpisodeFile struct {
	ID      int      `json:"id"`
	Path    string   `json:"path"`
	Size    int64    `json:"size"`
	Quality lQuality `json:"quality"`
}

// backup has the app make a backup of itself, then downloads it.
func (l *legacy) backup(ctx context.Context) (name string, data []byte, err error) {
	var cmd struct {
		ID int `json:"id"`
	}
	if err := l.do(ctx, "POST", "/api/v3/command", nil, map[string]string{"name": "Backup"}, &cmd); err != nil {
		return "", nil, fmt.Errorf("asking %s for a backup: %w", l.name(), err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		var st struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := l.get(ctx, "/command/"+strconv.Itoa(cmd.ID), nil, &st); err != nil {
			return "", nil, err
		}
		if st.Status == "completed" {
			break
		}
		if st.Status == "failed" || st.Status == "aborted" {
			return "", nil, fmt.Errorf("%s couldn't make a backup: %s", l.name(), st.Message)
		}
		if time.Now().After(deadline) {
			return "", nil, fmt.Errorf("%s took too long to make a backup", l.name())
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	var list []struct {
		Name string    `json:"name"`
		Path string    `json:"path"`
		Type string    `json:"type"`
		Time time.Time `json:"time"`
	}
	if err := l.get(ctx, "/system/backup", nil, &list); err != nil {
		return "", nil, err
	}
	var newest int = -1
	for i, b := range list {
		if b.Type == "manual" && (newest < 0 || b.Time.After(list[newest].Time)) {
			newest = i
		}
	}
	if newest < 0 {
		return "", nil, fmt.Errorf("%s made a backup but doesn't list it", l.name())
	}
	b := list[newest]
	u := *l.base
	u.Path += "/" + strings.TrimLeft(b.Path, "/")
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("X-Api-Key", l.key)
	res, err := l.http.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("downloading %s's backup: %w", l.name(), err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", nil, fmt.Errorf("downloading %s's backup: HTTP %d", l.name(), res.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, 4<<30))
	if err != nil {
		return "", nil, err
	}
	if len(data) < 4 || string(data[:2]) != "PK" {
		return "", nil, fmt.Errorf("%s's backup didn't download as a zip file", l.name())
	}
	return l.app + "-" + b.Name, data, nil
}
