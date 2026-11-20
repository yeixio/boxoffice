package otp

import (
	"strings"
	"testing"
	"time"
)

// The key of RFC 6238's examples, "12345678901234567890".
const rfc = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestCodesMatchTheStandard(t *testing.T) {
	// RFC 6238 appendix B, the last six of its eight digits.
	for at, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := At(rfc, Counter(time.Unix(at, 0)))
		if err != nil || got != want {
			t.Errorf("at %d: %q, %v; want %q", at, got, err, want)
		}
	}
}

func TestCheck(t *testing.T) {
	now := time.Unix(1111111109, 0)
	n := Counter(now)
	if got, ok := Check(rfc, "081 804", now, 0); !ok || got != n {
		t.Fatalf("the right code: %d, %v", got, ok)
	}
	if _, ok := Check(rfc, "081804", now, n); ok {
		t.Fatal("a code worked twice")
	}
	if _, ok := Check(rfc, "081804", now.Add(30*time.Second), 0); !ok {
		t.Fatal("the code before should do for a slow clock")
	}
	if _, ok := Check(rfc, "081804", now.Add(90*time.Second), 0); ok {
		t.Fatal("an old code worked")
	}
	for _, bad := range []string{"", "081805", "81804", "0818044", "abcdef"} {
		if _, ok := Check(rfc, bad, now, 0); ok {
			t.Errorf("%q worked", bad)
		}
	}
}

func TestSecretsAndRecoveryCodes(t *testing.T) {
	s := NewSecret()
	if len(s) != 32 || s == NewSecret() {
		t.Fatalf("secret %q", s)
	}
	if _, err := At(strings.ToLower(s[:16])+" "+s[16:], 1); err != nil {
		t.Fatalf("a key typed in lower case with a space: %v", err)
	}
	uri := URI(s, "Box Office", "admin")
	if !strings.HasPrefix(uri, "otpauth://totp/Box%20Office:admin?") || !strings.Contains(uri, "secret="+s) || !strings.Contains(uri, "issuer=Box%20Office") {
		t.Fatalf("uri %q", uri)
	}
	codes := NewRecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
	}
	if HashRecovery(" "+strings.ToUpper(codes[0])+" ") != HashRecovery(strings.ReplaceAll(codes[0], "-", "")) {
		t.Fatal("a recovery code typed another way should be the same code")
	}
}
