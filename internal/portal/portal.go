// Package portal is the request site: a friendly, brandable site where the
// people who watch (not the people who run the server) ask for movies and
// shows, follow what they asked for, and report problems with what's in
// the library. Admins approve or decline requests and answer reports in
// the admin UI.
//
// The people who use it are members (store.Member), not admins: they sign
// in with a local account or with their Matinee account, and only ever see
// the request site.
package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
)

// Setting keys.
const (
	SettingSite = "portal"      // Settings, as JSON
	SettingLogo = "portal_logo" // the logo, as a data: URL
)

// Settings are how the request site looks and works.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Name is what the site calls itself, such as "Ellen's Movie Night".
	Name    string `json:"name"`
	Welcome string `json:"welcome"`
	// Accent is the site's main colour, as #rrggbb; "" uses Box Office's.
	Accent string `json:"accent"`
	// Host is a host name of its own, such as requests.example.com: there
	// the request site is the whole site. "" serves it at /request only.
	Host string `json:"host"`
	// LocalSignIn lets people sign in with accounts made here, and SignUp
	// lets them make their own.
	LocalSignIn bool `json:"localSignIn"`
	SignUp      bool `json:"signUp"`
	// MatineeURL, when set, lets people sign in with their Matinee account:
	// it's where Box Office checks their password.
	MatineeURL string `json:"matineeUrl"`
	// Approval is manual (an admin approves each request) or auto.
	Approval string `json:"approval"`
	// QuotaMovies and QuotaShows are how many each person may ask for a
	// week; 0 is no limit. A person's own limits win.
	QuotaMovies int `json:"quotaMovies"`
	QuotaShows  int `json:"quotaShows"`
	// MovieProfile and ShowProfile are the quality profiles approved
	// titles get; 0 uses the first one that isn't Do not touch.
	MovieProfile int `json:"movieProfileId"`
	ShowProfile  int `json:"showProfileId"`
}

// Defaults are the settings before an admin changes anything: off, local
// accounts only, every request approved by hand, no limits.
func Defaults() Settings {
	return Settings{Name: "Requests", LocalSignIn: true, Approval: ApprovalManual}
}

const (
	ApprovalManual = "manual"
	ApprovalAuto   = "auto"
)

var (
	hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	hostName  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
)

// Error is a problem with what someone asked for, in words to show them.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// ErrOff means the request site is turned off.
var ErrOff = errors.New("the request site is turned off")

// Check tidies settings and says what's wrong with them.
func (st *Settings) Check() error {
	st.Name = strings.TrimSpace(st.Name)
	st.Welcome = strings.TrimSpace(st.Welcome)
	st.Accent = strings.TrimSpace(st.Accent)
	st.Host = strings.ToLower(strings.TrimSpace(st.Host))
	st.MatineeURL = strings.TrimRight(strings.TrimSpace(st.MatineeURL), "/")
	switch {
	case st.Name == "":
		return badf("give the request site a name")
	case utf8.RuneCountInString(st.Name) > 60:
		return badf("keep the name to 60 characters")
	case utf8.RuneCountInString(st.Welcome) > 1000:
		return badf("keep the welcome text to 1,000 characters")
	case st.Accent != "" && !hexColour.MatchString(st.Accent):
		return badf("pick the colour as #rrggbb")
	case st.Host != "" && !hostName.MatchString(st.Host):
		return badf("enter just a host name, such as requests.example.com")
	case st.MatineeURL != "" && !strings.HasPrefix(st.MatineeURL, "http://") && !strings.HasPrefix(st.MatineeURL, "https://"):
		return badf("enter Matinee's address starting with http:// or https://")
	case st.QuotaMovies < 0 || st.QuotaShows < 0:
		return badf("a weekly limit can't be below 0")
	case st.Enabled && !st.LocalSignIn && st.MatineeURL == "":
		return badf("turn on at least one way to sign in")
	}
	if st.Approval != ApprovalAuto {
		st.Approval = ApprovalManual
	}
	if !st.LocalSignIn {
		st.SignUp = false
	}
	return nil
}

// Service is the request site's logic.
type Service struct {
	Store   *store.Store
	Catalog *catalog.Service
	// Notify, when set, tells admins about new requests and reports.
	Notify interface {
		Add(context.Context, store.Notification) error
	}
	Log  *slog.Logger
	HTTP *http.Client
}

// Settings reads the settings, with the defaults for what isn't saved.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Defaults()
	v, err := s.Store.Setting(ctx, SettingSite)
	if err != nil || v == "" {
		return st, err
	}
	err = json.Unmarshal([]byte(v), &st)
	return st, err
}

// SaveSettings checks and saves the settings.
func (s *Service) SaveSettings(ctx context.Context, st Settings) (Settings, error) {
	if err := st.Check(); err != nil {
		return st, err
	}
	b, _ := json.Marshal(st)
	return st, s.Store.SetSetting(ctx, SettingSite, string(b))
}

