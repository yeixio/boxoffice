package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yeixio/boxoffice/internal/otp"
	"github.com/yeixio/boxoffice/internal/passkey"
	"github.com/yeixio/boxoffice/internal/store"
)

// Passkeys (internal/passkey) for the admin and for request-site members,
// and a one-time code from an authenticator app (internal/otp) as a second
// step after the admin's password.

const (
	// passkeyWait is how long someone has to answer their authenticator.
	passkeyWait = 5 * time.Minute
	// maxPasskeys is how many one account may have.
	maxPasskeys = 20
	// settingCode holds the one-time code's key and recovery codes.
	settingCode = "sign_in_code"
)

func (s *Server) passkeyRoutes() []route {
	admin := func(fn func(http.ResponseWriter, *http.Request, pkOwner) error) func(http.ResponseWriter, *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			u := user(r)
			if u.ID == 0 {
				return errorf(http.StatusForbidden, "sign in as an admin to change how you sign in")
			}
			return fn(w, r, pkOwner{name: u.Username, handle: fmt.Sprintf("a:%d", u.ID)})
		}
	}
	member := func(fn func(http.ResponseWriter, *http.Request, pkOwner) error) func(http.ResponseWriter, *http.Request) error {
		return s.asMember(func(w http.ResponseWriter, r *http.Request, m store.Member) error {
			if m.Source != "local" {
				return errorf(http.StatusBadRequest, "passkeys are for accounts made here; this one signs in somewhere else")
			}
			return fn(w, r, pkOwner{member: m.ID, name: m.Username, display: m.Name(), handle: fmt.Sprintf("m:%d", m.ID)})
		})
	}
	return []route{
		{"GET /api/v1/passkeys", true, admin(s.passkeyList)},
		{"POST /api/v1/passkeys/start", true, admin(s.passkeyStart)},
		{"POST /api/v1/passkeys", true, admin(s.passkeyAdd)},
		{"DELETE /api/v1/passkeys/{id}", true, admin(s.passkeyDelete)},
		{"POST /api/v1/passkeys/challenge", false, func(w http.ResponseWriter, r *http.Request) error { return s.passkeyChallenge(w, r, false) }},
		{"POST /api/v1/passkeys/login", false, func(w http.ResponseWriter, r *http.Request) error { return s.passkeyLogin(w, r, false) }},

		{"GET /api/portal/passkeys", false, member(s.passkeyList)},
		{"POST /api/portal/passkeys/start", false, member(s.passkeyStart)},
		{"POST /api/portal/passkeys", false, member(s.passkeyAdd)},
		{"POST /api/portal/passkeys/{id}/remove", false, member(s.passkeyDelete)},
		{"POST /api/portal/passkeys/challenge", false, func(w http.ResponseWriter, r *http.Request) error { return s.passkeyChallenge(w, r, true) }},
		{"POST /api/portal/passkeys/login", false, func(w http.ResponseWriter, r *http.Request) error { return s.passkeyLogin(w, r, true) }},

		{"GET /api/v1/code", true, admin(s.codeState)},
		{"POST /api/v1/code/start", true, admin(s.codeStart)},
		{"POST /api/v1/code/confirm", true, admin(s.codeConfirm)},
		{"POST /api/v1/code/off", true, admin(s.codeOff)},
	}
}

// pkOwner is whose passkeys a request is about: the admin (member 0) or a
// request-site member.
type pkOwner struct {
	member        int64
	name, display string
	// handle is what the authenticator keeps to tell accounts apart.
	handle string
}

// pkPending is a challenge that's been handed out and not answered yet.
type pkPending struct {
	// register is a new passkey for owner; otherwise it's a sign-in, to
	// the request site when members is set.
	register bool
	owner    int64
	members  bool
	at       time.Time
}

type passkeyState struct {
	mu      sync.Mutex
	pending map[string]pkPending
}

// ask hands out a challenge.
func (p *passkeyState) ask(v pkPending) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, x := range p.pending {
		if now.Sub(x.at) > passkeyWait {
			delete(p.pending, k)
		}
	}
	if len(p.pending) >= 5000 {
		return "", errorf(http.StatusTooManyRequests, "too many people are signing in just now; try again in a few minutes")
	}
	if p.pending == nil {
		p.pending = map[string]pkPending{}
	}
	c := passkey.NewChallenge()
	v.at = now
	p.pending[c] = v
	return c, nil
}

// answered takes a challenge back: each is good for one answer.
func (p *passkeyState) answered(challenge string) (pkPending, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.pending[challenge]
	delete(p.pending, challenge)
	return v, ok && time.Since(v.at) <= passkeyWait
}

// passkeySite is the name passkeys are made for at the address the browser
// came by, or why there can't be any there.
func passkeySite(r *http.Request) (string, error) {
	id, ok := passkey.RPID(r.Host)
	if !ok {
		return "", errorf(http.StatusBadRequest, "passkeys need Box Office opened by a name, such as box.example.com, not by a number address")
	}
	if !secure(r) && id != "localhost" {
		return "", errorf(http.StatusBadRequest, "passkeys need Box Office opened over https")
	}
	return id, nil
}

