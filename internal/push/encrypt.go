package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"
)

// What goes to a push service is encrypted for the one browser it's for
// (RFC 8291, "aes128gcm"), so the service passing it on can't read it, and
// signed with this server's key (RFC 8292, "VAPID"), so the service knows
// it's from the server the browser subscribed to.

var b64 = base64.RawURLEncoding

// decode reads a key as browsers give it: base64url, with or without the
// padding.
func decode(s string) ([]byte, error) {
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	if b, err := b64.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// recordSize is the most a push service has to take in one message.
const recordSize = 4096

// maxPayload is the most text that fits: the record, less its header, the
// padding mark and the seal.
const maxPayload = recordSize - 86 - 1 - 16

// encrypt seals payload for the browser whose keys are p256dh and auth.
// ours is a key made for this one message and salt 16 random bytes; both
// are given only by tests.
func encrypt(payload []byte, p256dh, auth []byte, ours *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload) > maxPayload {
		return nil, errors.New("the notification is too long")
	}
	if len(auth) != 16 {
		return nil, errors.New("the device's auth secret isn't 16 bytes")
	}
	theirs, err := ecdh.P256().NewPublicKey(p256dh)
	if err != nil {
		return nil, errors.New("the device's key isn't a P-256 key")
	}
	if ours == nil {
		if ours, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
	}
	shared, err := ours.ECDH(theirs)
	if err != nil {
		return nil, err
	}
	pub := ours.PublicKey().Bytes()
	// The secret both sides share, tied to both public keys.
	info := append([]byte("WebPush: info\x00"), p256dh...)
	info = append(info, pub...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Header: salt, record size, our public key. Then the one record, which
	// ends in 2 to say it's the last.
	out := make([]byte, 0, 86+len(payload)+17)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(pub)))
	out = append(out, pub...)
	return gcm.Seal(out, nonce, append(append([]byte{}, payload...), 2), nil), nil
}

// newKey makes this server's signing key, as the 32 bytes kept in settings.
func newKey() (string, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	b, err := k.Bytes()
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

func parseKey(saved string) (*ecdsa.PrivateKey, error) {
	b, err := decode(saved)
	if err != nil {
		return nil, err
	}
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), b)
}

// publicKey is the key browsers subscribe with: the uncompressed point.
func publicKey(k *ecdsa.PrivateKey) (string, error) {
	b, err := k.PublicKey.Bytes()
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

// authorization is the header that says who's sending: a token signed with
// this server's key, good for half a day, for the one push service at aud
// ("https://fcm.googleapis.com"). contact is where the service's people
// could reach whoever runs this server.
func authorization(k *ecdsa.PrivateKey, aud, contact string, now time.Time) (string, error) {
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": aud, "exp": now.Add(12 * time.Hour).Unix(), "sub": contact})
	if err != nil {
		return "", err
	}
	signed := head + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, k, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	pub, err := publicKey(k)
	if err != nil {
		return "", err
	}
	return "vapid t=" + signed + "." + b64.EncodeToString(sig) + ", k=" + pub, nil
}