// Logo is the site's logo as a data: URL, or "".
func (s *Service) Logo(ctx context.Context) (string, error) {
	return s.Store.Setting(ctx, SettingLogo)
}

// Logos may be PNG, JPEG or WebP (not SVG, which can carry scripts), up
// to MaxLogo bytes.
const MaxLogo = 512 << 10

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// SaveLogo keeps a new logo; empty data removes it.
func (s *Service) SaveLogo(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return s.Store.SetSetting(ctx, SettingLogo, "")
	}
	if len(data) > MaxLogo {
		return badf("use a logo of at most %d KB", MaxLogo>>10)
	}
	mime := http.DetectContentType(data)
	if !logoTypes[mime] {
		return badf("use a PNG, JPEG or WebP picture for the logo")
	}
	return s.Store.SetSetting(ctx, SettingLogo, "data:"+mime+";base64,"+encode(data))
}

// ---- requests ----

// Title is what a request or report is about, as the request site shows
// it, with where it stands here.
type Title struct {
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"`
}

// View is a request with where its title stands now, for the person who
// asked and for admins.
type View struct {
	store.Request
	// State is what's happening, in the order a request goes through:
	// pending, declined, searching, downloading, partial, available, or
	// paused (approved, but no longer looked for), removed (approved, then
	// taken off the list).
	State string `json:"state"`
	// Progress is how much of the download is done, from 0 to 1, while
	// State is downloading.
	Progress float64 `json:"progress,omitempty"`
	// TitleID is its id on the list, once it's there.
	TitleID int64 `json:"titleId,omitempty"`
}

// The states a request can be in; see View.
const (
	StatePending     = "pending"
	StateDeclined    = "declined"
	StateSearching   = "searching"
	StateDownloading = "downloading"
	StatePartial     = "partial"
	StateAvailable   = "available"
	StatePaused      = "paused"
	StateRemoved     = "removed"
)

// owned is every title on the list, by kind and TMDB id.
func (s *Service) owned(ctx context.Context) (map[string]store.Owned, error) {
	list, err := s.Store.OwnedTitles(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]store.Owned, len(list))
	for _, o := range list {
		m[key(o.Kind, o.TMDBID)] = o
	}
	return m, nil
}

func key(kind string, tmdbID int64) string { return fmt.Sprintf("%s:%d", kind, tmdbID) }

// Views adds where each request's title stands.
func (s *Service) Views(ctx context.Context, rs []store.Request) ([]View, error) {
	own, err := s.owned(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rs))
	for _, r := range rs {
		v := View{Request: r}
		o, ok := own[key(r.Kind, r.TMDBID)]
		switch {
		case r.Status == store.RequestPending:
			v.State = StatePending
		case r.Status == store.RequestDeclined:
			v.State = StateDeclined
		case !ok:
			v.State = StateRemoved
		default:
			v.TitleID = o.ID
			switch catalog.Status(o) {
			case catalog.StatusAvailable:
				v.State = StateAvailable
			case catalog.StatusPartial:
				v.State = StatePartial
			case catalog.StatusPaused:
				v.State = StatePaused
			default:
				v.State = StateSearching
			}
			if v.State == StateSearching || v.State == StatePartial {
				if p, ok := s.progress(ctx, r.Kind, o.ID); ok {
					v.State, v.Progress = StateDownloading, p
				}
			}
		}
		if ok && v.TitleID == 0 {
			v.TitleID = o.ID
		}
		out = append(out, v)
	}
	return out, nil
}

// progress is how far a title's downloads have got, while there are any.
func (s *Service) progress(ctx context.Context, kind string, titleID int64) (float64, bool) {
	ds, err := s.Store.Pending(ctx, kind, titleID)
	if err != nil || len(ds) == 0 {
		return 0, false
	}
	var done, size int64
	for _, d := range ds {
		done += d.DoneBytes
		size += d.Size
	}
	if size <= 0 {
		return 0, true
	}
	return min(float64(done)/float64(size), 1), true
}

// lookup finds a title on TMDB, so a request carries its name and poster.
func (s *Service) lookup(ctx context.Context, kind string, tmdbID int64) (Title, error) {
	t := Title{Kind: kind, TMDBID: tmdbID}
	switch kind {
	case "movie":
		m, err := s.Catalog.TMDB(ctx).Movie(ctx, tmdbID)
		if err != nil {
			return t, err
		}
		t.Title, t.Year, t.Poster = m.Title, m.Year, m.Poster
	case "series":
		m, err := s.Catalog.TMDB(ctx).Show(ctx, tmdbID)
		if err != nil {
			return t, err
		}
		t.Title, t.Year, t.Poster = m.Title, m.Year, m.Poster
	default:
		return t, badf("ask for a movie or a show")
	}
	return t, nil
}

