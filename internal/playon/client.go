package playon

import (
	"bytes"
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

// PlayOn has no published API. These are the addresses its own web and
// phone apps use, as the open-source cs.app.playon client does: sign in
// with the account's email and password for a token that's sent, as it
// is, in the Authorization header; list the recordings; and ask for one's
// download, which is a CloudFront address that takes three signed cookies.

// APIBase is PlayOn Cloud's API. Tests point it at a fake.
var APIBase = "https://api.playonrecorder.com/v3/"

// ErrBadLogin says PlayOn turned the email and password down.
var ErrBadLogin = errors.New("PlayOn didn't accept that email and password")

// Client talks to PlayOn Cloud for one account.
type Client struct {
	Email, Password string
	Base            string // APIBase when empty
	HTTP            *http.Client
	UserAgent       string

	token   string
	expires time.Time
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return APIBase
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// answer is how PlayOn wraps what it says.
type answer struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

// call asks PlayOn something and reads the data out of its answer.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, auth bool, into any) error {
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-mmt-app", "web")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if auth {
		if err := c.signIn(ctx); err != nil {
			return err
		}
		req.Header.Set("Authorization", c.token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var a answer
	jerr := json.Unmarshal(body, &a)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		c.token = ""
		if path == "login" {
			return ErrBadLogin
		}
		return fmt.Errorf("PlayOn said %s", resp.Status)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("PlayOn said %s%s", resp.Status, why(a))
	case jerr != nil:
		return fmt.Errorf("PlayOn's answer wasn't understood: %w", jerr)
	case !a.Success:
		if path == "login" {
			return ErrBadLogin
		}
		return fmt.Errorf("PlayOn said no%s", why(a))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(a.Data, into)
}

func why(a answer) string {
	msg := a.Message
	if msg == "" && len(a.Error) > 0 {
		var s string
		if json.Unmarshal(a.Error, &s) == nil {
			msg = s
		} else {
			msg = string(a.Error)
		}
	}
	if msg = strings.TrimSpace(msg); msg != "" {
		return ": " + msg
	}
	return ""
}

// login is what signing in gives.
type login struct {
	Token string `json:"token"`
	// Exp is when the token stops working, in Unix seconds.
	Exp num `json:"exp"`
}

// signIn gets a token, unless the one it has works for a while yet.
func (c *Client) signIn(ctx context.Context) error {
	if c.token != "" && time.Until(c.expires) > time.Minute {
		return nil
	}
	if c.Email == "" || c.Password == "" {
		return ErrBadLogin
	}
	var l login
	if err := c.call(ctx, http.MethodPost, "login", url.Values{"email": {c.Email}, "password": {c.Password}}, false, &l); err != nil {
		return err
	}
	if l.Token == "" {
		return errors.New("PlayOn signed in but gave no token")
	}
	c.token, c.expires = l.Token, time.Unix(int64(l.Exp), 0)
	if l.Exp == 0 {
		c.expires = time.Now().Add(30 * time.Minute)
	}
	return nil
}

// Check signs in, to say whether the email and password work.
func (c *Client) Check(ctx context.Context) error {
	c.token = ""
	return c.signIn(ctx)
}

// Recording is a recording as PlayOn lists it. One still being recorded
// has no Created.
type Recording struct {
	ID         num    `json:"ID"`
	Name       string `json:"Name"`
	Series     string `json:"Series"`
	Season     num    `json:"Season"`
	Episode    num    `json:"Episode"`
	Year       num    `json:"ReleaseYear"`
	Provider   string `json:"ProviderID"`
	Quality    num    `json:"Quality"`
	Created    string `json:"Created"`
	Expires    string `json:"Expires"`
	BrowsePath string `json:"BrowsePath"`
	Size       num    `json:"Size"`
}

// Ready says whether it can be downloaded now.
func (r Recording) Ready(now time.Time) bool {
	if r.Created == "" {
		return false
	}
	exp, ok := playOnTime(r.Expires)
	return ok && exp.After(now)
}

// ExpiresAt is when PlayOn deletes it; zero when it doesn't say.
func (r Recording) ExpiresAt() time.Time {
	t, _ := playOnTime(r.Expires)
	return t
}

// playOnTime reads PlayOn's times, which are UTC: "2026-11-25 16:00:42"
// or with fractions of a second.
func playOnTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, f := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if t, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type entries struct {
	Entries []Recording `json:"entries"`
}

// Library is every recording PlayOn has for the account: ready, being
// recorded, and expired.
func (c *Client) Library(ctx context.Context) ([]Recording, error) {
	var e entries
	err := c.call(ctx, http.MethodGet, "library/all", nil, true, &e)
	return e.Entries, err
}

// Queue is what's waiting to be recorded, or being recorded.
func (c *Client) Queue(ctx context.Context) ([]Recording, error) {
	var e entries
	err := c.call(ctx, http.MethodGet, "queue", nil, true, &e)
	return e.Entries, err
}

// download is where a recording is fetched from.
type download struct {
	URL  string         `json:"url"`
	Data map[string]any `json:"data"`
}

// Open starts downloading a recording. The caller closes the body; size
// is -1 when PlayOn doesn't say, and name is the file's name on PlayOn's
// file server.
func (c *Client) Open(ctx context.Context, id int64) (body io.ReadCloser, size int64, name string, err error) {
	var d download
	if err = c.call(ctx, http.MethodGet, "library/"+strconv.FormatInt(id, 10)+"/download", nil, true, &d); err != nil {
		return nil, 0, "", err
	}
	if d.URL == "" {
		return nil, 0, "", errors.New("PlayOn gave no address to download it from")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return nil, 0, "", err
	}
	var cookies []string
	for _, k := range []string{"CloudFront-Expires", "CloudFront-Key-Pair-Id", "CloudFront-Signature"} {
		if v, ok := d.Data[k]; ok {
			cookies = append(cookies, k+"="+fmt.Sprint(v))
		}
	}
	if len(cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	// No overall timeout: a recording can be several gigabytes.
	hc := *c.http()
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, "", fmt.Errorf("downloading it, PlayOn's file server said %s", resp.Status)
	}
	return resp.Body, resp.ContentLength, fileName(d.URL), nil
}

// fileName is the name PlayOn gives the file, from its download address.
func fileName(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	p := u.Path
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// num is a number PlayOn sends as a number, a string, or null.
type num int64

func (n *num) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(strings.TrimSpace(s))
		if len(b) == 0 {
			*n = 0
			return nil
		}
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		*n = 0
		return nil // a number PlayOn writes oddly isn't worth failing the list for
	}
	*n = num(f)
	return nil
}
