// Package passkey checks passkeys (WebAuthn): that a browser made one for
// this site, and later that whoever signs in holds it.
//
// It asks the authenticator for no attestation, so a new passkey arrives
// as the browser hands it over: the authenticator's data and the public
// key, which browsers give ready to read. Nothing here needs CBOR.
package passkey

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// The kinds of key a passkey may have, by their COSE numbers.
const (
	ES256 = -7
	RS256 = -257
)

// Algorithms is what a browser is asked for, best first.
var Algorithms = []int{ES256, RS256}

// Credential is a passkey as it's kept.
type Credential struct {
	// ID is the authenticator's name for it, base64url.
	ID string
	// PublicKey is its public key (SubjectPublicKeyInfo, DER).
	PublicKey []byte
	Alg       int
	// SignCount is how many times the authenticator says it has signed;
	// many passkeys always say 0.
	SignCount uint32
	// RPID is the host name it was made for, the only one it works at.
	RPID string
}

// Registration is what a browser sends once it has made a passkey.
type Registration struct {
	ID         string `json:"id"`
	ClientData string `json:"clientDataJSON"`
	AuthData   string `json:"authenticatorData"`
	PublicKey  string `json:"publicKey"`
	Alg        int    `json:"publicKeyAlgorithm"`
}

// Assertion is what a browser sends to sign in with a passkey.
type Assertion struct {
	ID         string `json:"id"`
	ClientData string `json:"clientDataJSON"`
	AuthData   string `json:"authenticatorData"`
	Signature  string `json:"signature"`
}

// Error is a passkey that isn't believed, said in words for whoever tried.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// NewChallenge makes the random value a passkey signs, so an answer can't
// be used twice.
func NewChallenge() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// RPID is the name passkeys are made for at this address: its host name,
// without the port. ok is false for a bare IP address, which browsers
// don't make passkeys for.
func RPID(host string) (id string, ok bool) {
	h := host
	if x, _, err := net.SplitHostPort(host); err == nil {
		h = x
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || net.ParseIP(strings.Trim(h, "[]")) != nil {
		return h, false
	}
	return h, true
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// Challenge reads the challenge a browser's answer was made for, to look
// up what was asked. The answer isn't believed yet.
func Challenge(clientDataJSON string) string {
	raw, err := b64(clientDataJSON)
	if err != nil {
		return ""
	}
	var cd clientData
	if json.Unmarshal(raw, &cd) != nil {
		return ""
	}
	return cd.Challenge
}

// client checks what the browser says it was doing: the right kind of
// thing, for this challenge, on this site. It gives the bytes it said it in.
func client(clientDataJSON, kind, challenge, origin string) ([]byte, error) {
	raw, err := b64(clientDataJSON)
	if err != nil {
		return nil, badf("the browser's answer can't be read")
	}
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return nil, badf("the browser's answer can't be read")
	}
	switch {
	case cd.Type != kind:
		return nil, badf("the browser's answer is for something else")
	case challenge == "" || subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(challenge)) != 1:
		return nil, badf("the browser's answer is for another try; try again")
	case cd.Origin != origin:
		return nil, badf("the passkey was used at %s, not at %s", cd.Origin, origin)
	case cd.CrossOrigin:
		return nil, badf("the passkey was used from inside another site")
	}
	return raw, nil
}

const (
	flagPresent  = 0x01
	flagVerified = 0x04
	flagAttested = 0x40
)

// authData is what the authenticator says: which site, whether someone
// was there and proved who they are, its count, and for a new passkey its
// name.
type authData struct {
	flags byte
	count uint32
	id    []byte
}