// Ask is someone asking for a title. It's approved straight away when
// approval is automatic for them (and the title is added and looked for),
// or when the title is on the list already; otherwise it waits for an
// admin.
func (s *Service) Ask(ctx context.Context, m store.Member, kind string, tmdbID int64, note string) (View, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return View{}, err
	}
	if !st.Enabled {
		return View{}, ErrOff
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return View{}, badf("keep the note to 500 characters")
	}
	if kind != "movie" && kind != "series" {
		return View{}, badf("ask for a movie or a show")
	}
	mine, err := s.Store.TitleRequests(ctx, kind, tmdbID)
	if err != nil {
		return View{}, err
	}
	for _, r := range mine {
		if r.MemberID == m.ID && r.Status != store.RequestDeclined {
			return View{}, badf("you've asked for this already")
		}
	}
	own, err := s.owned(ctx)
	if err != nil {
		return View{}, err
	}
	o, onList := own[key(kind, tmdbID)]
	if onList && catalog.Status(o) == catalog.StatusAvailable {
		return View{}, badf("it's in the library already")
	}
	if !onList {
		if err := s.checkQuota(ctx, st, m, kind); err != nil {
			return View{}, err
		}
	}
	t, err := s.lookup(ctx, kind, tmdbID)
	if err != nil {
		return View{}, err
	}
	r := store.Request{MemberID: m.ID, Kind: kind, TMDBID: tmdbID, Title: t.Title, Year: t.Year, Poster: t.Poster, Note: note}
	auto := m.AutoApprove == "on" || (m.AutoApprove == "" && st.Approval == ApprovalAuto)
	switch {
	case onList:
		// On its way already: nothing for an admin to decide.
		r.Status = store.RequestApproved
	case auto:
		if err := s.add(ctx, st, kind, tmdbID, catalog.AddOptions{}); err != nil {
			// Something an admin has to sort out, such as a root folder.
			s.Log.Warn("couldn't add a request on its own; it waits for an admin", "title", t.Title, "err", err)
		} else {
			r.Status = store.RequestApproved
		}
	}
	saved, err := s.Store.AddRequest(ctx, r)
	if err != nil {
		return View{}, err
	}
	if saved.Status == store.RequestPending {
		s.tell(ctx, "request.created", m.Name()+" asked for "+named(t), note, t)
	}
	vs, err := s.Views(ctx, []store.Request{saved})
	if err != nil {
		return View{}, err
	}
	return vs[0], nil
}

func named(t Title) string {
	if t.Year > 0 {
		return fmt.Sprintf("%s (%d)", t.Title, t.Year)
	}
	return t.Title
}

// checkQuota says no when someone has asked for as many titles of a kind
// this week as they may.
func (s *Service) checkQuota(ctx context.Context, st Settings, m store.Member, kind string) error {
	limit, what := st.QuotaMovies, "movies"
	mine := m.QuotaMovies
	if kind == "series" {
		limit, what, mine = st.QuotaShows, "shows", m.QuotaShows
	}
	if mine >= 0 {
		limit = mine
	}
	if limit == 0 {
		return nil
	}
	n, err := s.Store.CountRequests(ctx, m.ID, kind, time.Now().AddDate(0, 0, -7))
	if err != nil {
		return err
	}
	if n >= limit {
		return badf("you can ask for %d %s a week, and you've reached that; try again in a few days", limit, what)
	}
	return nil
}

// add puts a title on the list and starts looking for it, with the request
// site's profile unless the admin picked one.
func (s *Service) add(ctx context.Context, st Settings, kind string, tmdbID int64, o catalog.AddOptions) error {
	if o.ProfileID == 0 {
		o.ProfileID = st.MovieProfile
		if kind == "series" {
			o.ProfileID = st.ShowProfile
		}
		if o.ProfileID == 0 {
			ps, err := s.Store.Profiles(ctx)
			if err != nil {
				return err
			}
			for _, p := range ps {
				if !p.Locked {
					o.ProfileID = p.ID
					break
				}
			}
		}
	}
	o.Monitored, o.Search = true, true
	var err error
	if kind == "movie" {
		_, err = s.Catalog.AddMovie(ctx, tmdbID, o)
	} else {
		_, err = s.Catalog.AddSeries(ctx, tmdbID, o)
	}
	if errors.Is(err, catalog.ErrExists) {
		return nil
	}
	return err
}

