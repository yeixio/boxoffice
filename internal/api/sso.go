package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/sso"
)

// Single sign-on (internal/sso): signing in to Box Office and to the
// request site through an OpenID Connect provider. The browser goes to
// /api/sso/start, on to the provider, and comes back to
// /api/sso/callback, which is the address the provider is given.

const (
	ssoCookie = "boxoffice_sso"
	// ssoVisit is how long someone has to sign in at the provider.
	ssoVisit = 10 * time.Minute
)

func (s *Server) ssoRoutes() []route {
	return []route{
		{"GET /api/v1/sso", true, s.ssoSettings},
		{"PUT /api/v1/sso", true, s.saveSSOSettings},
		{"GET /api/v1/sso/test/{state}", true, s.ssoTestResult},
		{"GET /api/sso/start", false, s.ssoStart},
		{"GET /api/sso/callback", false, s.ssoCallback},
	}
}

// ssoState is what the server keeps for single sign-on between requests.
type ssoState struct {
	once   sync.Once
	client *sso.Client

	mu    sync.Mutex
	tests map[string]ssoTest
}

// ssoTest is what a trial sign-in from Settings found.
type ssoTest struct {
	Identity sso.Identity `json:"identity"`
	// Admin and Member say what that account would be let in to.
	Admin  bool      `json:"admin"`
	Member bool      `json:"member"`
	Error  string    `json:"error,omitempty"`
	at     time.Time `json:"-"`
}

func (s *Server) ssoClient() *sso.Client {
	s.sso.once.Do(func() {
		if s.SSO == nil {
			s.SSO = &sso.Client{}
		}
		s.sso.client = s.SSO
	})
	return s.sso.client
}

func (s *Server) readSSO(r *http.Request) (sso.Settings, error) {
	st := sso.Settings{Admins: []string{}}
	raw, err := s.Store.Setting(r.Context(), sso.SettingKey)
	if err != nil || raw == "" {
		return st, err
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return sso.Settings{Admins: []string{}}, nil
	}
	if st.Admins == nil {
		st.Admins = []string{}
	}
	return st, nil
}

// ssoAdmins says whether single sign-on lets anyone in to Box Office
// itself, and under what name the sign-in page offers it.
func (s *Server) ssoAdmins(r *http.Request) (string, bool) {
	st, err := s.readSSO(r)
	return st.Name, err == nil && st.Enabled && (len(st.Admins) > 0 || st.AdminGroup != "")
}

// ssoMembers is the same for the request site.
func (s *Server) ssoMembers(r *http.Request) (string, bool) {
	st, err := s.readSSO(r)
	return st.Name, err == nil && st.Enabled && st.Members
}

// ssoView is the settings as the page gets them: without the secret.
type ssoView struct {
	sso.Settings
	HasSecret bool `json:"hasSecret"`
	// PortalHost is the request site's own host name, which needs its own
	// address registered with the provider.
	PortalHost string `json:"portalHost"`
}

func (s *Server) ssoViewOf(r *http.Request, st sso.Settings) ssoView {
	v := ssoView{Settings: st, HasSecret: st.ClientSecret != ""}
	v.ClientSecret = ""
	if s.Portal != nil {
		if ps, err := s.Portal.Settings(r.Context()); err == nil && ps.Enabled {
			v.PortalHost = ps.Host
		}
	}
	return v
}

func (s *Server) ssoSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.readSSO(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.ssoViewOf(r, st))
}