func passkeyErr(err error) error {
	var pe *passkey.Error
	if errors.As(err, &pe) {
		return errorf(http.StatusBadRequest, pe.Message)
	}
	return err
}

func (s *Server) passkeyList(w http.ResponseWriter, r *http.Request, o pkOwner) error {
	list, err := s.Store.Passkeys(r.Context(), o.member)
	if err != nil {
		return err
	}
	host, _ := passkey.RPID(r.Host)
	return writeJSON(w, http.StatusOK, map[string]any{"passkeys": list, "host": host})
}

// passkeyStart gives the browser what it needs to make a passkey.
func (s *Server) passkeyStart(w http.ResponseWriter, r *http.Request, o pkOwner) error {
	site, err := passkeySite(r)
	if err != nil {
		return err
	}
	have, err := s.Store.Passkeys(r.Context(), o.member)
	if err != nil {
		return err
	}
	if len(have) >= maxPasskeys {
		return errorf(http.StatusBadRequest, "that's as many passkeys as one account can have; remove one first")
	}
	// The authenticator is told which it holds already, so it doesn't
	// make a second for the same account.
	exclude := []string{}
	for _, p := range have {
		if p.Host == site {
			exclude = append(exclude, p.Credential)
		}
	}
	challenge, err := s.pk.ask(pkPending{register: true, owner: o.member})
	if err != nil {
		return err
	}
	name := "Box Office"
	if o.member != 0 {
		if st, err := s.Portal.Settings(r.Context()); err == nil && st.Name != "" {
			name = st.Name
		}
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"challenge": challenge,
		"rp":        map[string]string{"id": site, "name": name},
		"user": map[string]string{"id": base64.RawURLEncoding.EncodeToString([]byte(o.handle)), "name": o.name,
			"displayName": cmpOr(o.display, o.name)},
		"algorithms": passkey.Algorithms,
		"exclude":    exclude,
	})
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// passkeyAdd keeps the passkey the browser made.
func (s *Server) passkeyAdd(w http.ResponseWriter, r *http.Request, o pkOwner) error {
	var in struct {
		passkey.Registration
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	site, err := passkeySite(r)
	if err != nil {
		return err
	}
	challenge := passkey.Challenge(in.ClientData)
	if p, ok := s.pk.answered(challenge); !ok || !p.register || p.owner != o.member {
		return errorf(http.StatusBadRequest, "that took too long; try again")
	}
	cred, err := passkey.Register(in.Registration, challenge, origin(r), site)
	if err != nil {
		return passkeyErr(err)
	}
	if _, err := s.Store.PasskeyByCredential(r.Context(), cred.ID); err == nil {
		return errorf(http.StatusConflict, "that passkey is here already")
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 60 {
		name = name[:60]
	}
	p, err := s.Store.AddPasskey(r.Context(), store.Passkey{Credential: cred.ID, PublicKey: base64.RawURLEncoding.EncodeToString(cred.PublicKey),
		Alg: cred.Alg, SignCount: cred.SignCount, Host: site, MemberID: o.member, Name: name})
	if err != nil {
		return err
	}
	s.Log.Info("a passkey was added", "account", o.name, "member", o.member != 0, "host", site, "from", clientIP(r))
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) passkeyDelete(w http.ResponseWriter, r *http.Request, o pkOwner) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeletePasskey(r.Context(), o.member, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// passkeyChallenge starts a sign-in: the browser asks whichever passkey
// the person picks to sign this.
func (s *Server) passkeyChallenge(w http.ResponseWriter, r *http.Request, members bool) error {
	site, err := passkeySite(r)
	if err != nil {
		return err
	}
	challenge, err := s.pk.ask(pkPending{members: members})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge, "rpId": site})
}

// passkeyLogin signs in whoever holds a passkey that's kept here. It needs
// no one-time code: the passkey checked who was using it.
func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request, members bool) error {
	var in passkey.Assertion
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	site, err := passkeySite(r)
	if err != nil {
		return err
	}
	if members {
		if st, err := s.Portal.Settings(ctx); err != nil {
			return err
		} else if !st.Enabled {
			return errorf(http.StatusNotFound, "the request site is turned off")
		}
	}
	challenge := passkey.Challenge(in.ClientData)
	if p, ok := s.pk.answered(challenge); !ok || p.register || p.members != members {
		return errorf(http.StatusBadRequest, "that took too long; try again")
	}
	unknown := errorf(http.StatusUnauthorized, "that passkey isn't one that signs in here")
	p, err := s.Store.PasskeyByCredential(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && ((p.MemberID != 0) != members || p.Host != site)) {
		s.Log.Warn("sign-in with a passkey that isn't known", "from", clientIP(r))
		return unknown
	}
	if err != nil {
		return err
	}
	key, err := base64.RawURLEncoding.DecodeString(p.PublicKey)
	if err != nil {
		return err
	}
	count, err := passkey.Verify(in, passkey.Credential{ID: p.Credential, PublicKey: key, Alg: p.Alg, SignCount: p.SignCount, RPID: p.Host}, challenge, origin(r))
	if err != nil {
		s.Log.Warn("sign-in with a passkey was turned down", "passkey", p.Name, "why", err.Error(), "from", clientIP(r))
		if errors.Is(err, passkey.ErrCopied) {
			return errorf(http.StatusUnauthorized, "that passkey looks like a copy of one used since; remove it and add it again")
		}
		return passkeyErr(err)
	}
	if err := s.Store.PasskeyUsed(ctx, p.ID, count); err != nil {
		return err
	}
	if members {
		m, err := s.Store.Member(ctx, p.MemberID)
		if err != nil || m.Disabled {
			return unknown
		}
		return s.memberSignedIn(w, r, m)
	}
	u, err := s.Store.FirstUser(ctx)
	if err != nil {
		return unknown
	}
	return s.signIn(w, r, u)
}

