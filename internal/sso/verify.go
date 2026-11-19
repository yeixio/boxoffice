package sso

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	_ "crypto/sha256" // for crypto.SHA256
	_ "crypto/sha512" // for crypto.SHA384 and SHA512
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// The proof of who signed in is an ID token: three base64 parts, the
// claims in the middle, signed by the provider with a key it publishes.

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type keySet struct {
	Keys []jwk `json:"keys"`
	read time.Time
}

// skew is how far the provider's clock and this one may disagree.
const skew = 2 * time.Minute

// keysFor reads the provider's signing keys, at most every hour unless a
// token names a key that isn't among them, which is how providers change
// keys.
func (c *Client) keysFor(ctx context.Context, p provider, kid string) (keySet, error) {
	c.mu.Lock()
	ks, ok := c.keys[p.KeysURL]
	c.mu.Unlock()
	has := func() bool {
		for _, k := range ks.Keys {
			if kid == "" || k.Kid == kid {
				return true
			}
		}
		return false
	}
	age := c.now().Sub(ks.read)
	if ok && age < time.Hour && (has() || age < time.Minute) {
		return ks, nil
	}
	ks = keySet{}
	if err := c.getJSON(ctx, p.KeysURL, "", &ks); err != nil {
		return ks, fmt.Errorf("couldn't read the provider's signing keys: %w", err)
	}
	ks.read = c.now()
	c.mu.Lock()
	if c.keys == nil {
		c.keys = map[string]keySet{}
	}
	c.keys[p.KeysURL] = ks
	c.mu.Unlock()
	return ks, nil
}

func part(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// verify checks an ID token: that the provider signed it, for this client,
// in answer to this visit, and that it's still good. It gives the claims.
func (c *Client) verify(ctx context.Context, st Settings, p provider, token, nonce string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("the provider's proof isn't a signed token")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	hb, err := part(parts[0])
	if err != nil || json.Unmarshal(hb, &head) != nil {
		return nil, errors.New("the provider's proof has a header that can't be read")
	}
	sig, err := part(parts[2])
	if err != nil {
		return nil, errors.New("the provider's proof has a signature that can't be read")
	}
	ks, err := c.keysFor(ctx, p, head.Kid)
	if err != nil {
		return nil, err
	}
	signed := []byte(parts[0] + "." + parts[1])
	ok := false
	for _, k := range ks.Keys {
		if (head.Kid != "" && k.Kid != head.Kid) || (k.Use != "" && k.Use != "sig") {
			continue
		}
		if checkSignature(head.Alg, k, signed, sig) == nil {
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("the proof isn't signed with one of the provider's keys (%s)", head.Alg)
	}
	cb, err := part(parts[1])
	if err != nil {
		return nil, errors.New("the provider's proof can't be read")
	}
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, errors.New("the provider's proof can't be read")
	}
	str := func(k string) string { s, _ := claims[k].(string); return s }
	num := func(k string) (time.Time, bool) {
		f, ok := claims[k].(float64)
		return time.Unix(int64(f), 0), ok
	}
	now := c.now()
	switch {
	case strings.TrimRight(str("iss"), "/") != st.Issuer:
		return nil, fmt.Errorf("the proof is from %q, not the provider", str("iss"))
	case !audience(claims["aud"], st.ClientID):
		return nil, errors.New("the proof was made for another app, not Box Office")
	case str("nonce") != nonce:
		return nil, errors.New("the proof answers another sign-in, not this one")
	case str("sub") == "":
		return nil, errors.New("the proof doesn't say who signed in")
	}
	if azp := str("azp"); azp != "" && azp != st.ClientID {
		return nil, errors.New("the proof was made for another app, not Box Office")
	}
	exp, ok := num("exp")
	if !ok || now.After(exp.Add(skew)) {
		return nil, errors.New("the proof is out of date; check that this server's clock is right")
	}
	if nbf, ok := num("nbf"); ok && now.Before(nbf.Add(-skew)) {
		return nil, errors.New("the proof isn't good yet; check that this server's clock is right")
	}
	return claims, nil
}

// audience says whether a token's audience, one name or a list, has id.
func audience(aud any, id string) bool {
	switch a := aud.(type) {
	case string:
		return a == id
	case []any:
		for _, v := range a {
			if s, _ := v.(string); s == id {
				return true
			}
		}
	}
	return false
}

// checkSignature checks sig over signed with one key. Only signatures made
// with the provider's private key count: "none" and the shared-secret kinds
// are refused.
func checkSignature(alg string, k jwk, signed, sig []byte) error {
	var hash crypto.Hash
	switch alg[max(len(alg)-3, 0):] {
	case "256":
		hash = crypto.SHA256
	case "384":
		hash = crypto.SHA384
	case "512":
		hash = crypto.SHA512
	default:
		return errors.New("unknown kind of signature")
	}
	h := hash.New()
	h.Write(signed)
	sum := h.Sum(nil)
	switch {
	case strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS"):
		if k.Kty != "RSA" {
			return errors.New("not an RSA key")
		}
		nb, err1 := part(k.N)
		eb, err2 := part(k.E)
		if err1 != nil || err2 != nil || len(nb) < 256 || len(eb) == 0 || len(eb) > 4 {
			return errors.New("not a usable RSA key")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(new(big.Int).SetBytes(eb).Int64())}
		if alg[0] == 'P' {
			return rsa.VerifyPSS(pub, hash, sum, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}
		return rsa.VerifyPKCS1v15(pub, hash, sum, sig)
	case strings.HasPrefix(alg, "ES"):
		curve, size := elliptic.P256(), 32
		switch alg {
		case "ES256":
		case "ES384":
			curve, size = elliptic.P384(), 48
		default:
			return errors.New("unknown kind of signature")
		}
		xb, err1 := part(k.X)
		yb, err2 := part(k.Y)
		if k.Kty != "EC" || err1 != nil || err2 != nil || len(sig) != 2*size {
			return errors.New("not a usable EC key or signature")
		}
		point := append([]byte{4}, append(pad(xb, size), pad(yb, size)...)...)
		pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return err
		}
		if !ecdsa.Verify(pub, sum, new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])) {
			return errors.New("wrong signature")
		}
		return nil
	}
	return errors.New("unknown kind of signature")
}

func pad(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	return append(make([]byte, size-len(b)), b...)
}
