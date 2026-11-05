package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/store"
)

// The request site (internal/portal). Its own API is under /api/portal,
// signed in with a member's session cookie, which never opens /api/v1;
// admins manage it under /api/v1/portal, /api/v1/requests and
// /api/v1/issues.

const memberCookie = "boxoffice_member"

func (s *Server) portalRoutes() []route {
	return []route{
		// Admins.
		{"GET /api/v1/portal/settings", true, s.portalSettings},
		{"PUT /api/v1/portal/settings", true, s.savePortalSettings},
		{"PUT /api/v1/portal/logo", true, s.savePortalLogo},
		{"DELETE /api/v1/portal/logo", true, s.deletePortalLogo},
		{"GET /api/v1/portal/members", true, s.portalMembers},
		{"POST /api/v1/portal/members", true, s.addPortalMember},
		{"PATCH /api/v1/portal/members/{id}", true, s.editPortalMember},
		{"DELETE /api/v1/portal/members/{id}", true, s.deletePortalMember},
		{"GET /api/v1/requests", true, s.adminRequests},
		{"GET /api/v1/requests/counts", true, s.requestCounts},
		{"POST /api/v1/requests/{id}/approve", true, s.approveRequest},
		{"POST /api/v1/requests/{id}/decline", true, s.declineRequest},
		{"DELETE /api/v1/requests/{id}", true, s.deleteRequest},
		{"GET /api/v1/issues", true, s.adminIssues},
		{"PATCH /api/v1/issues/{id}", true, s.editIssue},

		// The request site.
		{"GET /api/portal/site", false, s.portalSite},
		{"GET /api/portal/logo", false, s.portalLogo},
		{"POST /api/portal/login", false, s.memberLogin},
		{"POST /api/portal/signup", false, s.memberSignUp},
		{"POST /api/portal/logout", false, s.memberLogout},
		{"PUT /api/portal/me/password", false, s.asMember(s.memberPassword)},
		{"PUT /api/portal/me/language", false, s.asMember(s.memberLanguage)},
		{"GET /api/portal/discover", false, s.asMember(func(w http.ResponseWriter, r *http.Request, _ store.Member) error { return s.discoverHome(w, r) })},
		{"GET /api/portal/discover/rows/{id}", false, s.asMember(func(w http.ResponseWriter, r *http.Request, _ store.Member) error { return s.discoverRow(w, r) })},
		{"GET /api/portal/search", false, s.asMember(s.memberSearch)},
		{"GET /api/portal/requests", false, s.asMember(s.memberRequests)},
		{"POST /api/portal/requests", false, s.asMember(s.memberAsk)},
		{"DELETE /api/portal/requests/{id}", false, s.asMember(s.memberCancel)},
		{"GET /api/portal/issues", false, s.asMember(s.memberIssues)},
		{"POST /api/portal/issues", false, s.asMember(s.memberReport)},
	}
}

// portalErr turns the request site's errors into answers.
func portalErr(err error) error {
	var pe *portal.Error
	switch {
	case errors.As(err, &pe):
		return errorf(http.StatusBadRequest, pe.Message)
	case errors.Is(err, portal.ErrOff):
		return errorf(http.StatusNotFound, "the request site is turned off")
	case errors.Is(err, portal.ErrBadSignIn):
		return errorf(http.StatusUnauthorized, err.Error())
	}
	return err
}

// asMember runs fn for someone signed in to the request site, while it's on.
func (s *Server) asMember(fn func(http.ResponseWriter, *http.Request, store.Member) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		st, err := s.Portal.Settings(r.Context())
		if err != nil {
			return err
		}
		if !st.Enabled {
			return portalErr(portal.ErrOff)
		}
		m, ok := s.member(r)
		if !ok {
			return errorf(http.StatusUnauthorized, "sign in first")
		}
		// As for admins: a browser's changes come as JSON, which another
		// site can't send without asking first.
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Requested-With") == "" &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			return errorf(http.StatusForbidden, "send changes as JSON")
		}
		return portalErr(fn(w, r, m))
	}
}

func (s *Server) member(r *http.Request) (store.Member, bool) {
	c, err := r.Cookie(memberCookie)
	if err != nil || c.Value == "" {
		return store.Member{}, false
	}
	m, err := s.Store.SessionMember(r.Context(), c.Value)
	return m, err == nil
}

