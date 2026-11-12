package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/health"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/migrate"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// ---- system and settings ----

func (s *Server) system(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	movies, err := s.Store.Movies(ctx)
	if err != nil {
		return err
	}
	shows, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "database": s.Database, "dataDir": s.DataDir, "go": runtime.Version(),
		"os": runtime.GOOS + "/" + runtime.GOARCH, "startedAt": s.started.UTC(), "movies": len(movies), "series": len(shows),
	})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	key, _ := s.Store.Setting(ctx, catalog.SettingTMDBKey)
	types, err := s.mediaTypes(ctx)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"apiKey": s.APIKey(ctx), "apiKeyFromEnv": s.EnvAPIKey != "",
		"hasTmdbKey": key != "" || s.Catalog.EnvTMDBKey != "", "tmdbKeyFromEnv": key == "" && s.Catalog.EnvTMDBKey != "",
		"tmdbKeyBuiltIn": key == "" && s.Catalog.EnvTMDBKey != "" && s.Catalog.TMDBKeyBuiltIn,
		"mediaTypes":     types,
	})
}

// mediaTypeView is a media type as Settings shows it: whether it's on and,
// when it is but has no root folder yet, the folders Box Office can see
// where its library is usually mounted.
type mediaTypeView struct {
	mediatypes.State
	Suggested []string `json:"suggested"`
}

