// Package playon brings in recordings made by PlayOn Cloud, the service
// that records shows and movies from streaming services (Netflix, Hulu,
// Disney+, Max and others) on its own computers. Recordings are queued in
// the PlayOn Cloud app; once one is finished, Box Office downloads it and
// files it in the library through Import files, the way an NZB added by
// hand is. PlayOn deletes recordings after a few days unless the account
// pays for storage, so they're fetched soon after they're made.
//
// PlayOn has no published API: this uses the one its own apps use (see
// client.go), so it may stop working if PlayOn changes it.
package playon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// settingConfig is where Settings → PlayOn Cloud is kept.
const settingConfig = "playon"

// Folder is where recordings are downloaded to, in the downloads folder,
// before they're filed.
const Folder = "playon"

// Every is how often PlayOn is asked for new recordings.
const Every = 15 * time.Minute

// MaxTries is how many times a recording is downloaded before Box Office
// gives up on it until someone presses Retry.
const MaxTries = 3

// firstWait gives the rest of Box Office time to start.
var firstWait = 2 * time.Minute

// Config is what admins set in Settings → PlayOn Cloud.
type Config struct {
	On       bool   `json:"on"`
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
}

// Normalize tidies what was typed.
func (c *Config) Normalize() { c.Email = strings.TrimSpace(c.Email) }

// Check says what's wrong with the settings, in words for the admin.
func (c Config) Check() error {
	if c.Email != "" && !strings.Contains(c.Email, "@") {
		return errors.New("enter the email address you sign in to PlayOn with")
	}
	if c.On && (c.Email == "" || c.Password == "") {
		return errors.New("enter your PlayOn email and password to turn this on")
	}
	return nil
}

// LoadConfig reads the settings; until they're saved, it's off.
func LoadConfig(ctx context.Context, st *store.Store) (Config, error) {
	var c Config
	v, err := st.Setting(ctx, settingConfig)
	if err != nil || v == "" {
		return c, err
	}
	if err := json.Unmarshal([]byte(v), &c); err != nil {
		return c, err
	}
	c.Normalize()
	return c, nil
}

// SaveConfig stores the settings.
func SaveConfig(ctx context.Context, st *store.Store, c Config) error {
	c.Normalize()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, settingConfig, string(b))
}

// Service checks PlayOn for new recordings and brings them in.
type Service struct {
	Store *store.Store
	// Dir is the downloads folder.
	Dir string
	// FileAway files a downloaded recording in the library through Import
	// files: it says where it went or, when it's left there to be checked,
	// why. kind is movie, series or auto.
	FileAway  func(ctx context.Context, src, name, kind string) (to, waiting string, err error)
	Log       *slog.Logger
	UserAgent string
	// Base is PlayOn's API address, for tests.
	Base string

	mu     sync.Mutex // held while checking
	smu    sync.Mutex // guards status
	status Status
	wake   chan struct{}
	once   sync.Once
	// cl is the client checks use, kept so its token is too.
	cl *Client
}

