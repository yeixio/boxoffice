// Package games is Box Office's Games section: the game collection already
// on disk, found in the games root folders, one folder per system. MAME
// sets are checked against MAME's own list, and console games matched by
// hash against No-Intro's, with facts and box art from libretro's
// database. None of these sources needs an account.
//
// It only reads the game files already there. Nothing here moves, renames,
// rebuilds or deletes one. Games are only ever added: brought in on the
// Import files page, or wanted (picked from a system's list), downloaded
// and checked to be the game wanted before they're copied in.
package games

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// ErrOff means Games isn't turned on in Settings.
var ErrOff = errors.New("turn on Games in Settings first")

// ErrBusy means a check is running already.
var ErrBusy = errors.New("Box Office is checking your games already")

// Settings.
const (
	// SettingMAMEVersion is the MAME version the sets are checked against,
	// such as 0.289; empty follows the newest.
	SettingMAMEVersion = "games_mame_version"
	settingNewest      = "games_mame_newest"
	settingNewestAt    = "games_mame_newest_at"
)

// Where the lists and art come from, unless set otherwise.
const (
	DefaultMAMEReleases = "https://github.com/mamedev/mame/releases"
	DefaultLibretroDB   = "https://raw.githubusercontent.com/libretro/libretro-database/master"
)

// DefaultArt are where box art is looked for, in turn: libretro's
// thumbnail server, then the same thumbnails on GitHub. {system} is
// libretro's name for the system, {repo} that with _ for spaces, {kind}
// Named_Boxarts, Named_Titles or Named_Snaps, and {name} the game.
var DefaultArt = []string{
	"https://thumbnails.libretro.com/{system}/{kind}/{name}.png",
	"https://raw.githubusercontent.com/libretro-thumbnails/{repo}/master/{kind}/{name}.png",
}

// Service is the Games section.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// Dir keeps the lists and art downloaded: data/games.
	Dir string
	// MAMEReleases, LibretroDB and Art override where lists and art come
	// from, such as for a mirror; empty uses the defaults.
	MAMEReleases string
	LibretroDB   string
	Art          []string
	HTTP         *http.Client
	UserAgent    string
	// Search queues a search for a game just wanted, or asked for.
	Search Searcher

	mu       sync.Mutex
	progress Progress
	queued   map[string]bool // checks asked for while one ran

	catMu    sync.Mutex
	catalogs map[string]*catalog
	listMu   sync.Mutex
	artSem   chan struct{}
	artOnce  sync.Once
}

// on checks Games is turned on.
func (s *Service) on(ctx context.Context) error {
	on, err := mediatypes.Enabled(ctx, s.Store, mediatypes.Game)
	if err != nil {
		return err
	}
	if !on {
		return ErrOff
	}
	return nil
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// get fetches a URL; a 404 is os.ErrNotExist.
func (s *Service) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", cmp.Or(s.UserAgent, "BoxOffice"))
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s", hostOf(u), resp.Status)
	}
	return resp, nil
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

// download saves a URL to a file, whole or not at all.
func (s *Service) download(ctx context.Context, u, dst string) error {
	resp, err := s.get(ctx, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading from %s: %w", hostOf(u), err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// MAME versions.

var mameTag = regexp.MustCompile(`mame0?(\d{3,4})`)

// tagVersion turns mame0289 into 0.289.
func tagVersion(tag string) string {
	m := mameTag.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	n, _ := strconv.Atoi(m[1])
	return fmt.Sprintf("0.%d", n)
}

// fileVersion turns 0.289 into 0289, as MAME's files are named.
func fileVersion(v string) string {
	_, minor, ok := strings.Cut(v, ".")
	if !ok {
		return ""
	}
	n, err := strconv.Atoi(minor)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%04d", n)
}

// ValidVersion says whether v is a MAME version Box Office can fetch the
// list for: 0.162 or later, which MAME publishes on GitHub.
func ValidVersion(v string) bool {
	_, minor, ok := strings.Cut(v, ".")
	n, err := strconv.Atoi(minor)
	return ok && strings.HasPrefix(v, "0.") && err == nil && n >= 162 && n < 10000
}

// Newest finds MAME's newest version, asking GitHub at most once a day.
func (s *Service) Newest(ctx context.Context) (string, error) {
	cached, _ := s.Store.Setting(ctx, settingNewest)
	at, _ := s.Store.Setting(ctx, settingNewestAt)
	if t, err := strconv.ParseInt(at, 10, 64); err == nil && cached != "" && time.Since(time.Unix(t, 0)) < 24*time.Hour {
		return cached, nil
	}
	v, err := s.askNewest(ctx)
	if err != nil {
		if cached != "" {
			return cached, nil
		}
		// The newest list already downloaded will do.
		if have := s.downloadedVersions(); len(have) > 0 {
			return have[0], nil
		}
		return "", fmt.Errorf("Box Office couldn't find out MAME's newest version: %w", err)
	}
	s.Store.SetSetting(ctx, settingNewest, v)
	s.Store.SetSetting(ctx, settingNewestAt, strconv.FormatInt(time.Now().Unix(), 10))
	return v, nil
}

// askNewest follows GitHub's "latest release" link, which points at the
// newest release's tag, mame0289.
func (s *Service) askNewest(ctx context.Context) (string, error) {
	base := cmp.Or(s.MAMEReleases, DefaultMAMEReleases)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cmp.Or(s.UserAgent, "BoxOffice"))
	c := *s.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if v := tagVersion(resp.Header.Get("Location")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s didn't say which release is newest (%s)", hostOf(base), resp.Status)
}

func (s *Service) downloadedVersions() []string {
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "mame"))
	var out []string
	for _, e := range entries {
		if v := tagVersion(e.Name()); v != "" && strings.HasSuffix(e.Name(), "lx.zip") {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(fileVersion(b), fileVersion(a)) })
	return out
}

