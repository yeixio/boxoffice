// Package passkeytest is an authenticator for tests: it makes passkeys
// and signs in with them the way a browser and its authenticator do.
package passkeytest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/yeixio/boxoffice/internal/passkey"
)

// Key is one passkey on the authenticator.
type Key struct {
	RPID string
	ID   []byte
	// Count is the authenticator's count of signatures; 0 stays 0, as on
	// passkeys that don't count.
	Count uint32
	// Flags is what the authenticator says about who was there; the
	// default is someone present who proved who they are.
	Flags byte
	// Type and CrossOrigin change what the browser says it was doing,
	// for answers that shouldn't be believed.
	Type        string
	CrossOrigin bool

	ec  *ecdsa.PrivateKey
	rsa *rsa.PrivateKey
}

// New makes a passkey for a site, with the usual kind of key.
func New(rpID string) *Key {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return &Key{RPID: rpID, ID: random(20), Flags: 0x05, ec: k}
}

// NewRSA makes a passkey with an RSA key, as Windows Hello may.
func NewRSA(rpID string) *Key {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	return &Key{RPID: rpID, ID: random(20), Flags: 0x05, rsa: k}
}

func random(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k *Key) client(kind, challenge, origin string) []byte {
	if k.Type != "" {
		kind = k.Type
	}
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": k.CrossOrigin})
	return b
}

func (k *Key) authData(attested bool) []byte {
	h := sha256.Sum256([]byte(k.RPID))
	flags := k.Flags
	if attested {
		flags |= 0x40
	}
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.Count)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(k.ID)))
		out = append(out, k.ID...)
		// The key in the authenticator's own format would follow; nothing
		// reads it, since the browser hands the key over ready to read.
		out = append(out, 0xa0)
	}
	return out
}

// Register is what the browser sends after making the passkey.
func (k *Key) Register(challenge, origin string) passkey.Registration {
	var pub any
	alg := passkey.ES256
	if k.rsa != nil {
		pub, alg = &k.rsa.PublicKey, passkey.RS256
	} else {
		pub = &k.ec.PublicKey
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return passkey.Registration{ID: b64(k.ID), ClientData: b64(k.client("webauthn.create", challenge, origin)), AuthData: b64(k.authData(true)), PublicKey: b64(der), Alg: alg}
}

// Assert is what the browser sends to sign in with the passkey.
func (k *Key) Assert(challenge, origin string) passkey.Assertion {
	if k.Count > 0 {
		k.Count++
	}
	cd := k.client("webauthn.get", challenge, origin)
	ad := k.authData(false)
	h := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	var sig []byte
	if k.rsa != nil {
		sig, _ = rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, sum[:])
	} else {
		sig, _ = ecdsa.SignASN1(rand.Reader, k.ec, sum[:])
	}
	return passkey.Assertion{ID: b64(k.ID), ClientData: b64(cd), AuthData: b64(ad), Signature: b64(sig)}
}
