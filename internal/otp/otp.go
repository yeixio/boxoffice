// Package otp makes and checks one-time codes from an authenticator app
// (TOTP, RFC 6238): six digits that change every 30 seconds.
package otp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Step is how long a code lasts.
const Step = 30 * time.Second

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret makes the key an authenticator app and Box Office share.
func NewSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return enc.EncodeToString(b)
}

// URI is the address an authenticator app reads the key from, as a link or
// a QR code. account is who it's for; issuer is the app's name for it.
func URI(secret, issuer, account string) string {
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

// At is the code for the step counter n.
func At(secret string, n int64) (string, error) {
	key, err := enc.DecodeString(strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secret, " ", ""), "=")))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("the one-time code's key can't be read")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(n))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000), nil
}

// Counter is the step a moment falls in.
func Counter(t time.Time) int64 { return t.Unix() / int64(Step.Seconds()) }

// Clean takes the spaces and dashes out of a code as someone typed it.
func Clean(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

// Check says whether code is right at now, allowing the step before and
// after for clocks that disagree. It gives the step the code was for, and
// takes none at or before last, the step of the last code used, so a code
// works once.
func Check(secret, code string, now time.Time, last int64) (int64, bool) {
	code = Clean(code)
	if len(code) != 6 {
		return 0, false
	}
	n := Counter(now)
	for _, c := range []int64{n, n - 1, n + 1} {
		if c <= last {
			continue
		}
		want, err := At(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// recoveryLetters leaves out the ones that look alike.
const recoveryLetters = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes makes n codes to keep somewhere safe, each good once
// when the authenticator app is gone, as xxxxx-xxxxx.
func NewRecoveryCodes(n int) []string {
	out := make([]string, n)
	for i := range out {
		b := make([]byte, 10)
		rand.Read(b)
		var s strings.Builder
		for j, x := range b {
			if j == 5 {
				s.WriteByte('-')
			}
			s.WriteByte(recoveryLetters[int(x)%len(recoveryLetters)])
		}
		out[i] = s.String()
	}
	return out
}

// HashRecovery is how a recovery code is kept: not as itself.
func HashRecovery(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(Clean(code))))
	return hex.EncodeToString(sum[:])
}