// MAMEVersion is the version sets are checked against: the one picked in
// Settings, or the newest.
func (s *Service) MAMEVersion(ctx context.Context) (string, error) {
	if v, err := s.Store.Setting(ctx, SettingMAMEVersion); err != nil {
		return "", err
	} else if v != "" {
		return v, nil
	}
	return s.Newest(ctx)
}

// mameList is MAME's list for a version, downloaded the first time:
// mame0289lx.zip, the -listxml MAME publishes with each release.
func (s *Service) mameList(ctx context.Context, version string) (string, error) {
	fv := fileVersion(version)
	if fv == "" {
		return "", fmt.Errorf("%q isn't a MAME version", version)
	}
	name := "mame" + fv + "lx.zip"
	p := filepath.Join(s.Dir, "mame", name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	s.listMu.Lock()
	defer s.listMu.Unlock()
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	u := cmp.Or(s.MAMEReleases, DefaultMAMEReleases) + "/download/mame" + fv + "/" + name
	if err := s.download(ctx, u, p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("MAME %s's list isn't on GitHub", version)
		}
		return "", fmt.Errorf("couldn't download MAME %s's list: %w", version, err)
	}
	// Only the version in use is kept: each is about 20 MB.
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if e.Name() != name && strings.HasSuffix(e.Name(), "lx.zip") {
			os.Remove(filepath.Join(filepath.Dir(p), e.Name()))
		}
	}
	return p, nil
}

// libretroDAT is one of libretro's DATs for a system, such as
// no-intro or genre, downloaded the first time and again after a month.
// A DAT libretro doesn't have for the system is "", with no error.
func (s *Service) libretroDAT(ctx context.Context, kind, system string) (string, error) {
	p := filepath.Join(s.Dir, "libretro", kind, system+".dat")
	missing := p + ".none"
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		return p, nil
	}
	if st, err := os.Stat(missing); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		return "", nil
	}
	s.listMu.Lock()
	defer s.listMu.Unlock()
	u := cmp.Or(s.LibretroDB, DefaultLibretroDB) + "/metadat/" + kind + "/" + url.PathEscape(system) + ".dat"
	err := s.download(ctx, u, p)
	switch {
	case err == nil:
		os.Remove(missing)
		return p, nil
	case errors.Is(err, os.ErrNotExist):
		os.MkdirAll(filepath.Dir(missing), 0o750)
		os.WriteFile(missing, nil, 0o640)
		return "", nil
	}
	// An older copy is better than none.
	if _, serr := os.Stat(p); serr == nil {
		s.log().Warn("couldn't update a game list; using the one downloaded before", "list", kind+"/"+system, "err", err)
		return p, nil
	}
	return "", err
}

// GameSettings are the Games section's settings.
type GameSettings struct {
	// MAMEVersion is the version picked, or "" for the newest.
	MAMEVersion string `json:"mameVersion"`
	// Newest is MAME's newest version, if Box Office could find out.
	Newest string `json:"newest,omitempty"`
	// Versions are the versions to pick from, newest first.
	Versions []string `json:"versions"`
}

// Settings gives the Games section's settings.
func (s *Service) Settings(ctx context.Context) (GameSettings, error) {
	v, err := s.Store.Setting(ctx, SettingMAMEVersion)
	if err != nil {
		return GameSettings{}, err
	}
	out := GameSettings{MAMEVersion: v, Versions: []string{}}
	out.Newest, _ = s.Newest(ctx)
	top := 0
	if fv := fileVersion(out.Newest); fv != "" {
		top, _ = strconv.Atoi(fv)
	}
	if fv := fileVersion(v); fv != "" {
		if n, _ := strconv.Atoi(fv); n > top {
			top = n
		}
	}
	for n := top; n >= 162; n-- {
		out.Versions = append(out.Versions, fmt.Sprintf("0.%d", n))
	}
	return out, nil
}

// SetMAMEVersion picks the MAME version sets are checked against; ""
// follows the newest. MAME's sets are checked again with it.
func (s *Service) SetMAMEVersion(ctx context.Context, v string) error {
	v = strings.TrimSpace(v)
	if v != "" && !ValidVersion(v) {
		return fmt.Errorf("%q isn't a MAME version Box Office can check against: pick 0.162 or later", v)
	}
	old, _ := s.Store.Setting(ctx, SettingMAMEVersion)
	if err := s.Store.SetSetting(ctx, SettingMAMEVersion, v); err != nil {
		return err
	}
	if old != v {
		if _, err := s.Start(ctx, 0, "mame"); err != nil && !errors.Is(err, ErrBusy) {
			return err
		}
	}
	return nil
}
