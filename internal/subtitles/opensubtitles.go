package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenSubtitles (opensubtitles.com) is the largest library of subtitles.
// Its API needs a free API key, and a free account raises how many files
// can be downloaded a day (to 20; VIP accounts get 1,000). Matinee uses the
// same API for its subtitle search, so the same key works for both.

// OpenSubtitlesBase is the API.
var OpenSubtitlesBase = "https://api.opensubtitles.com/api/v1"

var (
	// ErrBadKey means OpenSubtitles turned the API key down.
	ErrBadKey = errors.New("OpenSubtitles didn't accept the API key")
	// ErrBadLogin means OpenSubtitles turned the username or password down.
	ErrBadLogin = errors.New("OpenSubtitles didn't accept the username or password")
)

// OpenSubtitles searches opensubtitles.com and fetches its files.
type OpenSubtitles struct {
	APIKey    string
	Username  string
	Password  string
	Base      string // OpenSubtitlesBase, or a test server
	UserAgent string // "BoxOffice v1.2.3": the API asks every app to name itself
	HTTP      *http.Client

	mu      sync.Mutex
	token   string
	tokenAt time.Time
	// base is where a signed-in account's requests go; VIP accounts get
	// their own server.
	base string
}

// tokenLife is how long a sign-in is used before signing in again.
// Signing in is limited to about one a second, so it isn't done per request.
const tokenLife = 12 * time.Hour

func (o *OpenSubtitles) Name() string { return "opensubtitles" }

// apiLanguages are the API's codes for a language: it splits Portuguese
// and Chinese in two.
func apiLanguages(code string) []string {
	switch code {
	case "pt":
		return []string{"pt-br", "pt-pt"}
	case "zh":
		return []string{"zh-cn", "zh-tw"}
	case "nb":
		return []string{"no"}
	}
	return []string{code}
}

// Search finds subtitles for a video: by the file's hash, and by its
// TMDB (or IMDb) id.
func (o *OpenSubtitles) Search(ctx context.Context, q Query) ([]Found, error) {
	var langs []string
	for _, l := range q.Languages {
		for _, c := range apiLanguages(l) {
			if !slices.Contains(langs, c) {
				langs = append(langs, c)
			}
		}
	}
	slices.Sort(langs)
	v := url.Values{}
	if len(langs) > 0 {
		v.Set("languages", strings.Join(langs, ","))
	}
	imdb := strings.TrimPrefix(strings.ToLower(q.IMDBID), "tt")
	switch {
	case !q.Movie && q.TMDBID != 0:
		v.Set("parent_tmdb_id", strconv.FormatInt(q.TMDBID, 10))
	case !q.Movie && imdb != "":
		v.Set("parent_imdb_id", imdb)
	case q.TMDBID != 0:
		v.Set("tmdb_id", strconv.FormatInt(q.TMDBID, 10))
	case imdb != "":
		v.Set("imdb_id", imdb)
	default:
		return nil, nil // nothing to find it by surely
	}
	if !q.Movie {
		if q.Season <= 0 && q.Episode <= 0 {
			return nil, nil
		}
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
	}
	if q.Hash != "" {
		// With an id as well, the hash only marks the files made for this
		// one (moviehash_match).
		v.Set("moviehash", q.Hash)
	}
	var res osSearch
	if err := o.call(ctx, http.MethodGet, "/subtitles?"+v.Encode(), nil, &res); err != nil {
		return nil, err
	}
	var out []Found
	for _, d := range res.Data {
		if f, ok := d.found(); ok && slices.Contains(q.Languages, f.Language) {
			out = append(out, f)
		}
	}
	return out, nil
}

type flexBool bool

// UnmarshalJSON takes true, false, 1, 0, "1" or "0": the API isn't consistent.
func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	*b = s == "true" || s == "1"
	return nil
}

type osSearch struct {
	Data []osItem `json:"data"`
}

type osItem struct {
	Attributes struct {
		Language         string    `json:"language"`
		DownloadCount    int       `json:"download_count"`
		HearingImpaired  flexBool  `json:"hearing_impaired"`
		ForeignPartsOnly flexBool  `json:"foreign_parts_only"`
		MachineTrans     flexBool  `json:"machine_translated"`
		AITranslated     flexBool  `json:"ai_translated"`
		FromTrusted      flexBool  `json:"from_trusted"`
		MoviehashMatch   *flexBool `json:"moviehash_match"`
		Release          string    `json:"release"`
		Files            []struct {
			FileID   int64  `json:"file_id"`
			FileName string `json:"file_name"`
		} `json:"files"`
	} `json:"attributes"`
}

// found turns a search result into a Found. Subtitles split over several
// files, one per disc, are left out: they fit no single video file.
func (d osItem) found() (Found, bool) {
	a := d.Attributes
	if len(a.Files) != 1 || a.Files[0].FileID == 0 {
		return Found{}, false
	}
	f := Found{
		Provider: "opensubtitles", ID: strconv.FormatInt(a.Files[0].FileID, 10),
		Language: Code(a.Language), Release: strings.TrimSpace(a.Release),
		SDH: bool(a.HearingImpaired), Forced: bool(a.ForeignPartsOnly),
		Machine: bool(a.MachineTrans) || bool(a.AITranslated), Downloads: a.DownloadCount, Trusted: bool(a.FromTrusted),
	}
	if a.MoviehashMatch != nil {
		f.MatchesFile = bool(*a.MoviehashMatch)
	}
	if f.Release == "" {
		f.Release = strings.TrimSuffix(a.Files[0].FileName, ".srt")
	}
	return f, f.Language != ""
}

