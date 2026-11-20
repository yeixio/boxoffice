// Package sso signs people in through a single sign-on provider that
// speaks OpenID Connect, such as Authentik, Authelia, Keycloak or Pocket
// ID: Box Office sends the browser there, and the provider sends it back
// with proof of who signed in.
package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SettingKey is where the settings are kept.
const SettingKey = "sso"

// Settings is how single sign-on is set up.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Name is what the provider is called on the sign-in button.
	Name string `json:"name"`
	// Issuer is the provider's address, the one its discovery document
	// (/.well-known/openid-configuration) is under.
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	// Admins are the accounts that sign in to Box Office itself: each an
	// email address, a username or the provider's id for the account.
	Admins []string `json:"admins"`
	// AdminGroup, when set, lets anyone in that group in as an admin too.
	AdminGroup string `json:"adminGroup"`
	// Members lets people sign in to the request site with it.
	Members bool `json:"members"`
	// MemberGroup, when set, keeps the request site to that group.
	MemberGroup string `json:"memberGroup"`
}

// Error is something to tell the person setting it up as it is.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// Check tidies the settings and says what's wrong with them.
func (st *Settings) Check() error {
	st.Name = strings.TrimSpace(st.Name)
	st.Issuer = strings.TrimRight(strings.TrimSpace(st.Issuer), "/")
	st.Issuer = strings.TrimSuffix(st.Issuer, "/.well-known/openid-configuration")
	st.ClientID = strings.TrimSpace(st.ClientID)
	st.ClientSecret = strings.TrimSpace(st.ClientSecret)
	st.AdminGroup = strings.TrimSpace(st.AdminGroup)
	st.MemberGroup = strings.TrimSpace(st.MemberGroup)
	admins := []string{}
	for _, a := range st.Admins {
		if a = strings.TrimSpace(a); a != "" && !slices.ContainsFunc(admins, func(h string) bool { return strings.EqualFold(h, a) }) {
			admins = append(admins, a)
		}
	}
	st.Admins = admins
	if st.Name == "" {
		st.Name = "single sign-on"
	}
	switch {
	case utf8.RuneCountInString(st.Name) > 40:
		return badf("keep the name to 40 characters")
	case len(st.Admins) > 50:
		return badf("that's more than 50 admin accounts; use a group")
	case !st.Enabled && st.Issuer == "":
		return nil
	case st.Issuer == "":
		return badf("enter the provider's address")
	case st.ClientID == "":
		return badf("enter the client ID the provider gave Box Office")
	}
	u, err := url.Parse(st.Issuer)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return badf("enter the provider's address starting with https://")
	}
	return nil
}

// Identity is who the provider says signed in.
type Identity struct {
	// Subject is the provider's own id for the account, which never
	// changes.
	Subject       string   `json:"subject"`
	Email         string   `json:"email,omitempty"`
	EmailVerified bool     `json:"emailVerified"`
	Username      string   `json:"username,omitempty"`
	Name          string   `json:"name,omitempty"`
	Groups        []string `json:"groups"`
}

// Admin says whether an account signs in to Box Office itself: it's
// listed by its id, username or email address, or it's in the admin
// group. An email address the provider says isn't the person's own
// doesn't count.
func (st Settings) Admin(id Identity) bool {
	if st.AdminGroup != "" && slices.Contains(id.Groups, st.AdminGroup) {
		return true
	}
	for _, a := range st.Admins {
		switch {
		case a == id.Subject:
			return true
		case id.Username != "" && strings.EqualFold(a, id.Username):
			return true
		case id.Email != "" && id.EmailVerified && strings.EqualFold(a, id.Email):
			return true
		}
	}
	return false
}

// Member says whether an account may use the request site.
func (st Settings) Member(id Identity) bool {
	return st.Members && (st.MemberGroup == "" || slices.Contains(id.Groups, st.MemberGroup))
}

// provider is what a provider's discovery document says about it.
type provider struct {
	Issuer        string   `json:"issuer"`
	AuthURL       string   `json:"authorization_endpoint"`
	TokenURL      string   `json:"token_endpoint"`
	UserInfoURL   string   `json:"userinfo_endpoint"`
	KeysURL       string   `json:"jwks_uri"`
	TokenAuth     []string `json:"token_endpoint_auth_methods_supported"`
	ScopesOffered []string `json:"scopes_supported"`

	read time.Time
}