// memberView is what the request site knows about the person signed in.
type memberView struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Language    string `json:"language"`
}

func viewMember(m store.Member) memberView {
	return memberView{ID: m.ID, Username: m.Username, DisplayName: m.DisplayName, Name: m.Name(), Source: m.Source, Language: m.Language}
}

// portalSite is what the request site needs before anyone signs in: its
// name, look and ways to sign in, and who's signed in.
func (s *Server) portalSite(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Portal.Settings(r.Context())
	if err != nil {
		return err
	}
	logo, err := s.Portal.Logo(r.Context())
	if err != nil {
		return err
	}
	out := map[string]any{
		"enabled": st.Enabled, "name": st.Name, "welcome": st.Welcome, "accent": st.Accent, "hasLogo": logo != "",
		"localSignIn": st.LocalSignIn, "signUp": st.SignUp, "matinee": st.MatineeURL != "",
	}
	if st.Enabled {
		if m, ok := s.member(r); ok {
			out["member"] = viewMember(m)
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) portalLogo(w http.ResponseWriter, r *http.Request) error {
	logo, err := s.Portal.Logo(r.Context())
	if err != nil {
		return err
	}
	mime, data, ok := decodeDataURL(logo)
	if !ok {
		return store.ErrNotFound
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
	return nil
}

func (s *Server) setMemberCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: memberCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", MaxAge: maxAge})
}

func (s *Server) memberSignedIn(w http.ResponseWriter, r *http.Request, m store.Member) error {
	tok, err := s.Store.CreateMemberSession(r.Context(), m.ID, sessionTTL)
	if err != nil {
		return err
	}
	s.setMemberCookie(w, r, tok, int(sessionTTL.Seconds()))
	return writeJSON(w, http.StatusOK, map[string]any{"member": viewMember(m)})
}

func (s *Server) memberLogin(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		credentials
		Matinee bool `json:"matinee"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	name := store.MemberName(in.Username)
	fv, _ := s.failures.LoadOrStore("member:"+name, &failure{})
	f := fv.(*failure)
	f.mu.Lock()
	wait := time.Until(f.until)
	f.mu.Unlock()
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		return errorf(http.StatusTooManyRequests, "too many wrong passwords; try again in a few minutes")
	}
	m, err := s.Portal.SignIn(r.Context(), in.Username, in.Password, in.Matinee)
	if errors.Is(err, portal.ErrBadSignIn) {
		f.mu.Lock()
		f.count++
		if f.count >= 5 {
			f.until = time.Now().Add(time.Duration(min(f.count-4, 15)) * time.Minute)
		}
		f.mu.Unlock()
		s.Log.Warn("wrong request site sign-in", "username", name, "matinee", in.Matinee, "from", clientIP(r))
	}
	if err != nil {
		return portalErr(err)
	}
	s.failures.Delete("member:" + name)
	return s.memberSignedIn(w, r, m)
}

func (s *Server) memberSignUp(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		credentials
		DisplayName string `json:"displayName"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	m, err := s.Portal.SignUp(r.Context(), in.Username, in.DisplayName, in.Password)
	if err != nil {
		return portalErr(err)
	}
	s.Log.Info("someone signed up for the request site", "username", m.Username, "from", clientIP(r))
	return s.memberSignedIn(w, r, m)
}

func (s *Server) memberLogout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(memberCookie); err == nil {
		s.Store.DeleteMemberSession(r.Context(), c.Value)
	}
	s.setMemberCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) memberPassword(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Portal.ChangeOwnPassword(r.Context(), m, in.Current, in.New); err != nil {
		return err
	}
	// Changing it signs out everywhere; this browser stays in.
	return s.memberSignedIn(w, r, m)
}

// memberLanguage keeps the language someone picked on the request site.
func (s *Server) memberLanguage(w http.ResponseWriter, r *http.Request, m store.Member) error {
	language, err := readLanguage(r)
	if err != nil {
		return err
	}
	if err := s.Store.SetMemberLanguage(r.Context(), m.ID, language); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"language": language})
}

func (s *Server) memberSearch(w http.ResponseWriter, r *http.Request, _ store.Member) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		return writeJSON(w, http.StatusOK, []catalog.DiscoverTitle{})
	}
	ts, err := s.Catalog.Search(r.Context(), q)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ts)
}