func (s *Server) mediaTypes(ctx context.Context) ([]mediaTypeView, error) {
	states, err := mediatypes.States(ctx, s.Store)
	if err != nil {
		return nil, err
	}
	out := make([]mediaTypeView, 0, len(states))
	for _, t := range states {
		v := mediaTypeView{State: t, Suggested: []string{}}
		if t.Enabled {
			roots, err := s.Store.RootFolders(ctx, t.Key)
			if err != nil {
				return nil, err
			}
			if len(roots) == 0 {
				for _, f := range t.Folders {
					if st, err := os.Stat(f); err == nil && st.IsDir() {
						v.Suggested = append(v.Suggested, f)
					}
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TMDBKey    *string         `json:"tmdbKey"`
		MediaTypes map[string]bool `json:"mediaTypes"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	for k := range in.MediaTypes {
		if _, ok := mediatypes.Lookup(k); !ok {
			return errorf(http.StatusBadRequest, "there's no media type "+k)
		}
	}
	for k, on := range in.MediaTypes {
		if err := mediatypes.SetEnabled(ctx, s.Store, k, on); err != nil {
			return err
		}
	}
	if in.TMDBKey != nil {
		k := strings.TrimSpace(*in.TMDBKey)
		if k != "" {
			c := s.Catalog.TMDB(ctx)
			c.Key = k
			if err := c.Check(ctx); err != nil {
				return errorf(http.StatusBadRequest, err.Error())
			}
		}
		if err := s.Store.SetSetting(ctx, catalog.SettingTMDBKey, k); err != nil {
			return err
		}
	}
	return s.settings(w, r)
}

func (s *Server) newAPIKey(w http.ResponseWriter, r *http.Request) error {
	if s.EnvAPIKey != "" {
		return errorf(http.StatusConflict, "the API key is set on the server (BOXOFFICE_API_KEY), so change it there")
	}
	if err := s.Store.SetSetting(r.Context(), SettingAPIKey, store.NewToken()); err != nil {
		return err
	}
	return s.settings(w, r)
}

func (s *Server) qualities(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, quality.All[1:])
}

func (s *Server) profiles(w http.ResponseWriter, r *http.Request) error {
	ps, err := s.Store.Profiles(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ps)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) error {
	var p quality.Profile
	if err := readJSON(r, &p); err != nil {
		return err
	}
	p.Locked = false // there's one Do not touch profile, and it's built in
	if err := p.Check(); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	p, err := s.Store.CreateProfile(r.Context(), p)
	if errors.Is(err, store.ErrExists) {
		return errorf(http.StatusConflict, "there's a profile with that name already")
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, p)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var p quality.Profile
	if err := readJSON(r, &p); err != nil {
		return err
	}
	p.ID = int(id)
	if old, err := s.Store.Profile(r.Context(), p.ID); err != nil {
		return err
	} else if old.Locked {
		return errorf(http.StatusBadRequest, "Do not touch is built in and can't be changed")
	}
	p.Locked = false
	if err := p.Check(); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if err := s.Store.UpdateProfile(r.Context(), p); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if old, err := s.Store.Profile(r.Context(), int(id)); err != nil {
		return err
	} else if old.Locked {
		return errorf(http.StatusBadRequest, "Do not touch is built in and can't be removed")
	}
	if err := s.Store.DeleteProfile(r.Context(), int(id)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) rootFolders(w http.ResponseWriter, r *http.Request) error {
	fs, err := s.Store.RootFolders(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		return err
	}
	type view struct {
		store.RootFolder
		FreeSpace  int64 `json:"freeSpace"`
		Accessible bool  `json:"accessible"`
	}
	out := make([]view, 0, len(fs))
	for _, f := range fs {
		st, err := os.Stat(f.Path)
		out = append(out, view{f, arrapi.FreeSpace(f.Path), err == nil && st.IsDir()})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addRootFolder(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Kind != "movie" && in.Kind != "series" {
		t, ok := mediatypes.Lookup(in.Kind)
		if !ok {
			return errorf(http.StatusBadRequest, "a root folder is for movies, series, or one of the media types in Settings")
		}
		on, err := mediatypes.Enabled(r.Context(), s.Store, in.Kind)
		if err != nil {
			return err
		}
		if !on {
			return errorf(http.StatusBadRequest, "turn on "+t.Name+" in Settings first")
		}
	}
	if !filepath.IsAbs(in.Path) {
		return errorf(http.StatusBadRequest, "enter the folder's full path")
	}
	p := filepath.Clean(in.Path)
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return errorf(http.StatusBadRequest, "Box Office can't see a folder at "+p)
	}
	f, err := s.Store.AddRootFolder(r.Context(), in.Kind, p)
	if errors.Is(err, store.ErrExists) {
		return errorf(http.StatusConflict, "that folder is added already")
	}
	if err != nil {
		return err
	}
	// Games need nothing picked: their folder is looked through, and the
	// games in it checked, as soon as it's added.
	if in.Kind == mediatypes.Game && s.Games != nil {
		if _, err := s.Games.Start(r.Context(), f.ID, ""); err != nil {
			s.Log.Info("not checking a new games folder now", "path", f.Path, "err", err)
		}
	}
	return writeJSON(w, http.StatusCreated, f)
}

func (s *Server) deleteRootFolder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteRootFolder(r.Context(), int(id)); err != nil {
		return err
	}
	if s.LibScan != nil {
		s.LibScan.Dismiss(int(id))
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// browse lists the folders in a folder, for picking root folders.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) error {
	p := filepath.Clean(cmp.Or(r.URL.Query().Get("path"), "/"))
	if !filepath.IsAbs(p) {
		return errorf(http.StatusBadRequest, "give a full path")
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return errorf(http.StatusNotFound, "can't open "+p)
	}
	dirs := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil && st.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	return writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "folders": dirs})
}

// ---- titles ----

func (s *Server) lookupMovies(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return writeJSON(w, http.StatusOK, []any{})
	}
	res, err := s.Catalog.LookupMovies(r.Context(), q)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

func (s *Server) lookupSeries(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return writeJSON(w, http.StatusOK, []any{})
	}
	res, err := s.Catalog.LookupShows(r.Context(), q)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

// lookupPeople finds actors and others by name; the Discover row
// person-{tmdbId} is what each is in.
func (s *Server) lookupPeople(w http.ResponseWriter, r *http.Request) error {
	res, err := s.Catalog.LookupPeople(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

func (s *Server) movies(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Store.Movies(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

func (s *Server) movie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := s.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	// Other cuts kept next to its copy, and which cut each one is.
	if m.Editions, err = s.Store.EditionFiles(r.Context(), m.ID); err != nil {
		return err
	}
	m.LabelEditions(s.Store.ReleaseNames(r.Context()))
	tw, err := s.Store.TitleWant(r.Context(), "movie", m.ID)
	if err != nil {
		return err
	}
	out := struct {
		store.Movie
		// Reported is a problem reported in Matinee that a replacement
		// is being looked for.
		Reported *store.TitleWant `json:"reported,omitempty"`
		// WantedEdition is the edition its copy should be, once one was
		// asked for in its place.
		WantedEdition string `json:"wantedEdition,omitempty"`
	}{Movie: m, WantedEdition: tw.Edition}
	if tw.Want == store.WantReplace && tw.Problem != "" {
		out.Reported = &tw
	}
	return writeJSON(w, http.StatusOK, out)
}

type addInput struct {
	TMDBID int64 `json:"tmdbId"`
	catalog.AddOptions
}

func (s *Server) addMovie(w http.ResponseWriter, r *http.Request) error {
	var in addInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	m, err := s.Catalog.AddMovie(r.Context(), in.TMDBID, in.AddOptions)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, m)
}

type editInput struct {
	Monitored  *bool   `json:"monitored"`
	ProfileID  *int    `json:"profileId"`
	RootFolder *string `json:"rootFolder"`
	Path       *string `json:"path"`
	// "" to follow Settings, or "on" or "off" for this title.
	AutoUpgrade *string `json:"autoUpgrade"`
}

func (s *Server) checkEdit(r *http.Request, in editInput, kind string) error {
	if in.ProfileID != nil {
		if _, err := s.Store.Profile(r.Context(), *in.ProfileID); err != nil {
			return errorf(http.StatusBadRequest, "there's no such quality profile")
		}
	}
	if in.AutoUpgrade != nil && *in.AutoUpgrade != "" && *in.AutoUpgrade != "on" && *in.AutoUpgrade != "off" {
		return errorf(http.StatusBadRequest, `automatic upgrades are "on", "off", or "" to follow Settings`)
	}
	if in.Path != nil && !filepath.IsAbs(*in.Path) {
		return errorf(http.StatusBadRequest, "give the folder's full path")
	}
	if in.RootFolder != nil {
		fs, err := s.Store.RootFolders(r.Context(), kind)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(fs, func(f store.RootFolder) bool { return f.Path == *in.RootFolder }) {
			return errorf(http.StatusBadRequest, "that isn't one of the root folders")
		}
	}
	return nil
}

func (s *Server) editMovie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	m, err := s.Store.Movie(r.Context(), id)
	if err != nil {
		return err
	}
	var in editInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.checkEdit(r, in, "movie"); err != nil {
		return err
	}
	if in.Monitored != nil {
		m.Monitored = *in.Monitored
	}
	if in.ProfileID != nil {
		m.ProfileID = *in.ProfileID
	}
	if in.RootFolder != nil && *in.RootFolder != m.RootFolder {
		m.RootFolder = *in.RootFolder
		m.Path = filepath.Join(m.RootFolder, catalog.FolderName(m.Title, m.Year))
	}
	if in.Path != nil {
		m.Path = filepath.Clean(*in.Path)
	}
	if in.AutoUpgrade != nil {
		m.AutoUpgrade = *in.AutoUpgrade
	}
	if err := s.Store.UpdateMovie(r.Context(), m); err != nil {
		return err
	}
	return s.movie(w, r)
}

// deleteMovie takes a movie off the list, cancelling its downloads. Its
// folder stays on disk unless deleteFiles=true asks for it to be deleted
// too (see removal.go).
func (s *Server) deleteMovie(w http.ResponseWriter, r *http.Request) error {
	m, p, err := s.moviePlan(r)
	if err != nil {
		return err
	}
	if flag(r, "deleteFiles") {
		if err := s.carryOut(r, p, m.Title); err != nil {
			return err
		}
	}
	if err := s.Store.DeleteMovie(r.Context(), m.ID); err != nil {
		return err
	}
	s.cancelDownloads(r.Context(), "movie", []int64{m.ID}, m.Title)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) searchMovie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.CheckUnlocked(r.Context(), "movie", id); err != nil {
		return err
	}
	if err := s.Catalog.SearchMoviesByHand(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (s *Server) refreshMovie(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Catalog.RefreshMovie(r.Context(), id); err != nil {
		return err
	}
	return s.movie(w, r)
}

func (s *Server) seriesList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Store.SeriesAll(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

// series is a show with its episodes.
func (s *Server) series(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return err
	}
	eps, err := s.Store.Episodes(r.Context(), id)
	if err != nil {
		return err
	}
	all, err := s.Store.ReportedWants(r.Context(), "episode")
	if err != nil {
		return err
	}
	// Problems reported in Matinee with its episodes, being fixed.
	reported := []store.TitleWant{}
	for _, e := range eps {
		if tw, ok := all[e.ID]; ok {
			reported = append(reported, tw)
		}
	}
	return writeJSON(w, http.StatusOK, map[string]any{"series": t, "episodes": eps, "reported": reported})
}

// showDefaults are which seasons a new show watches when nobody picks:
// Settings' "New shows" card, and the add-show form's starting choice.
func (s *Server) showDefaults(w http.ResponseWriter, r *http.Request) error {
	d, err := catalog.LoadShowDefaults(r.Context(), s.Store)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, d)
}

func (s *Server) saveShowDefaults(w http.ResponseWriter, r *http.Request) error {
	var in catalog.ShowDefaults
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := catalog.SaveShowDefaults(r.Context(), s.Store, in); err != nil {
		return err
	}
	return s.showDefaults(w, r)
}

func (s *Server) addSeries(w http.ResponseWriter, r *http.Request) error {
	var in addInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	t, err := s.Catalog.AddSeries(r.Context(), in.TMDBID, in.AddOptions)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, t)
}

func (s *Server) editSeries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return err
	}
	var in editInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.checkEdit(r, in, "series"); err != nil {
		return err
	}
	if in.Monitored != nil {
		t.Monitored = *in.Monitored
	}
	if in.ProfileID != nil {
		t.ProfileID = *in.ProfileID
	}
	if in.RootFolder != nil && *in.RootFolder != t.RootFolder {
		t.RootFolder = *in.RootFolder
		t.Path = filepath.Join(t.RootFolder, catalog.FolderName(t.Title, t.Year))
	}
	if in.Path != nil {
		t.Path = filepath.Clean(*in.Path)
	}
	if in.AutoUpgrade != nil {
		t.AutoUpgrade = *in.AutoUpgrade
	}
	if err := s.Store.UpdateSeries(r.Context(), t); err != nil {
		return err
	}
	return s.series(w, r)
}

// deleteSeries takes a show off the list, cancelling its downloads. Its
// folder stays on disk unless deleteFiles=true asks for it to be deleted
// too (see removal.go).
func (s *Server) deleteSeries(w http.ResponseWriter, r *http.Request) error {
	t, p, err := s.seriesPlan(r)
	if err != nil {
		return err
	}
	if flag(r, "deleteFiles") {
		if err := s.carryOut(r, p, t.Title); err != nil {
			return err
		}
	}
	if err := s.Store.DeleteSeries(r.Context(), t.ID); err != nil {
		return err
	}
	s.cancelDownloads(r.Context(), "series", []int64{t.ID}, t.Title)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) searchSeries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.CheckUnlocked(r.Context(), "series", id); err != nil {
		return err
	}
	if err := s.Catalog.SearchSeriesByHand(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (s *Server) refreshSeries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Catalog.RefreshSeries(r.Context(), id); err != nil {
		return err
	}
	return s.series(w, r)
}

func (s *Server) editSeason(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		return errorf(http.StatusNotFound, "not found")
	}
	var in struct {
		Monitored bool `json:"monitored"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	t, err := s.Store.Series(r.Context(), id)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(t.Seasons, func(se store.Season) bool { return se.Number == n }) {
		return errorf(http.StatusNotFound, "there's no such season")
	}
	if err := s.Store.SetSeason(r.Context(), id, store.Season{Number: n, Monitored: in.Monitored}); err != nil {
		return err
	}
	return s.series(w, r)
}

func (s *Server) editEpisode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Monitored bool `json:"monitored"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Store.SetEpisodeMonitored(r.Context(), id, in.Monitored); err != nil {
		return err
	}
	e, err := s.Store.Episode(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, e)
}

func (s *Server) searchEpisode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.CheckUnlocked(r.Context(), "episode", id); err != nil {
		return err
	}
	if err := s.Catalog.SearchEpisodesByHand(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	from := parseDay(q.Get("start"), time.Now().AddDate(0, 0, -7))
	to := parseDay(q.Get("end"), from.AddDate(0, 0, 42))
	if to.Sub(from) > 400*24*time.Hour {
		return errorf(http.StatusBadRequest, "ask for a year at most")
	}
	up, err := s.Catalog.Calendar(r.Context(), from, to)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, up)
}

func parseDay(s string, def time.Time) time.Time {
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return def
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) error {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		n = 100
	}
	js, err := s.Store.Jobs(r.Context(), min(n, 500))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, js)
}

// ---- backups ----

// backupView is the settings without their secrets.
type backupView struct {
	EveryHours    int    `json:"everyHours"`
	Keep          int    `json:"keep"`
	HasPassphrase bool   `json:"hasPassphrase"`
	Remote        remote `json:"remote"`
}

type remote struct {
	Kind      string `json:"kind"`
	Folder    string `json:"folder,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	HasSecret bool   `json:"hasSecret,omitempty"`
	PathStyle bool   `json:"pathStyle,omitempty"`
}

func view(st backup.Settings) backupView {
	return backupView{EveryHours: st.EveryHours, Keep: st.Keep, HasPassphrase: st.Passphrase != "", Remote: remote{
		Kind: st.Remote.Kind, Folder: st.Remote.Folder, Endpoint: st.Remote.S3.Endpoint, Region: st.Remote.S3.Region,
		Bucket: st.Remote.S3.Bucket, Prefix: st.Remote.S3.Prefix, AccessKey: st.Remote.S3.AccessKey,
		HasSecret: st.Remote.S3.SecretKey != "", PathStyle: st.Remote.S3.PathStyle}}
}

func (s *Server) backups(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Backups.Settings(r.Context())
	if err != nil {
		return err
	}
	list, listErr := s.Backups.List(r.Context())
	out := map[string]any{"settings": view(st), "backups": list, "dir": s.Backups.Dir}
	if list == nil {
		out["backups"] = []any{}
	}
	if listErr != nil {
		out["remoteError"] = listErr.Error()
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) makeBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.Backups.Make(r.Context(), backup.ReasonManual)
	if err != nil && b.Name == "" {
		return err
	}
	out := map[string]any{"backup": b}
	if err != nil {
		out["warning"] = err.Error()
	}
	return writeJSON(w, http.StatusCreated, out)
}

// backupInput is the settings as the UI sends them: an empty passphrase or
// secret keeps the saved one; clearPassphrase removes it.
type backupInput struct {
	EveryHours      int    `json:"everyHours"`
	Keep            int    `json:"keep"`
	Passphrase      string `json:"passphrase"`
	ClearPassphrase bool   `json:"clearPassphrase"`
	Remote          struct {
		remote
		SecretKey string `json:"secretKey"`
	} `json:"remote"`
}

func (s *Server) backupSettingsFrom(r *http.Request) (backup.Settings, error) {
	var in backupInput
	if err := readJSON(r, &in); err != nil {
		return backup.Settings{}, err
	}
	old, err := s.Backups.Settings(r.Context())
	if err != nil {
		return old, err
	}
	st := backup.Settings{EveryHours: in.EveryHours, Keep: in.Keep, Passphrase: old.Passphrase}
	if in.Passphrase != "" {
		st.Passphrase = in.Passphrase
	}
	if in.ClearPassphrase {
		st.Passphrase = ""
	}
	rm := in.Remote
	st.Remote = backup.Remote{Kind: rm.Kind, Folder: rm.Folder, S3: backup.S3{Endpoint: strings.TrimSpace(rm.Endpoint), Region: strings.TrimSpace(rm.Region),
		Bucket: strings.TrimSpace(rm.Bucket), Prefix: strings.TrimSpace(rm.Prefix), AccessKey: strings.TrimSpace(rm.AccessKey), SecretKey: rm.SecretKey, PathStyle: rm.PathStyle}}
	if st.Remote.S3.SecretKey == "" && old.Remote.S3.AccessKey == st.Remote.S3.AccessKey {
		st.Remote.S3.SecretKey = old.Remote.S3.SecretKey
	}
	st.Normalize()
	if err := st.Check(); err != nil {
		return st, errorf(http.StatusBadRequest, err.Error())
	}
	return st, nil
}

func (s *Server) saveBackupSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.backupSettingsFrom(r)
	if err != nil {
		return err
	}
	if err := s.Backups.SaveSettings(r.Context(), st); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return s.backups(w, r)
}

func (s *Server) testBackupRemote(w http.ResponseWriter, r *http.Request) error {
	st, err := s.backupSettingsFrom(r)
	if err != nil {
		return err
	}
	if st.Remote.Kind == "" {
		return errorf(http.StatusBadRequest, "pick a place to test")
	}
	if err := s.Backups.TestRemote(r.Context(), st); err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]string{"result": "Box Office wrote a test file there and read it back."})
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !backup.ValidName(name) {
		return errorf(http.StatusNotFound, "there's no such backup")
	}
	rc, err := s.Backups.Open(r.Context(), name)
	if err != nil {
		return errorf(http.StatusNotFound, "there's no such backup")
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, err = io.Copy(w, rc)
	return err
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !backup.ValidName(name) {
		return errorf(http.StatusNotFound, "there's no such backup")
	}
	if err := s.Backups.Restore(r.Context(), name); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]string{"result": "restored"})
}