// Download fetches a file the search found, as SubRip.
func (o *OpenSubtitles) Download(ctx context.Context, f Found) ([]byte, string, error) {
	id, err := strconv.ParseInt(f.ID, 10, 64)
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Link      string `json:"link"`
		FileName  string `json:"file_name"`
		Remaining int    `json:"remaining"`
		Message   string `json:"message"`
	}
	body, _ := json.Marshal(map[string]any{"file_id": id, "sub_format": "srt"})
	if err := o.call(ctx, http.MethodPost, "/download", body, &res); err != nil {
		return nil, "", err
	}
	if res.Link == "" {
		if res.Remaining <= 0 {
			return nil, "", ErrQuota
		}
		return nil, "", fmt.Errorf("OpenSubtitles gave no download link: %s", res.Message)
	}
	data, err := fetch(ctx, o.client(), res.Link, o.UserAgent)
	if err != nil {
		return nil, "", fmt.Errorf("OpenSubtitles: %w", err)
	}
	return data, "subtitle.srt", nil
}

// fetch downloads a subtitle file.
func fetch(ctx context.Context, c *http.Client, link, ua string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSubtitleSize {
		return nil, errors.New("the file is too big")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("the file is empty")
	}
	return data, nil
}

// Check makes sure the API key works, and the username and password when
// they're given.
func (o *OpenSubtitles) Check(ctx context.Context) error {
	if o.Username != "" {
		o.mu.Lock()
		o.token = ""
		o.mu.Unlock()
		_, _, err := o.signIn(ctx)
		return err
	}
	return o.call(ctx, http.MethodGet, "/infos/formats", nil, nil)
}

func (o *OpenSubtitles) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return httpClient
}

func (o *OpenSubtitles) apiBase() string {
	if o.Base != "" {
		return strings.TrimSuffix(o.Base, "/")
	}
	return OpenSubtitlesBase
}

// signIn returns a token for the account, signing in when there's none
// yet, and where its requests go.
func (o *OpenSubtitles) signIn(ctx context.Context) (token, base string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && time.Since(o.tokenAt) < tokenLife {
		return o.token, o.base, nil
	}
	body, _ := json.Marshal(map[string]string{"username": o.Username, "password": o.Password})
	var res struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	status, err := o.do(ctx, o.apiBase(), "", http.MethodPost, "/login", body, &res)
	switch {
	case status == http.StatusUnauthorized:
		return "", "", ErrBadLogin
	case err != nil:
		return "", "", err
	case res.Token == "":
		return "", "", ErrBadLogin
	}
	o.token, o.tokenAt, o.base = res.Token, time.Now(), o.apiBase()
	if b := strings.TrimSpace(res.BaseURL); b != "" && o.Base == "" {
		// A host such as vip-api.opensubtitles.com.
		if !strings.Contains(b, "://") {
			b = "https://" + b
		}
		if u, err := url.Parse(b); err == nil && u.Host != "" {
			o.base = "https://" + u.Host + "/api/v1"
		}
	}
	return o.token, o.base, nil
}

// call makes a request, signed in when there's an account, and signs in
// again once if the token has expired.
func (o *OpenSubtitles) call(ctx context.Context, method, path string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		token, base := "", o.apiBase()
		if o.Username != "" {
			var err error
			if token, base, err = o.signIn(ctx); err != nil {
				return err
			}
		}
		status, err := o.do(ctx, base, token, method, path, body, out)
		if status == http.StatusUnauthorized && token != "" && attempt == 0 {
			o.mu.Lock()
			o.token = ""
			o.mu.Unlock()
			continue
		}
		return err
	}
}

func (o *OpenSubtitles) do(ctx context.Context, base, token, method, path string, body []byte, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Api-Key", o.APIKey)
	req.Header.Set("User-Agent", o.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, ErrBadKey
	case resp.StatusCode == http.StatusUnauthorized && token == "" && path != "/login":
		return resp.StatusCode, ErrBadKey
	case resp.StatusCode == http.StatusNotAcceptable || resp.StatusCode == http.StatusProxyAuthRequired:
		// 406 and 407 are how the API says the day's downloads are used up.
		return resp.StatusCode, ErrQuota
	case resp.StatusCode == http.StatusTooManyRequests:
		return resp.StatusCode, errors.New("OpenSubtitles asked Box Office to slow down")
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated:
		return resp.StatusCode, fmt.Errorf("OpenSubtitles %s %s: %s: %s", method, strings.SplitN(path, "?", 2)[0], resp.Status, bytes.TrimSpace(raw[:min(len(raw), 200)]))
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("OpenSubtitles: %w", err)
	}
	return resp.StatusCode, nil
}

// FileHash is a video's OpenSubtitles hash: its size plus the sum of its
// first and last 64 KB as 64-bit numbers, as 16 hex digits. It reads only
// those two pieces, so it's quick even on network storage.
func FileHash(path string) (string, error) {
	const chunk = 64 << 10
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	if size < chunk {
		return "", errors.New("too small to hash")
	}
	hash := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := f.ReadAt(buf, off); err != nil {
			return "", err
		}
		for i := 0; i < chunk; i += 8 {
			hash += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", hash), nil
}