func parseAuthData(raw []byte, rpID string) (authData, error) {
	var a authData
	if len(raw) < 37 {
		return a, badf("the passkey's answer is too short")
	}
	want := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(raw[:32], want[:]) {
		return a, badf("the passkey is for another site, not %s", rpID)
	}
	a.flags = raw[32]
	a.count = binary.BigEndian.Uint32(raw[33:37])
	if a.flags&flagPresent == 0 {
		return a, badf("the passkey didn't say anyone was there")
	}
	if a.flags&flagVerified == 0 {
		return a, badf("the passkey didn't check who was using it (a PIN, fingerprint or face); use one that does")
	}
	if a.flags&flagAttested != 0 {
		rest := raw[37:]
		if len(rest) < 18 {
			return a, badf("the passkey's answer is too short")
		}
		n := int(binary.BigEndian.Uint16(rest[16:18]))
		if n == 0 || n > 1023 || len(rest) < 18+n {
			return a, badf("the passkey's answer is too short")
		}
		a.id = rest[18 : 18+n]
	}
	return a, nil
}

func publicKey(der []byte, alg int) (crypto.PublicKey, error) {
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, badf("the passkey's key can't be read")
	}
	switch pub := k.(type) {
	case *ecdsa.PublicKey:
		if alg == ES256 && pub.Curve == elliptic.P256() {
			return pub, nil
		}
	case *rsa.PublicKey:
		if alg == RS256 && pub.N.BitLen() >= 2048 {
			return pub, nil
		}
	}
	return nil, badf("the passkey has a kind of key Box Office doesn't take")
}

// Register checks a passkey a browser just made: that it was made on this
// site (origin, such as https://box.example.com), in answer to challenge,
// by someone who proved who they are to the authenticator.
func Register(reg Registration, challenge, origin, rpID string) (Credential, error) {
	if _, err := client(reg.ClientData, "webauthn.create", challenge, origin); err != nil {
		return Credential{}, err
	}
	raw, err := b64(reg.AuthData)
	if err != nil {
		return Credential{}, badf("the passkey's answer can't be read")
	}
	a, err := parseAuthData(raw, rpID)
	if err != nil {
		return Credential{}, err
	}
	id, err := b64(reg.ID)
	if err != nil || len(a.id) == 0 || !bytes.Equal(id, a.id) {
		return Credential{}, badf("the passkey's answer doesn't name the passkey")
	}
	der, err := b64(reg.PublicKey)
	if err != nil {
		return Credential{}, badf("the passkey's key can't be read")
	}
	if _, err := publicKey(der, reg.Alg); err != nil {
		return Credential{}, err
	}
	return Credential{ID: base64.RawURLEncoding.EncodeToString(id), PublicKey: der, Alg: reg.Alg, SignCount: a.count, RPID: rpID}, nil
}

// ErrCopied is a passkey whose count went backwards: a sign that there
// are two of it.
var ErrCopied = errors.New("the passkey's count went backwards, as if it had been copied")

// Verify checks a sign-in: that the holder of cred signed challenge on
// this site just now. It gives the authenticator's new count to keep.
func Verify(a Assertion, cred Credential, challenge, origin string) (uint32, error) {
	cd, err := client(a.ClientData, "webauthn.get", challenge, origin)
	if err != nil {
		return 0, err
	}
	raw, err := b64(a.AuthData)
	if err != nil {
		return 0, badf("the passkey's answer can't be read")
	}
	ad, err := parseAuthData(raw, cred.RPID)
	if err != nil {
		return 0, err
	}
	sig, err := b64(a.Signature)
	if err != nil {
		return 0, badf("the passkey's signature can't be read")
	}
	pub, err := publicKey(cred.PublicKey, cred.Alg)
	if err != nil {
		return 0, err
	}
	h := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, raw...), h[:]...))
	ok := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, sum[:], sig)
	case *rsa.PublicKey:
		ok = rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) == nil
	}
	if !ok {
		return 0, badf("the passkey's signature isn't right")
	}
	if (ad.count != 0 || cred.SignCount != 0) && ad.count <= cred.SignCount {
		return 0, ErrCopied
	}
	return ad.count, nil
}