// Approve adds a pending request's title, with the profile and folder the
// admin picked (or the request site's), and marks it approved. Other
// pending requests for the same title are approved with it.
func (s *Service) Approve(ctx context.Context, id int64, o catalog.AddOptions) (store.Request, error) {
	r, err := s.Store.Request(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status != store.RequestPending {
		return r, badf("that request was %s already", r.Status)
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return r, err
	}
	if err := s.add(ctx, st, r.Kind, r.TMDBID, o); err != nil {
		return r, err
	}
	same, err := s.Store.TitleRequests(ctx, r.Kind, r.TMDBID)
	if err != nil {
		return r, err
	}
	for _, o := range same {
		if o.Status == store.RequestPending {
			if err := s.Store.DecideRequest(ctx, o.ID, store.RequestApproved, ""); err != nil && !errors.Is(err, store.ErrInUse) {
				return r, err
			}
		}
	}
	return s.Store.Request(ctx, id)
}

// Decline turns a pending request down, with a reason for the person who
// asked.
func (s *Service) Decline(ctx context.Context, id int64, reply string) (store.Request, error) {
	reply = strings.TrimSpace(reply)
	if utf8.RuneCountInString(reply) > 500 {
		return store.Request{}, badf("keep the reason to 500 characters")
	}
	if err := s.Store.DecideRequest(ctx, id, store.RequestDeclined, reply); errors.Is(err, store.ErrInUse) {
		return store.Request{}, badf("that request was decided already")
	} else if err != nil {
		return store.Request{}, err
	}
	return s.Store.Request(ctx, id)
}

// Cancel is someone taking back a request an admin hasn't decided yet.
func (s *Service) Cancel(ctx context.Context, m store.Member, id int64) error {
	r, err := s.Store.Request(ctx, id)
	if err != nil || r.MemberID != m.ID {
		return store.ErrNotFound
	}
	if r.Status != store.RequestPending {
		return badf("an admin has decided on it already")
	}
	return s.Store.DeleteRequest(ctx, id)
}

// ---- problem reports ----

// Report is someone telling admins about a problem with a title in the
// library.
func (s *Service) Report(ctx context.Context, m store.Member, in store.Issue) (store.Issue, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return in, err
	}
	if !st.Enabled {
		return in, ErrOff
	}
	if !contains(store.Problems, in.Problem) {
		return in, badf("pick what's wrong")
	}
	in.Message = strings.TrimSpace(in.Message)
	if utf8.RuneCountInString(in.Message) > 1000 {
		return in, badf("keep the message to 1,000 characters")
	}
	if in.Problem == "other" && in.Message == "" {
		return in, badf("say what's wrong")
	}
	if in.Season < 0 || in.Episode < 0 || (in.Kind == "movie" && (in.Season > 0 || in.Episode > 0)) {
		return in, badf("that season or episode isn't right")
	}
	own, err := s.owned(ctx)
	if err != nil {
		return in, err
	}
	o, ok := own[key(in.Kind, in.TMDBID)]
	if !ok || o.Files == 0 {
		return in, badf("you can report a problem with something in the library")
	}
	mine, err := s.Store.MemberIssues(ctx, m.ID)
	if err != nil {
		return in, err
	}
	open := 0
	for _, i := range mine {
		if i.Status == store.IssueOpen {
			open++
		}
	}
	if open >= 20 {
		return in, badf("you have 20 reports waiting already; give the admins a moment")
	}
	t, err := s.lookup(ctx, in.Kind, in.TMDBID)
	if err != nil {
		// TMDB being down shouldn't stop a report: the list knows the name.
		t = Title{Kind: in.Kind, TMDBID: in.TMDBID, Title: o.Title}
	}
	in.MemberID, in.Title, in.Year, in.Poster = m.ID, t.Title, t.Year, t.Poster
	saved, err := s.Store.AddIssue(ctx, in)
	if err != nil {
		return saved, err
	}
	what := named(t)
	if saved.Season > 0 {
		what += fmt.Sprintf(", season %d", saved.Season)
		if saved.Episode > 0 {
			what += fmt.Sprintf(" episode %d", saved.Episode)
		}
	}
	s.tell(ctx, "issue.created", m.Name()+" reported a problem with "+what, ProblemName(saved.Problem)+sentence(saved.Message), t)
	return saved, nil
}

// ProblemName is how a problem reads.
func ProblemName(p string) string {
	switch p {
	case "wont_play":
		return "It won't play."
	case "wrong_title":
		return "It's the wrong movie or episode."
	case "picture":
		return "The picture is bad."
	case "sound":
		return "The sound is wrong."
	case "subtitles":
		return "Subtitles are missing or wrong."
	}
	return "Something else."
}

func sentence(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// tell lets admins know, through the notifications Matinee reads.
func (s *Service) tell(ctx context.Context, event, title, body string, t Title) {
	if s.Notify == nil {
		return
	}
	s.Notify.Add(ctx, store.Notification{Event: event, Level: store.LevelInfo, Audience: store.AudienceAdmins, Title: title, Body: body,
		Media: &store.Media{Kind: t.Kind, Title: t.Title, Year: t.Year, TMDBID: t.TMDBID, Poster: t.Poster}})
}