// restoreUpload restores a backup file sent as the request body.
func (s *Server) restoreUpload(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("X-Requested-With") == "" && user(r).ID != 0 {
		return errorf(http.StatusForbidden, "send the file from Box Office's own page")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<30))
	if err != nil {
		return err
	}
	if err := s.Backups.RestoreData(r.Context(), data); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]string{"result": "restored"})
}

// ---- migrations ----

func (s *Server) previewMigration(w http.ResponseWriter, r *http.Request) error {
	var in migrate.Request
	if err := readJSON(r, &in); err != nil {
		return err
	}
	p, err := s.Migrator.Preview(r.Context(), in)
	if err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	return writeJSON(w, http.StatusOK, p)
}

// startScan previews a migration in the background; the scan's progress
// and, once it's ready, its plan are read from scan.
func (s *Server) startScan(w http.ResponseWriter, r *http.Request) error {
	var in migrate.Request
	if err := readJSON(r, &in); err != nil {
		return err
	}
	sc, err := s.Migrator.StartScan(in)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusAccepted, sc)
}

func (s *Server) scans(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.Migrator.Scans())
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) error {
	sc, ok := s.Migrator.ScanByID(r.PathValue("id"))
	if !ok {
		return errorf(http.StatusNotFound, "that look-over has ended; connect again")
	}
	return writeJSON(w, http.StatusOK, sc)
}