// Client talks to the provider.
type Client struct {
	// HTTP is given by tests.
	HTTP *http.Client
	// Now is given by tests.
	Now func() time.Time

	mu        sync.Mutex
	providers map[string]provider
	keys      map[string]keySet
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) getJSON(ctx context.Context, address, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", address, res.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// discover reads the provider's discovery document, at most every hour.
func (c *Client) discover(ctx context.Context, st Settings) (provider, error) {
	c.mu.Lock()
	p, ok := c.providers[st.Issuer]
	c.mu.Unlock()
	if ok && c.now().Sub(p.read) < time.Hour {
		return p, nil
	}
	p = provider{}
	address := st.Issuer + "/.well-known/openid-configuration"
	if err := c.getJSON(ctx, address, "", &p); err != nil {
		return p, badf("couldn't read the provider's settings at %s: %v", address, err)
	}
	switch {
	case p.AuthURL == "" || p.TokenURL == "" || p.KeysURL == "":
		return p, badf("%s doesn't look like an OpenID Connect provider", address)
	case strings.TrimRight(p.Issuer, "/") != st.Issuer:
		// A provider that names another address isn't the one asked for.
		return p, badf("the provider at %s says its address is %s; enter that one", st.Issuer, p.Issuer)
	}
	p.read = c.now()
	c.mu.Lock()
	if c.providers == nil {
		c.providers = map[string]provider{}
	}
	c.providers[st.Issuer] = p
	c.mu.Unlock()
	return p, nil
}

// Visit is one trip to the provider and back: what Box Office keeps in
// the browser meanwhile, to know the answer is to its own question.
type Visit struct {
	// State comes back with the answer.
	State string `json:"s"`
	// Nonce comes back inside the signed proof.
	Nonce string `json:"n"`
	// Verifier is shown when the answer is traded for the proof, so only
	// the browser that started can finish.
	Verifier string `json:"v"`
	// For is what the sign-in is for: admin, member or test.
	For string `json:"f"`
	// Redirect is the address the provider sends the browser back to.
	Redirect string `json:"r"`
}

func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start begins a visit: it gives the address at the provider to send the
// browser to, and what to keep until it comes back to redirect.
func (c *Client) Start(ctx context.Context, st Settings, what, redirect string) (string, Visit, error) {
	p, err := c.discover(ctx, st)
	if err != nil {
		return "", Visit{}, err
	}
	v := Visit{State: random(), Nonce: random(), Verifier: random(), For: what, Redirect: redirect}
	challenge := sha256.Sum256([]byte(v.Verifier))
	scopes := []string{"openid", "profile", "email"}
	// Groups are asked for where the provider has a scope for them; others
	// send them anyway, or need them added to the profile.
	if slices.Contains(p.ScopesOffered, "groups") {
		scopes = append(scopes, "groups")
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {st.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {v.State},
		"nonce":                 {v.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(p.AuthURL, "?") {
		sep = "&"
	}
	return p.AuthURL + sep + q.Encode(), v, nil
}

// ErrRefused is the provider, or the person at it, saying no.
var ErrRefused = errors.New("the sign-in was cancelled or refused at the provider")

// Finish ends a visit: it trades the code the provider sent back for the
// signed proof of who signed in, and checks the proof.
func (c *Client) Finish(ctx context.Context, st Settings, v Visit, code string) (Identity, error) {
	p, err := c.discover(ctx, st)
	if err != nil {
		return Identity{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {v.Redirect}, "code_verifier": {v.Verifier}, "client_id": {st.ClientID}}
	// Providers take the secret in a header unless they say they only take
	// it in the form.
	inForm := len(p.TokenAuth) > 0 && !slices.Contains(p.TokenAuth, "client_secret_basic") && slices.Contains(p.TokenAuth, "client_secret_post")
	if inForm && st.ClientSecret != "" {
		form.Set("client_secret", st.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if !inForm && st.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(st.ClientID), url.QueryEscape(st.ClientSecret))
	}
	res, err := c.client().Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("couldn't reach the provider: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.Unmarshal(body, &tok)
	switch {
	case res.StatusCode != http.StatusOK && tok.Error == "invalid_client":
		return Identity{}, badf("the provider turned down Box Office's client ID or secret (%s)", strings.TrimSpace(tok.Error+" "+tok.Description))
	case res.StatusCode != http.StatusOK:
		return Identity{}, fmt.Errorf("the provider answered %d %s", res.StatusCode, strings.TrimSpace(tok.Error+" "+tok.Description))
	case tok.IDToken == "":
		return Identity{}, errors.New("the provider's answer has no proof of who signed in (no id_token)")
	}
	claims, err := c.verify(ctx, st, p, tok.IDToken, v.Nonce)
	if err != nil {
		return Identity{}, err
	}
	id := identity(claims)
	// What the proof leaves out, the provider tells when asked, for the
	// same account only.
	if p.UserInfoURL != "" && tok.AccessToken != "" && (id.Email == "" || id.Username == "" || len(id.Groups) == 0) {
		var more map[string]any
		if err := c.getJSON(ctx, p.UserInfoURL, tok.AccessToken, &more); err == nil {
			if extra := identity(more); extra.Subject == id.Subject {
				id = merge(id, extra)
			}
		}
	}
	return id, nil
}

func identity(claims map[string]any) Identity {
	str := func(k string) string {
		s, _ := claims[k].(string)
		return strings.TrimSpace(s)
	}
	id := Identity{Subject: str("sub"), Email: str("email"), Username: str("preferred_username"), Name: str("name"), Groups: []string{}}
	// A provider that doesn't say treats the address as the person's own;
	// one that says no is believed.
	switch v := claims["email_verified"].(type) {
	case bool:
		id.EmailVerified = v
	case string:
		id.EmailVerified = v == "true"
	default:
		id.EmailVerified = id.Email != ""
	}
	switch g := claims["groups"].(type) {
	case []any:
		for _, v := range g {
			if s, ok := v.(string); ok && s != "" {
				id.Groups = append(id.Groups, s)
			}
		}
	case string:
		if g != "" {
			id.Groups = append(id.Groups, g)
		}
	}
	return id
}

func merge(id, extra Identity) Identity {
	if id.Email == "" {
		id.Email, id.EmailVerified = extra.Email, extra.EmailVerified
	}
	if id.Username == "" {
		id.Username = extra.Username
	}
	if id.Name == "" {
		id.Name = extra.Name
	}
	if len(id.Groups) == 0 {
		id.Groups = extra.Groups
	}
	return id
}

// Check reads the provider's settings, to say at once when the address is
// wrong.
func (c *Client) Check(ctx context.Context, st Settings) error {
	c.mu.Lock()
	delete(c.providers, st.Issuer)
	c.mu.Unlock()
	_, err := c.discover(ctx, st)
	return err
}
