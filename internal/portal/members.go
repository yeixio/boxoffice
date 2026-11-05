package portal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/yeixio/boxoffice/internal/store"
)

// ErrBadSignIn means the username and password don't match.
var ErrBadSignIn = errors.New("that username and password don't match")

func encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// CheckUsername says what's wrong with a username for a local account.
func CheckUsername(name string) error {
	name = store.MemberName(name)
	switch {
	case name == "":
		return badf("enter a username")
	case utf8.RuneCountInString(name) > 40:
		return badf("keep the username to 40 characters")
	case strings.ContainsAny(name, " \t\r\n"):
		return badf("use a username without spaces")
	}
	return nil
}

// CheckPassword says what's wrong with a password for a local account.
func CheckPassword(p string) error {
	if len(p) < 8 {
		return badf("use a password of at least 8 characters")
	}
	if len(p) > 72 {
		return badf("use a password of at most 72 characters")
	}
	return nil
}

// HashPassword hashes a local account's password.
func HashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

// dummyHash is compared against when there's no such account, so a wrong
// username takes as long as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not a real password"), bcrypt.DefaultCost)

// CreateLocal makes a local account, for an admin or for someone signing
// up.
func (s *Service) CreateLocal(ctx context.Context, m store.Member, password string) (store.Member, error) {
	if err := CheckUsername(m.Username); err != nil {
		return m, err
	}
	if err := CheckPassword(password); err != nil {
		return m, err
	}
	m.DisplayName = strings.TrimSpace(m.DisplayName)
	if utf8.RuneCountInString(m.DisplayName) > 60 {
		return m, badf("keep the name to 60 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return m, err
	}
	m.Source = "local"
	created, err := s.Store.CreateMember(ctx, m, hash)
	if errors.Is(err, store.ErrExists) {
		return m, badf("someone has that username already")
	}
	return created, err
}

// SignUp is someone making their own account, when the admin allows it.
func (s *Service) SignUp(ctx context.Context, username, displayName, password string) (store.Member, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return store.Member{}, err
	}
	if !st.Enabled {
		return store.Member{}, ErrOff
	}
	if !st.LocalSignIn || !st.SignUp {
		return store.Member{}, badf("ask an admin for an account")
	}
	return s.CreateLocal(ctx, store.Member{Username: username, DisplayName: displayName, QuotaMovies: -1, QuotaShows: -1}, password)
}

// SignIn checks a username and password: a local account's here, or a
// Matinee account's with Matinee. Someone signing in with Matinee for the
// first time gets an account here.
func (s *Service) SignIn(ctx context.Context, username, password string, withMatinee bool) (store.Member, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return store.Member{}, err
	}
	if !st.Enabled {
		return store.Member{}, ErrOff
	}
	if withMatinee {
		if st.MatineeURL == "" {
			return store.Member{}, badf("signing in with Matinee is turned off")
		}
		return s.matineeSignIn(ctx, st.MatineeURL, username, password)
	}
	if !st.LocalSignIn {
		return store.Member{}, badf("sign in with your Matinee account")
	}
	m, err := s.Store.MemberByName(ctx, username)
	hash := dummyHash
	if err == nil && m.Source == "local" {
		hash = []byte(m.PasswordHash())
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || err != nil || m.Source != "local" {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return m, err
		}
		if err == nil && m.Source == "matinee" {
			return m, badf("this account signs in with Matinee")
		}
		return m, ErrBadSignIn
	}
	if m.Disabled {
		return m, badf("this account is turned off; ask an admin")
	}
	return m, nil
}

// matineeUser is the part of Matinee's sign-in answer Box Office uses.
type matineeUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Guest    bool   `json:"guest"`
}