func (s *Server) dismissScan(w http.ResponseWriter, r *http.Request) error {
	s.Migrator.DismissScan(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// health looks Box Office over for problems an admin can fix.
func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	c := &health.Checker{Store: s.Store, Backups: s.Backups, TMDBKey: func(ctx context.Context) string { return s.Catalog.TMDB(ctx).Key }}
	return writeJSON(w, http.StatusOK, map[string]any{"issues": c.Check(ctx), "checked": time.Now().UTC()})
}

// settingMovedIn is set once an admin has finished moving in. Move in then
// leaves the main menu for Settings, where it stays for an app added later.
const settingMovedIn = "moved_in"

func (s *Server) setMovedIn(done bool) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		v := ""
		if done {
			v = time.Now().UTC().Format(time.RFC3339)
		}
		if err := s.Store.SetSetting(r.Context(), settingMovedIn, v); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// settingSkipped lists the apps an admin chose not to move in from, so
// Move in stops offering them.
const settingSkipped = "move_in_skipped"

var moveInApps = []string{"radarr", "sonarr", "lidarr", "readarr", "nzbget"}

func (s *Server) skipped(ctx context.Context) ([]string, error) {
	v, err := s.Store.Setting(ctx, settingSkipped)
	if err != nil {
		return nil, err
	}
	out := []string{}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return []string{}, nil
		}
	}
	return out, nil
}