// ---- the one-time code ----

// signInCode is the one-time code as it's kept.
type signInCode struct {
	Secret string `json:"secret"`
	// On is set once a code from the app has been typed in, which shows
	// the app has the key.
	On bool `json:"on"`
	// Last is the step of the last code used, so each works once.
	Last int64 `json:"last"`
	// Recovery holds the recovery codes that haven't been used, hashed.
	Recovery []string `json:"recovery"`
}

func (s *Server) readCode(r *http.Request) (signInCode, error) {
	var c signInCode
	raw, err := s.Store.Setting(r.Context(), settingCode)
	if err != nil || raw == "" {
		return c, err
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return signInCode{}, nil
	}
	return c, nil
}

func (s *Server) saveCode(r *http.Request, c signInCode) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(r.Context(), settingCode, string(raw))
}

// codeWanted says whether signing in with the password also takes a code.
func (s *Server) codeWanted(r *http.Request) (bool, error) {
	if s.SkipCode {
		return false, nil
	}
	c, err := s.readCode(r)
	return c.On && c.Secret != "", err
}

// useCode takes a code typed at sign-in: one from the app, or a recovery
// code, which is used up.
func (s *Server) useCode(r *http.Request, code string) (bool, error) {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	c, err := s.readCode(r)
	if err != nil {
		return false, err
	}
	if step, ok := otp.Check(c.Secret, code, time.Now(), c.Last); ok {
		c.Last = step
		return true, s.saveCode(r, c)
	}
	h := otp.HashRecovery(code)
	for i, x := range c.Recovery {
		if x == h {
			c.Recovery = append(c.Recovery[:i:i], c.Recovery[i+1:]...)
			s.Log.Warn("a recovery code was used to sign in", "left", len(c.Recovery), "from", clientIP(r))
			return true, s.saveCode(r, c)
		}
	}
	return false, nil
}

func (s *Server) codeState(w http.ResponseWriter, r *http.Request, _ pkOwner) error {
	c, err := s.readCode(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"on": c.On, "recoveryLeft": len(c.Recovery), "skipped": s.SkipCode && c.On})
}

// codeStart makes a key for the authenticator app. Nothing is asked for at
// sign-in until a code from the app has been typed in.
func (s *Server) codeStart(w http.ResponseWriter, r *http.Request, o pkOwner) error {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	c, err := s.readCode(r)
	if err != nil {
		return err
	}
	if c.On {
		return errorf(http.StatusConflict, "the one-time code is on already; turn it off to set it up again")
	}
	c = signInCode{Secret: otp.NewSecret()}
	if err := s.saveCode(r, c); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"secret": c.Secret, "uri": otp.URI(c.Secret, "Box Office", o.name)})
}

// codeConfirm turns the code on once the app shows it has the key, and
// gives the recovery codes, this once.
func (s *Server) codeConfirm(w http.ResponseWriter, r *http.Request, _ pkOwner) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	c, err := s.readCode(r)
	if err != nil {
		return err
	}
	if c.Secret == "" || c.On {
		return errorf(http.StatusConflict, "start setting up the one-time code first")
	}
	step, ok := otp.Check(c.Secret, in.Code, time.Now(), 0)
	if !ok {
		return errorf(http.StatusBadRequest, "that code isn't right; type the 6 digits the app shows now, and check that this server's clock is right")
	}
	codes := otp.NewRecoveryCodes(10)
	c.On, c.Last, c.Recovery = true, step, nil
	for _, x := range codes {
		c.Recovery = append(c.Recovery, otp.HashRecovery(x))
	}
	if err := s.saveCode(r, c); err != nil {
		return err
	}
	s.Log.Info("the one-time code for signing in was turned on", "from", clientIP(r))
	return writeJSON(w, http.StatusOK, map[string]any{"recovery": codes})
}

// codeOff stops asking for a code. It takes the password, so a browser
// left signed in can't do it.
func (s *Server) codeOff(w http.ResponseWriter, r *http.Request, _ pkOwner) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(user(r).PasswordHash()), []byte(in.Password)) != nil {
		return errorf(http.StatusForbidden, "the password isn't right")
	}
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	if err := s.Store.SetSetting(r.Context(), settingCode, ""); err != nil {
		return err
	}
	s.Log.Info("the one-time code for signing in was turned off", "from", clientIP(r))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