// matineeSignIn checks a password with Matinee: it signs in there, as a
// device named "Box Office request site", and straight back out.
func (s *Service) matineeSignIn(ctx context.Context, base, username, password string) (store.Member, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return store.Member{}, ErrBadSignIn
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password, "deviceName": "Box Office request site", "platform": "web"})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return store.Member{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := s.client().Do(req)
	if err != nil {
		s.Log.Warn("couldn't reach Matinee to sign someone in", "url", base, "err", err)
		return store.Member{}, badf("Matinee isn't answering; try again soon")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return store.Member{}, ErrBadSignIn
	case res.StatusCode == http.StatusTooManyRequests:
		return store.Member{}, badf("Matinee says too many wrong passwords; try again in a few minutes")
	case res.StatusCode != http.StatusOK:
		s.Log.Warn("Matinee turned a sign-in down", "url", base, "status", res.StatusCode)
		return store.Member{}, badf("Matinee couldn't sign you in (it answered %d)", res.StatusCode)
	}
	var out struct {
		User  matineeUser `json:"user"`
		Token string      `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.User.Username == "" {
		return store.Member{}, badf("that address doesn't look like Matinee")
	}
	s.matineeSignOut(base, out.Token)
	if out.User.Guest {
		return store.Member{}, badf("sign in with your own Matinee account, not the home one")
	}
	name := store.MemberName(out.User.Username)
	m, err := s.Store.MemberByName(ctx, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		m, err = s.Store.CreateMember(ctx, store.Member{Username: name, DisplayName: out.User.Username, Source: "matinee", QuotaMovies: -1, QuotaShows: -1}, "")
		if errors.Is(err, store.ErrExists) {
			return s.Store.MemberByName(ctx, name)
		}
		return m, err
	case err != nil:
		return m, err
	case m.Source != "matinee":
		return m, badf("there's an account named %s here already; sign in with its password", name)
	case m.Disabled:
		return m, badf("this account is turned off; ask an admin")
	}
	return m, nil
}

// matineeSignOut ends the session the check made, so it doesn't linger in
// the person's list of devices.
func (s *Service) matineeSignOut(base, token string) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/v1/auth/logout", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if res, err := s.client().Do(req); err == nil {
		res.Body.Close()
	}
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// EditMember is what an admin can change about someone.
type EditMember struct {
	DisplayName *string `json:"displayName"`
	AutoApprove *string `json:"autoApprove"`
	QuotaMovies *int    `json:"quotaMovies"`
	QuotaShows  *int    `json:"quotaShows"`
	Disabled    *bool   `json:"disabled"`
	// Password sets a new password for a local account.
	Password *string `json:"password"`
}

// Edit changes someone's account.
func (s *Service) Edit(ctx context.Context, id int64, in EditMember) (store.Member, error) {
	m, err := s.Store.Member(ctx, id)
	if err != nil {
		return m, err
	}
	if in.DisplayName != nil {
		m.DisplayName = strings.TrimSpace(*in.DisplayName)
		if utf8.RuneCountInString(m.DisplayName) > 60 {
			return m, badf("keep the name to 60 characters")
		}
	}
	if in.AutoApprove != nil {
		switch *in.AutoApprove {
		case "", "on", "off":
			m.AutoApprove = *in.AutoApprove
		default:
			return m, badf("approval is on, off, or follows Settings")
		}
	}
	for _, q := range []*int{in.QuotaMovies, in.QuotaShows} {
		if q != nil && *q < -1 {
			return m, badf("a weekly limit can't be below 0")
		}
	}
	if in.QuotaMovies != nil {
		m.QuotaMovies = *in.QuotaMovies
	}
	if in.QuotaShows != nil {
		m.QuotaShows = *in.QuotaShows
	}
	if in.Disabled != nil {
		m.Disabled = *in.Disabled
	}
	if in.Password != nil {
		if m.Source != "local" {
			return m, badf("%s signs in with Matinee, so their password is set there", m.Name())
		}
		if err := CheckPassword(*in.Password); err != nil {
			return m, err
		}
		hash, err := HashPassword(*in.Password)
		if err != nil {
			return m, err
		}
		if err := s.Store.SetMemberPassword(ctx, id, hash); err != nil {
			return m, err
		}
	}
	if err := s.Store.UpdateMember(ctx, m); err != nil {
		return m, err
	}
	return s.Store.Member(ctx, id)
}

// ChangeOwnPassword is someone with a local account changing its password.
func (s *Service) ChangeOwnPassword(ctx context.Context, m store.Member, current, next string) error {
	if m.Source != "local" {
		return badf("you sign in with Matinee, so change your password there")
	}
	if bcrypt.CompareHashAndPassword([]byte(m.PasswordHash()), []byte(current)) != nil {
		return badf("the current password isn't right")
	}
	if err := CheckPassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.Store.SetMemberPassword(ctx, m.ID, hash)
}