func (s *Server) skippedApps(w http.ResponseWriter, r *http.Request) error {
	list, err := s.skipped(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

// skipApp skips an app in Move in, or offers it again. Nothing is changed
// in the app or the library: it's only taken off the list of apps to do.
func (s *Server) skipApp(skip bool) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		app := r.PathValue("app")
		if !slices.Contains(moveInApps, app) {
			return errorf(http.StatusNotFound, "there's no app called "+app+" to move in from")
		}
		list, err := s.skipped(r.Context())
		if err != nil {
			return err
		}
		list = slices.DeleteFunc(list, func(a string) bool { return a == app })
		if skip {
			list = append(list, app)
		}
		b, _ := json.Marshal(list)
		if err := s.Store.SetSetting(r.Context(), settingSkipped, string(b)); err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, list)
	}
}

// findApp looks for an app to move in from at its usual addresses.
func (s *Server) findApp(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, map[string]string{"url": s.Migrator.Find(r.Context(), r.URL.Query().Get("app"))})
}

func (s *Server) runMigration(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
		migrate.Options
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	id, err := s.Migrator.Run(r.Context(), in.Token, in.Options)
	if err != nil {
		return errorf(http.StatusConflict, err.Error())
	}
	pr, err := s.Migrator.Progress(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, pr)
}

func (s *Server) migrations(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Migrator.List(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

func (s *Server) migration(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pr, err := s.Migrator.Progress(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, pr)
}

func (s *Server) switchMatinee(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MigrationID int64 `json:"migrationId"`
		migrate.SwitchRequest
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.Migrator.SwitchMatinee(r.Context(), in.MigrationID, in.SwitchRequest, s.APIKey(r.Context()))
	if errors.Is(err, store.ErrNotFound) {
		return errorf(http.StatusNotFound, "there's no such migration")
	}
	if err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	return writeJSON(w, http.StatusOK, res)
}
