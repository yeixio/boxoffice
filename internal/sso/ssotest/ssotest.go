// Package ssotest stands in for a single sign-on provider, for tests.
package ssotest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

var b64 = base64.RawURLEncoding

// Provider is an OpenID Connect provider with one person at the keyboard.
type Provider struct {
	// URL is its address: the issuer.
	URL string
	// ClientID and ClientSecret are what it gave Box Office.
	ClientID, ClientSecret string

	key *rsa.PrivateKey
	hs  *httptest.Server

	mu sync.Mutex
	// who is signed in at the provider: the claims it vouches for.
	who map[string]any
	// refuse makes it send the browser back with an error.
	refuse bool
	// change alters the proof before it's signed, and sign replaces how
	// it's signed, for proofs that shouldn't be believed.
	change func(claims map[string]any)
	sign   func(signed string) (alg string, sig []byte)
	codes  map[string]pending
	// thin leaves everything but the subject out of the proof, as some
	// providers do, so it has to be asked for.
	thin bool
}

type pending struct {
	nonce, challenge, redirect string
	who                        map[string]any
}

// New starts a provider.
func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{ClientID: "boxoffice", ClientSecret: "s3cret", key: key, codes: map[string]pending{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	p.hs = httptest.NewServer(mux)
	t.Cleanup(p.hs.Close)
	p.URL = p.hs.URL
	return p
}

// SignIn says who's at the keyboard: sub and whatever else the provider
// vouches for (email, preferred_username, name, groups).
func (p *Provider) SignIn(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.who = claims
}

// Refuse makes the provider turn sign-ins down from now on.
func (p *Provider) Refuse(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refuse = v
}

// Thin makes the proof carry only the subject; the rest is at /userinfo.
func (p *Provider) Thin(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.thin = v
}

// Change alters each proof's claims before signing; nil stops.
func (p *Provider) Change(fn func(claims map[string]any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.change = fn
}

// SignWith replaces how proofs are signed; nil goes back to the real key.
func (p *Provider) SignWith(fn func(signed string) (alg string, sig []byte)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sign = fn
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
		"jwks_uri": p.URL + "/keys", "userinfo_endpoint": p.URL + "/userinfo", "scopes_supported": []string{"openid", "profile", "email", "groups"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, r *http.Request) {
	pub := p.key.PublicKey
	json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": b64.EncodeToString(pub.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// authorize is the provider's sign-in page: whoever is at the keyboard is
// signed in at once, and the browser goes back with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := url.Values{"state": {q.Get("state")}}
	if p.refuse || p.who == nil {
		out.Set("error", "access_denied")
	} else {
		b := make([]byte, 12)
		rand.Read(b)
		code := b64.EncodeToString(b)
		p.codes[code] = pending{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), who: p.who}
		out.Set("code", code)
	}
	back.RawQuery = out.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, secret, _ := r.BasicAuth()
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if id != p.ClientID || secret != p.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
		return
	}
	code, ok := p.codes[r.Form.Get("code")]
	delete(p.codes, r.Form.Get("code"))
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || code.redirect != r.Form.Get("redirect_uri") || b64.EncodeToString(sum[:]) != code.challenge {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	claims := map[string]any{"iss": p.URL, "aud": p.ClientID, "nonce": code.nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
	for k, v := range code.who {
		if !p.thin || k == "sub" {
			claims[k] = v
		}
	}
	if p.change != nil {
		p.change(claims)
	}
	sub, _ := code.who["sub"].(string)
	json.NewEncoder(w).Encode(map[string]string{"id_token": p.proof(claims), "access_token": "access-" + sub, "token_type": "Bearer"})
}

func (p *Provider) proof(claims map[string]any) string {
	alg := "RS256"
	body, _ := json.Marshal(claims)
	signed := func(alg string) string {
		head, _ := json.Marshal(map[string]string{"alg": alg, "kid": "k1", "typ": "JWT"})
		return b64.EncodeToString(head) + "." + b64.EncodeToString(body)
	}
	if p.sign != nil {
		alg, _ = p.sign("")
		s := signed(alg)
		_, sig := p.sign(s)
		return s + "." + b64.EncodeToString(sig)
	}
	s := signed(alg)
	sum := sha256.Sum256([]byte(s))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	return s + "." + b64.EncodeToString(sig)
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sub, _ := p.who["sub"].(string)
	if r.Header.Get("Authorization") != "Bearer access-"+sub {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	json.NewEncoder(w).Encode(p.who)
}
