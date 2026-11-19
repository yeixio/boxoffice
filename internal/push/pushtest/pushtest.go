// Package pushtest stands in for a browser that takes push notifications
// and the push service that reaches it, for tests.
package pushtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

var b64 = base64.RawURLEncoding

// Device is a browser with a push subscription.
type Device struct {
	// Endpoint, P256dh and Auth are what the browser hands the server.
	Endpoint, P256dh, Auth string

	key  *ecdh.PrivateKey
	auth []byte
	hs   *httptest.Server

	mu       sync.Mutex
	got      []map[string]string
	headers  []http.Header
	answer   int
	problems []error
}

// New starts a device and its push service. The service only speaks plain
// HTTP on this machine, so the sender needs AnyAddress.
func New(t testing.TB) *Device {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{key: key, auth: make([]byte, 16), answer: http.StatusCreated}
	rand.Read(d.auth)
	d.hs = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.hs.Close)
	d.Endpoint = d.hs.URL + "/send/" + b64.EncodeToString(d.auth[:6])
	d.P256dh = b64.EncodeToString(key.PublicKey().Bytes())
	d.Auth = b64.EncodeToString(d.auth)
	return d
}

// JSON is the subscription as a browser sends it.
func (d *Device) JSON() string {
	j, _ := json.Marshal(map[string]any{"endpoint": d.Endpoint, "keys": map[string]string{"p256dh": d.P256dh, "auth": d.Auth}})
	return string(j)
}

// Answer makes the push service answer with a status from now on: 410 for
// a subscription the browser dropped.
func (d *Device) Answer(status int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answer = status
}

// Got is the messages that arrived, opened, oldest first.
func (d *Device) Got() []map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]string{}, d.got...)
}

// Headers is the headers each message came with.
func (d *Device) Headers() []http.Header {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]http.Header{}, d.headers...)
}

// Problems is what was wrong with messages that couldn't be opened.
func (d *Device) Problems() []error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]error{}, d.problems...)
}

func (d *Device) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.answer >= 300 {
		w.WriteHeader(d.answer)
		return
	}
	text, err := Open(body, d.key, d.auth)
	var m map[string]string
	if err == nil {
		err = json.Unmarshal(text, &m)
	}
	if err == nil && r.Header.Get("Content-Encoding") != "aes128gcm" {
		err = errors.New("not sent as aes128gcm")
	}
	if err != nil {
		d.problems = append(d.problems, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	d.got = append(d.got, m)
	d.headers = append(d.headers, r.Header.Clone())
	w.WriteHeader(d.answer)
}

// Open is the browser's side of RFC 8291: it opens a message sealed for
// the key and auth secret.
func Open(body []byte, key *ecdh.PrivateKey, auth []byte) ([]byte, error) {
	if len(body) < 21 || len(body) < 21+int(body[20]) {
		return nil, errors.New("too short")
	}
	salt, theirs, sealed := body[:16], body[21:21+int(body[20])], body[21+int(body[20]):]
	pub, err := ecdh.P256().NewPublicKey(theirs)
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(pub)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), key.PublicKey().Bytes()...)
	info = append(info, theirs...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	text, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, err
	}
	// The text ends with a 2 and then any padding.
	for len(text) > 0 && text[len(text)-1] == 0 {
		text = text[:len(text)-1]
	}
	if len(text) == 0 || text[len(text)-1] != 2 {
		return nil, errors.New("the last record isn't marked as the last")
	}
	return text[:len(text)-1], nil
}