// Status is how bringing recordings in is going.
type Status struct {
	Running   bool      `json:"running"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	NextAt    time.Time `json:"nextAt,omitzero"`
	Error     string    `json:"error,omitempty"`
	// Paused says why downloading waits, such as the data cap.
	Paused string `json:"paused,omitempty"`
	// Recording is how many are queued or being recorded in PlayOn.
	Recording int `json:"recording"`
	// Current is the recording being downloaded.
	Current *Progress `json:"current,omitempty"`
}

// Progress is a download under way.
type Progress struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

func (s *Service) log() *slog.Logger { return cmp.Or(s.Log, slog.Default()) }

func (s *Service) waker() chan struct{} {
	s.once.Do(func() { s.wake = make(chan struct{}, 1) })
	return s.wake
}

// Wake checks PlayOn soon, as when the settings were saved.
func (s *Service) Wake() {
	select {
	case s.waker() <- struct{}{}:
	default:
	}
}

// State is how it's going.
func (s *Service) State() Status {
	s.smu.Lock()
	defer s.smu.Unlock()
	st := s.status
	if st.Current != nil {
		c := *st.Current
		st.Current = &c
	}
	return st
}

func (s *Service) set(f func(*Status)) {
	s.smu.Lock()
	f(&s.status)
	s.smu.Unlock()
}

// Client is a PlayOn client for the account in c.
func (s *Service) Client(c Config) *Client {
	return &Client{Email: c.Email, Password: c.Password, Base: s.Base, UserAgent: s.UserAgent}
}

// Run checks PlayOn every Every while it's turned on, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if err := s.Store.ResetPlayOnDownloading(ctx); err != nil && ctx.Err() == nil {
		s.log().Warn("playon: couldn't pick up downloads a restart cut off", "err", err)
	}
	t := time.NewTimer(firstWait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.waker():
		}
		s.Check(ctx)
		s.set(func(st *Status) { st.NextAt = time.Now().Add(Every) })
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(Every)
	}
}

// Check asks PlayOn for new recordings and downloads them, one at a time.
// It does nothing while PlayOn Cloud is off.
func (s *Service) Check(ctx context.Context) {
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	c, err := LoadConfig(ctx, s.Store)
	if err != nil {
		s.log().Warn("playon: couldn't read the settings", "err", err)
		return
	}
	if !c.On {
		return
	}
	s.set(func(st *Status) { st.Running, st.Error, st.Paused = true, "", "" })
	if s.cl == nil || s.cl.Email != c.Email || s.cl.Password != c.Password {
		s.cl = s.Client(c)
	}
	err = s.check(ctx, s.cl)
	s.set(func(st *Status) {
		st.Running, st.Current, st.CheckedAt = false, nil, time.Now()
		if err != nil {
			st.Error = err.Error()
		}
	})
	if err != nil && ctx.Err() == nil {
		s.log().Warn("playon: checking for recordings", "err", err)
	}
}

func (s *Service) check(ctx context.Context, cl *Client) error {
	list, err := cl.Library(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	ready := map[int64]bool{}
	for _, r := range list {
		if r.ID == 0 || r.Created == "" {
			continue // still being recorded
		}
		rec := toRecording(r)
		if err := s.Store.SeePlayOnRecording(ctx, rec); err != nil {
			return err
		}
		ready[rec.ID] = r.Ready(now)
	}
	if q, err := cl.Queue(ctx); err == nil {
		s.set(func(st *Status) { st.Recording = len(q) })
	}
	recs, err := s.Store.PlayOnRecordings(ctx)
	if err != nil {
		return err
	}
	// Oldest first, as they expire first.
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if ctx.Err() != nil {
			return nil
		}
		if r.State != store.PlayOnNew {
			continue
		}
		if !ready[r.ID] {
			if !r.Expires.IsZero() && r.Expires.Before(now) {
				s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnExpired, "PlayOn deleted it before it could be downloaded", "", 0, false)
			}
			continue
		}
		// Skipped while the others were downloading?
		if cur, err := s.Store.PlayOnRecording(ctx, r.ID); err != nil || cur.State != store.PlayOnNew {
			continue
		}
		capState, err := downloader.LoadCap(ctx, s.Store)
		if err == nil && capState.Level == downloader.CapFull {
			s.set(func(st *Status) { st.Paused = capState.Why() })
			break
		}
		s.bring(ctx, cl, r)
	}
	return s.Store.PrunePlayOn(ctx)
}

// bring downloads one recording and files it.
func (s *Service) bring(ctx context.Context, cl *Client, r store.PlayOnRecording) {
	name, kind := Name(r)
	if err := s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnDownloading, "", "", 0, false); err != nil {
		s.log().Warn("playon: couldn't note a download", "id", r.ID, "err", err)
		return
	}
	s.log().Info("playon: downloading a recording", "id", r.ID, "name", name)
	path, size, err := s.fetch(ctx, cl, r, name)
	if err != nil {
		state, note := store.PlayOnNew, "Downloading it failed, so it'll be tried again: "+err.Error()
		if r.Tries+1 >= MaxTries {
			state, note = store.PlayOnFailed, fmt.Sprintf("Downloading it failed %d times: %s", MaxTries, err)
		}
		if ctx.Err() != nil {
			state, note = store.PlayOnNew, ""
		}
		s.Store.SetPlayOnState(context.WithoutCancel(ctx), r.ID, state, note, "", 0, ctx.Err() == nil)
		if ctx.Err() == nil {
			s.log().Warn("playon: downloading a recording", "id", r.ID, "name", name, "err", err)
		}
		return
	}
	ctx = context.WithoutCancel(ctx)
	if s.FileAway == nil {
		s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnCheck, "Downloaded to "+path, "", size, true)
		return
	}
	to, waiting, err := s.FileAway(ctx, path, filepath.Base(path), kind)
	switch {
	case err != nil:
		s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnCheck, "Downloaded, but Box Office couldn't file it: "+err.Error(), "", size, true)
		s.log().Warn("playon: couldn't file a recording", "id", r.ID, "name", name, "err", err)
	case waiting != "":
		s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnCheck, "Downloaded, and waiting on Import files: "+waiting+".", "", size, true)
		s.log().Info("playon: a recording waits on Import files", "id", r.ID, "name", name, "why", waiting)
	default:
		s.Store.SetPlayOnState(ctx, r.ID, store.PlayOnFiled, "", to, size, true)
		s.log().Info("playon: filed a recording", "id", r.ID, "name", name, "to", to)
	}
}

// fetch downloads a recording into the PlayOn folder, named name, and
// says where it is. The bytes count toward the monthly data cap.
func (s *Service) fetch(ctx context.Context, cl *Client, r store.PlayOnRecording, name string) (string, int64, error) {
	dir := filepath.Join(s.Dir, Folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	body, total, remote, err := cl.Open(ctx, r.ID)
	if err != nil {
		return "", 0, err
	}
	defer body.Close()
	ext := strings.ToLower(filepath.Ext(remote))
	if ext == "" || len(ext) > 5 {
		ext = ".mp4"
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".%d.part", r.ID))
	f, err := os.Create(tmp)
	if err != nil {
		return "", 0, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	s.set(func(st *Status) { st.Current = &Progress{ID: r.ID, Name: name, Total: total} })
	w := &counter{s: s, ctx: ctx}
	n, err := io.Copy(f, io.TeeReader(body, w))
	w.flush()
	if err != nil {
		return "", n, err
	}
	if total > 0 && n != total {
		return "", n, fmt.Errorf("the download stopped at %d of %d bytes", n, total)
	}
	if err := f.Close(); err != nil {
		return "", n, err
	}
	ok = true
	dest := unique(filepath.Join(dir, name+ext))
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", n, err
	}
	return dest, n, nil
}

// counter keeps the progress and counts the bytes toward the data cap,
// saving them every so often.
type counter struct {
	s       *Service
	ctx     context.Context
	pending int64
}

func (c *counter) Write(p []byte) (int, error) {
	n := int64(len(p))
	c.pending += n
	c.s.set(func(st *Status) {
		if st.Current != nil {
			st.Current.Done += n
		}
	})
	if c.pending >= 64<<20 {
		c.flush()
	}
	return len(p), nil
}

func (c *counter) flush() {
	if c.pending == 0 {
		return
	}
	if err := c.s.Store.AddDataUsage(context.WithoutCancel(c.ctx), store.PlayOnUsage, c.pending); err != nil {
		c.s.log().Warn("playon: couldn't count data used", "err", err)
	}
	c.pending = 0
}

// unique is p, or p with a number added when something's there already.
func unique(p string) string {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		q := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Lstat(q); errors.Is(err, os.ErrNotExist) {
			return q
		}
	}
}

// toRecording is what's kept of a recording PlayOn lists. A show's season
// and episode are filled in from its name or where it was found on the
// service when PlayOn doesn't give them.
func toRecording(r Recording) store.PlayOnRecording {
	rec := store.PlayOnRecording{
		ID: int64(r.ID), Name: strings.TrimSpace(r.Name), Series: strings.TrimSpace(r.Series),
		Season: int(r.Season), Episode: int(r.Episode), Year: int(r.Year),
		Provider: r.Provider, Quality: int(r.Quality), Expires: r.ExpiresAt(),
	}
	if strings.EqualFold(rec.Series, "none") {
		rec.Series = ""
	}
	if m := reBrowse.FindStringSubmatch(r.BrowsePath); m != nil {
		if rec.Series == "" {
			rec.Series = strings.TrimSpace(m[1])
		}
		if rec.Season == 0 {
			fmt.Sscan(m[2], &rec.Season)
		}
	}
	if rec.Series != "" {
		if m := reCode.FindStringSubmatch(episodeTitle(rec)); m != nil {
			var se, ep int
			fmt.Sscan(m[1], &se)
			fmt.Sscan(m[2], &ep)
			if rec.Season == 0 {
				rec.Season = se
			}
			if rec.Episode == 0 {
				rec.Episode = ep
			}
		}
	}
	return rec
}

var (
	// reBrowse finds the show and season at the end of where a recording
	// was found on its service: "Hulu | TV | The Flash | Season 9".
	reBrowse = regexp.MustCompile(`(?i)\|\s*([^|]*[^|\s])\s*\|\s*season\s+(\d+)\s*$`)
	// reCode is a season and episode at the start of a name: "s09e03 - ".
	reCode = regexp.MustCompile(`(?i)^s(\d{1,3})\s*e(\d{1,4})\b[\s\-:.]*`)
	// reBad is what can't go in a file name.
	reBad = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)
)

// episodeTitle is a show's recording's name without the show's name or a
// season and episode in front.
func episodeTitle(r store.PlayOnRecording) string {
	t := r.Name
	if r.Series != "" {
		for _, sep := range []string{" - ", ": "} {
			if p := r.Series + sep; len(t) > len(p) && strings.EqualFold(t[:len(p)], p) {
				t = t[len(p):]
				break
			}
		}
	}
	return strings.TrimSpace(t)
}

// Name is what a recording's file is called, so Import files can tell what
// it is ("The Flash - S09E03 - Rogue Air", "Arrival (2016)"), and the kind
// it's filed as: series, movie, or auto when that isn't clear.
func Name(r store.PlayOnRecording) (name, kind string) {
	clean := func(s string) string {
		s = reBad.ReplaceAllString(s, " ")
		return strings.Join(strings.Fields(s), " ")
	}
	switch {
	case r.Series != "" && r.Season > 0 && r.Episode > 0:
		t := reCode.ReplaceAllString(episodeTitle(r), "")
		name = fmt.Sprintf("%s - S%02dE%02d", clean(r.Series), r.Season, r.Episode)
		if t = clean(t); t != "" {
			name += " - " + t
		}
		return name, "series"
	case r.Series != "":
		return clean(r.Series + " - " + episodeTitle(r)), "auto"
	case r.Year > 0:
		return fmt.Sprintf("%s (%d)", clean(r.Name), r.Year), "movie"
	}
	return cmp.Or(clean(r.Name), fmt.Sprintf("PlayOn recording %d", r.ID)), "auto"
}