func (s *Server) saveSSOSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		sso.Settings
		// ClearSecret removes the saved secret; an empty secret keeps it.
		ClearSecret bool `json:"clearSecret"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	old, err := s.readSSO(r)
	if err != nil {
		return err
	}
	st := in.Settings
	if st.ClientSecret == "" && !in.ClearSecret {
		st.ClientSecret = old.ClientSecret
	}
	if err := st.Check(); err != nil {
		return ssoErr(err)
	}
	// Turning it on reads the provider's settings first, so a wrong
	// address is said here and not at the sign-in page.
	if st.Enabled {
		if err := s.ssoClient().Check(r.Context(), st); err != nil {
			return ssoErr(err)
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := s.Store.SetSetting(r.Context(), sso.SettingKey, string(raw)); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.ssoViewOf(r, st))
}

func ssoErr(err error) error {
	var se *sso.Error
	if errors.As(err, &se) {
		return errorf(http.StatusBadRequest, se.Message)
	}
	return err
}

// origin is the address the browser reached Box Office at.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// ssoBack sends the browser back to a page with what happened, for the
// page to say: refused (said no at the provider), notallowed (signed in
// there, but not an account that's let in here) or failed.
func (s *Server) ssoBack(w http.ResponseWriter, r *http.Request, what, outcome string) {
	to := "/"
	switch {
	case what == "test":
		to = "/settings"
	case what == "member" && !s.portalHost(r):
		to = "/request"
	}
	if outcome != "" {
		to += "?sso=" + url.QueryEscape(outcome)
	}
	if what == "test" {
		to += "#sso"
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ssoStart sends the browser to the provider. for says what the sign-in
// is for: admin (Box Office itself), member (the request site) or test (a
// trial from Settings, which signs nobody in).
func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) error {
	what := r.URL.Query().Get("for")
	st, err := s.readSSO(r)
	if err != nil {
		return err
	}
	switch what {
	case "admin":
		// Box Office's own sign-in isn't offered on the request site's host.
		if _, on := s.ssoAdmins(r); !on || s.portalHost(r) {
			return errorf(http.StatusNotFound, "single sign-on isn't turned on")
		}
	case "member":
		if _, on := s.ssoMembers(r); !on {
			return errorf(http.StatusNotFound, "single sign-on isn't turned on")
		}
	case "test":
		// Only an admin tries it out, before it's turned on too.
		if _, ok := s.authenticate(r); !ok || s.portalHost(r) {
			return errorf(http.StatusUnauthorized, "sign in first")
		}
		if err := st.Check(); err != nil {
			return ssoErr(err)
		}
		if st.Issuer == "" || st.ClientID == "" {
			return errorf(http.StatusBadRequest, "save the provider's address and client ID first")
		}
	default:
		return errorf(http.StatusBadRequest, "say what the sign-in is for")
	}
	to, visit, err := s.ssoClient().Start(r.Context(), st, what, origin(r)+"/api/sso/callback")
	if err != nil {
		s.Log.Warn("single sign-on couldn't start", "err", err)
		if what == "test" {
			return ssoErr(err)
		}
		s.ssoBack(w, r, what, "failed")
		return nil
	}
	raw, err := json.Marshal(visit)
	if err != nil {
		return err
	}
	// Lax, so it comes back with the browser from the provider's site.
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/api/sso", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure(r), MaxAge: int(ssoVisit.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusSeeOther)
	return nil
}

// ssoCallback is where the provider sends the browser back to. It checks
// the answer belongs to the visit this browser started, asks the provider
// who signed in, and signs them in if they're let in.
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) error {
	var visit sso.Visit
	if c, err := r.Cookie(ssoCookie); err == nil {
		if raw, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil {
			json.Unmarshal(raw, &visit)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/sso", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	what := visit.For
	switch {
	case visit.State == "" || !constEq(q.Get("state"), visit.State):
		// Not an answer to a visit this browser started.
		s.Log.Warn("single sign-on answer without a matching visit", "from", s.clientIP(r))
		s.ssoBack(w, r, what, "failed")
		return nil
	case q.Get("error") != "":
		s.Log.Warn("single sign-on refused at the provider", "error", q.Get("error"), "description", q.Get("error_description"))
		s.ssoDone(w, r, visit, ssoTest{Error: sso.ErrRefused.Error()}, "refused")
		return nil
	}
	st, err := s.readSSO(r)
	if err != nil {
		return err
	}
	id, err := s.ssoClient().Finish(r.Context(), st, visit, q.Get("code"))
	if err != nil {
		s.Log.Warn("single sign-on failed", "for", what, "err", err)
		s.ssoDone(w, r, visit, ssoTest{Error: err.Error()}, "failed")
		return nil
	}
	found := ssoTest{Identity: id, Admin: st.Admin(id), Member: st.Member(id)}
	switch what {
	case "admin":
		if _, on := s.ssoAdmins(r); !on || s.portalHost(r) || !found.Admin {
			s.Log.Warn("single sign-on account isn't an admin", "subject", id.Subject, "username", id.Username, "email", id.Email, "from", s.clientIP(r))
			s.ssoBack(w, r, what, "notallowed")
			return nil
		}
		u, err := s.Store.FirstUser(r.Context())
		if err != nil {
			return err
		}
		if _, err := s.sessionCookie(w, r, u); err != nil {
			return err
		}
		s.Log.Info("signed in with single sign-on", "username", id.Username, "email", id.Email)
		s.ssoBack(w, r, what, "")
	case "member":
		ps, err := s.Portal.Settings(r.Context())
		if err != nil {
			return err
		}
		if _, on := s.ssoMembers(r); !on || !ps.Enabled || !found.Member {
			s.Log.Warn("single sign-on account isn't let in to the request site", "subject", id.Subject, "username", id.Username, "from", s.clientIP(r))
			s.ssoBack(w, r, what, "notallowed")
			return nil
		}
		name := id.Username
		if name == "" {
			name = id.Email
		}
		m, err := s.Portal.SSOMember(r.Context(), id.Subject, name, id.Name)
		if err != nil {
			s.Log.Warn("single sign-on member refused", "username", name, "err", err)
			s.ssoBack(w, r, what, "notallowed")
			return nil
		}
		tok, err := s.Store.CreateMemberSession(r.Context(), m.ID, sessionTTL)
		if err != nil {
			return err
		}
		s.setMemberCookie(w, r, tok, int(sessionTTL.Seconds()))
		s.ssoBack(w, r, what, "")
	default:
		s.ssoDone(w, r, visit, found, "")
	}
	return nil
}

// ssoDone ends a visit that signs nobody in: a trial's findings are kept
// for the Settings page to fetch, and anything else goes back with the
// outcome.
func (s *Server) ssoDone(w http.ResponseWriter, r *http.Request, visit sso.Visit, found ssoTest, outcome string) {
	if visit.For != "test" {
		s.ssoBack(w, r, visit.For, outcome)
		return
	}
	found.at = time.Now()
	s.sso.mu.Lock()
	if s.sso.tests == nil {
		s.sso.tests = map[string]ssoTest{}
	}
	for k, t := range s.sso.tests {
		if time.Since(t.at) > ssoVisit || len(s.sso.tests) > 20 {
			delete(s.sso.tests, k)
		}
	}
	s.sso.tests[visit.State] = found
	s.sso.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/settings?sso_test="+url.QueryEscape(visit.State)+"#sso", http.StatusSeeOther)
}

// ssoTestResult gives the Settings page what a trial found, once.
func (s *Server) ssoTestResult(w http.ResponseWriter, r *http.Request) error {
	state := r.PathValue("state")
	s.sso.mu.Lock()
	found, ok := s.sso.tests[state]
	delete(s.sso.tests, state)
	s.sso.mu.Unlock()
	if !ok || time.Since(found.at) > ssoVisit {
		return errorf(http.StatusNotFound, "that trial is over; try again")
	}
	return writeJSON(w, http.StatusOK, found)
}