func (s *Server) memberRequests(w http.ResponseWriter, r *http.Request, m store.Member) error {
	rs, err := s.Store.MemberRequests(r.Context(), m.ID)
	if err != nil {
		return err
	}
	vs, err := s.Portal.Views(r.Context(), rs)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, vs)
}

func (s *Server) memberAsk(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in struct {
		Kind   string `json:"kind"`
		TMDBID int64  `json:"tmdbId"`
		Note   string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	v, err := s.Portal.Ask(r.Context(), m, in.Kind, in.TMDBID, in.Note)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, v)
}

func (s *Server) memberCancel(w http.ResponseWriter, r *http.Request, m store.Member) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Portal.Cancel(r.Context(), m, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) memberIssues(w http.ResponseWriter, r *http.Request, m store.Member) error {
	is, err := s.Store.MemberIssues(r.Context(), m.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, is)
}

func (s *Server) memberReport(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in store.Issue
	if err := readJSON(r, &in); err != nil {
		return err
	}
	i, err := s.Portal.Report(r.Context(), m, store.Issue{Kind: in.Kind, TMDBID: in.TMDBID, Season: in.Season, Episode: in.Episode, Problem: in.Problem, Message: in.Message})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, i)
}

// ---- admins ----

func (s *Server) portalSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Portal.Settings(r.Context())
	if err != nil {
		return err
	}
	logo, err := s.Portal.Logo(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"settings": st, "hasLogo": logo != ""})
}

func (s *Server) savePortalSettings(w http.ResponseWriter, r *http.Request) error {
	var st portal.Settings
	if err := readJSON(r, &st); err != nil {
		return err
	}
	for _, id := range []int{st.MovieProfile, st.ShowProfile} {
		if id != 0 {
			if _, err := s.Store.Profile(r.Context(), id); err != nil {
				return errorf(http.StatusBadRequest, "there's no such quality profile")
			}
		}
	}
	st, err := s.Portal.SaveSettings(r.Context(), st)
	if err != nil {
		return portalErr(err)
	}
	return s.portalSettings(w, r)
}

// savePortalLogo takes the picture as the request body.
func (s *Server) savePortalLogo(w http.ResponseWriter, r *http.Request) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, portal.MaxLogo+1))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errorf(http.StatusBadRequest, "pick a picture")
	}
	if err := s.Portal.SaveLogo(r.Context(), data); err != nil {
		return portalErr(err)
	}
	return s.portalSettings(w, r)
}

func (s *Server) deletePortalLogo(w http.ResponseWriter, r *http.Request) error {
	if err := s.Portal.SaveLogo(r.Context(), nil); err != nil {
		return err
	}
	return s.portalSettings(w, r)
}

// memberRow is a member as the admin UI lists them, with what they asked
// for.
type memberRow struct {
	store.Member
	Requests int `json:"requests"`
	Pending  int `json:"pending"`
}

func (s *Server) portalMembers(w http.ResponseWriter, r *http.Request) error {
	ms, err := s.Store.Members(r.Context())
	if err != nil {
		return err
	}
	rs, err := s.Store.Requests(r.Context(), "", 100000)
	if err != nil {
		return err
	}
	count, pending := map[int64]int{}, map[int64]int{}
	for _, q := range rs {
		count[q.MemberID]++
		if q.Status == store.RequestPending {
			pending[q.MemberID]++
		}
	}
	out := make([]memberRow, 0, len(ms))
	for _, m := range ms {
		out = append(out, memberRow{Member: m, Requests: count[m.ID], Pending: pending[m.ID]})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addPortalMember(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		credentials
		DisplayName string `json:"displayName"`
		AutoApprove string `json:"autoApprove"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	switch in.AutoApprove {
	case "", "on", "off":
	default:
		return errorf(http.StatusBadRequest, "approval is on, off, or follows Settings")
	}
	m, err := s.Portal.CreateLocal(r.Context(), store.Member{Username: in.Username, DisplayName: in.DisplayName, AutoApprove: in.AutoApprove, QuotaMovies: -1, QuotaShows: -1}, in.Password)
	if err != nil {
		return portalErr(err)
	}
	return writeJSON(w, http.StatusCreated, m)
}

func (s *Server) editPortalMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in portal.EditMember
	if err := readJSON(r, &in); err != nil {
		return err
	}
	m, err := s.Portal.Edit(r.Context(), id, in)
	if err != nil {
		return portalErr(err)
	}
	return writeJSON(w, http.StatusOK, m)
}

func (s *Server) deletePortalMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteMember(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) adminRequests(w http.ResponseWriter, r *http.Request) error {
	status := r.URL.Query().Get("status")
	switch status {
	case "", store.RequestPending, store.RequestApproved, store.RequestDeclined:
	default:
		return errorf(http.StatusBadRequest, "status is pending, approved or declined")
	}
	rs, err := s.Store.Requests(r.Context(), status, 500)
	if err != nil {
		return err
	}
	vs, err := s.Portal.Views(r.Context(), rs)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, vs)
}

func (s *Server) requestCounts(w http.ResponseWriter, r *http.Request) error {
	req, iss, err := s.Store.CountPending(r.Context())
	if err != nil {
		return err
	}
	st, err := s.Portal.Settings(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"requests": req, "issues": iss, "enabled": st.Enabled})
}

func (s *Server) approveRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in catalog.AddOptions
	if err := readJSON(r, &in); err != nil {
		return err
	}
	req, err := s.Portal.Approve(r.Context(), id, catalog.AddOptions{ProfileID: in.ProfileID, RootFolder: in.RootFolder, Monitor: in.Monitor, Recent: in.Recent})
	if err != nil {
		return portalErr(err)
	}
	return s.oneRequest(w, r, req)
}

func (s *Server) declineRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Reply string `json:"reply"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	req, err := s.Portal.Decline(r.Context(), id, in.Reply)
	if err != nil {
		return portalErr(err)
	}
	return s.oneRequest(w, r, req)
}

func (s *Server) oneRequest(w http.ResponseWriter, r *http.Request, req store.Request) error {
	vs, err := s.Portal.Views(r.Context(), []store.Request{req})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, vs[0])
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteRequest(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// issueRow is a problem report with the title's id on the list, so the
// admin UI can link to it.
type issueRow struct {
	store.Issue
	TitleID int64 `json:"titleId,omitempty"`
}

func (s *Server) adminIssues(w http.ResponseWriter, r *http.Request) error {
	status := r.URL.Query().Get("status")
	if status != "" && status != store.IssueOpen && status != store.IssueResolved {
		return errorf(http.StatusBadRequest, "status is open or resolved")
	}
	is, err := s.Store.Issues(r.Context(), status, 500)
	if err != nil {
		return err
	}
	ids, err := s.titleIDs(r.Context())
	if err != nil {
		return err
	}
	out := make([]issueRow, 0, len(is))
	for _, i := range is {
		out = append(out, issueRow{Issue: i, TitleID: ids[i.Kind+":"+strconv.FormatInt(i.TMDBID, 10)]})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) titleIDs(ctx context.Context) (map[string]int64, error) {
	list, err := s.Store.OwnedTitles(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(list))
	for _, o := range list {
		m[o.Kind+":"+strconv.FormatInt(o.TMDBID, 10)] = o.ID
	}
	return m, nil
}

func (s *Server) editIssue(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Resolved bool   `json:"resolved"`
		Reply    string `json:"reply"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if len([]rune(in.Reply)) > 1000 {
		return errorf(http.StatusBadRequest, "keep the reply to 1,000 characters")
	}
	if err := s.Store.ResolveIssue(r.Context(), id, in.Resolved, strings.TrimSpace(in.Reply)); err != nil {
		return err
	}
	i, err := s.Store.Issue(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, i)
}

// ---- its own host ----

// portalHost says whether a request came in on the request site's own host
// name, where the request site is the whole site.
func (s *Server) portalHost(r *http.Request) bool {
	if s.Portal == nil {
		return false
	}
	st, err := s.Portal.Settings(r.Context())
	if err != nil || !st.Enabled || st.Host == "" {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.EqualFold(host, st.Host)
}

// decodeDataURL reads a data: URL of base64 data.
func decodeDataURL(u string) (mime string, data []byte, ok bool) {
	rest, found := strings.CutPrefix(u, "data:")
	if !found {
		return "", nil, false
	}
	mime, b64, found := strings.Cut(rest, ";base64,")
	if !found {
		return "", nil, false
	}
	data, err := decodeBase64(b64)
	return mime, data, err == nil
}

func decodeBase64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
