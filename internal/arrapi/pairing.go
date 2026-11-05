package arrapi

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Pairing is what Box Office offers Matinee beyond the Radarr and Sonarr
// API: live download progress, other editions of a movie, and better or
// replacement copies. The server fills it in; a handler without it
// answers none of this and advertises none of it, so Matinee carries on
// as it does with Radarr and Sonarr.
type Pairing struct {
	// Live is what the downloader is doing now.
	Live func() LiveState
	// Waiting says why queued downloads aren't starting, or "".
	Waiting func() string
	// CanImport says a finished download is waiting to be moved into the
	// library by hand.
	CanImport func(store.Download) bool
	// Editions searches for a movie's editions; owned are those the
	// library has.
	Editions func(ctx context.Context, movieID int64, owned []string) (any, []string, error)
	// Ask queues a search for what was asked: kind is movie or episode.
	Ask func(ctx context.Context, kind string, id int64, subject, edition, mode, want string) error
}

// LiveState is the downloader's live progress.
type LiveState struct {
	Speed int64 // bytes a second
	Steps map[int64]LiveStep
}

// LiveStep is one working download's progress.
type LiveStep struct {
	DoneBytes, Size     int64
	StepDone, StepTotal int64
	Stage               string
}

// Features are what this Box Office adds for Matinee, for
// system/status's boxOfficeFeatures.
func (h *Handler) features() []string {
	f := []string{"notifications", "discover"}
	if h.Pairing != nil {
		f = append(f, "progress", "upgrades")
		if h.App == Radarr {
			f = append(f, "editions")
		}
	}
	return f
}

func (h *Handler) pairingRoutes() {
	h.route("GET /api/v3/boxoffice/activity", h.needPairing(h.activity))
	h.route("POST /api/v3/boxoffice/upgrade", h.needPairing(h.upgrade))
	if h.App == Radarr {
		h.route("GET /api/v3/boxoffice/editions", h.needPairing(h.editions))
		h.route("POST /api/v3/boxoffice/editions", h.needPairing(h.askEdition))
	}
}

func (h *Handler) needPairing(fn func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if h.Pairing == nil {
			return errNotFound
		}
		return fn(w, r)
	}
}

// How long finished downloads stay on the activity list: long enough for
// Matinee to show a title arriving, and a problem until someone sees it.
const (
	activityDone   = 30 * time.Minute
	activityFailed = 24 * time.Hour
)

// activityItem is one download, as Matinee shows it on a title's poster.
type activityItem struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"` // movie or series
	TMDBID   int64  `json:"tmdbId,omitempty"`
	TVDBID   int64  `json:"tvdbId,omitempty"`
	IMDBID   string `json:"imdbId,omitempty"`
	MovieID  int64  `json:"movieId,omitempty"`
	SeriesID int64  `json:"seriesId,omitempty"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Poster   string `json:"poster,omitempty"`
	// Episodes are the episodes it's for, as season and episode numbers.
	Episodes []activityEpisode `json:"episodes,omitempty"`
	// Stage is one of queued, downloading, paused, waiting (downloaded,
	// waiting its turn to be checked), checking, repairing, unpacking,
	// importing, waitingToMove (finished, waiting to be moved into the
	// library by hand), imported, failed, quarantined.
	Stage string `json:"stage"`
	// Percent is how far the stage has got, 0 to 100.
	Percent   float64 `json:"percent"`
	Size      int64   `json:"size"`
	DoneBytes int64   `json:"doneBytes"`
	// ETA is seconds until it's downloaded, counting what's ahead of it in
	// the queue, at the speed now; 0 when it can't be told.
	ETA       int64     `json:"etaSeconds,omitempty"`
	Quality   string    `json:"quality,omitempty"`
	Edition   string    `json:"edition,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Message   string    `json:"message,omitempty"`
	AddedAt   time.Time `json:"addedAt"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

type activityEpisode struct {
	Season  int `json:"seasonNumber"`
	Episode int `json:"episodeNumber"`
}

// activity is GET /api/v3/boxoffice/activity: the downloads under way,
// and those that just finished or went wrong, for Matinee to show as
// titles coming soon. Like the notifications, both /radarr and /sonarr
// answer with everything.
func (h *Handler) activity(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	st := h.Catalog.Store
	queue, err := st.DownloadQueue(ctx)
	if err != nil {
		return err
	}
	recent, err := st.RecentDownloads(ctx, time.Now().Add(-activityFailed))
	if err != nil {
		return err
	}
	live := LiveState{}
	if h.Pairing.Live != nil {
		live = h.Pairing.Live()
	}
	id, err := st.InstanceID(ctx)
	if err != nil {
		return err
	}
	titles := titleCache{st: st}
	items := []activityItem{}
	// ahead is what's left to download before each one, in queue order.
	var ahead int64
	for _, d := range queue {
		it, ok := titles.item(ctx, d)
		if !ok {
			continue
		}
		done, size := d.DoneBytes, d.Size
		step := LiveStep{}
		if l, ok := live.Steps[d.ID]; ok {
			step = l
			done, size = max(l.DoneBytes, done), max(l.Size, size)
		}
		it.DoneBytes, it.Size = done, size
		switch d.Status {
		case store.DownloadQueued, store.DownloadDownloading:
			it.Stage = d.Status
			it.Percent = percent(done, size)
			left := max(size-done, 0)
			if live.Speed > 0 && left > 0 {
				it.ETA = (ahead + left + live.Speed - 1) / live.Speed
			}
			ahead += left
		case store.DownloadPaused:
			it.Stage, it.Percent = "paused", percent(done, size)
		case store.DownloadDownloaded:
			it.Stage, it.Percent = "waiting", 100
		case store.DownloadVerifying, store.DownloadRepairing, store.DownloadUnpacking, store.DownloadImporting:
			it.Stage = map[string]string{store.DownloadVerifying: "checking", store.DownloadRepairing: "repairing",
				store.DownloadUnpacking: "unpacking", store.DownloadImporting: "importing"}[d.Status]
			sd, stt := d.StepDone, d.StepTotal
			if step.StepTotal > 0 {
				sd, stt = step.StepDone, step.StepTotal
			}
			it.Percent = percent(sd, stt)
		default:
			continue
		}
		items = append(items, it)
	}
	now := time.Now()
	for _, d := range recent {
		var stage string
		switch {
		case d.Status == store.DownloadFailed && d.Quarantined != "":
			stage = "quarantined"
		case d.Status == store.DownloadFailed:
			stage = "failed"
		case h.Pairing.CanImport != nil && h.Pairing.CanImport(d):
			stage = "waitingToMove"
		case d.ImportedTo != "" && now.Sub(d.FinishedAt) <= activityDone:
			stage = "imported"
		default:
			continue
		}
		it, ok := titles.item(ctx, d)
		if !ok {
			continue
		}
		it.Stage, it.Size, it.DoneBytes = stage, d.Size, d.DoneBytes
		it.Percent = 100
		if stage == "failed" || stage == "quarantined" {
			it.Percent = percent(d.DoneBytes, d.Size)
		}
		it.UpdatedAt = d.FinishedAt
		items = append(items, it)
	}
	waiting := ""
	if h.Pairing.Waiting != nil {
		waiting = h.Pairing.Waiting()
	}
	return write(w, http.StatusOK, map[string]any{"boxOfficeId": id, "speed": live.Speed, "waiting": waiting, "items": items})
}

func percent(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(done) * 100 / float64(total)
	return min(max(float64(int(p*10))/10, 0), 100)
}

// titleCache looks up downloads' titles once each.
type titleCache struct {
	st     *store.Store
	movies map[int64]*store.Movie
	series map[int64]*store.Series
	eps    map[int64][]store.Episode
}

// item starts a download's activity row; false for one that isn't for a
// title on the list.
func (c *titleCache) item(ctx context.Context, d store.Download) (activityItem, bool) {
	it := activityItem{ID: d.ID, Kind: d.Kind, Edition: d.Edition, Mode: d.Mode, Message: d.Message, AddedAt: d.AddedAt}
	if q, ok := quality.ByID(d.Quality); ok && d.Quality > 0 {
		it.Quality = q.Name
	}
	switch d.Kind {
	case "movie":
		if c.movies == nil {
			c.movies = map[int64]*store.Movie{}
		}
		m, ok := c.movies[d.TitleID]
		if !ok {
			if v, err := c.st.Movie(ctx, d.TitleID); err == nil {
				m = &v
			}
			c.movies[d.TitleID] = m
		}
		if m == nil {
			return it, false
		}
		it.MovieID, it.TMDBID, it.IMDBID, it.Title, it.Year, it.Poster = m.ID, m.TMDBID, m.IMDBID, m.Title, m.Year, m.Poster
	case "series":
		if c.series == nil {
			c.series, c.eps = map[int64]*store.Series{}, map[int64][]store.Episode{}
		}
		t, ok := c.series[d.TitleID]
		if !ok {
			if v, err := c.st.Series(ctx, d.TitleID); err == nil {
				t = &v
				c.eps[t.ID], _ = c.st.Episodes(ctx, t.ID)
			}
			c.series[d.TitleID] = t
		}
		if t == nil {
			return it, false
		}
		it.SeriesID, it.TVDBID, it.TMDBID, it.IMDBID, it.Title, it.Year, it.Poster = t.ID, t.TVDBID, t.TMDBID, t.IMDBID, t.Title, t.Year, t.Poster
		for _, e := range c.eps[t.ID] {
			if slices.Contains(d.EpisodeIDs, e.ID) {
				it.Episodes = append(it.Episodes, activityEpisode{e.Season, e.Number})
			}
		}
	default:
		return it, false
	}
	return it, true
}

// ---- editions ----

// movieFor is the movie a request names, by Box Office's id (movieId,
// which is Radarr's id for it) or TMDB's (tmdbId).
func (h *Handler) movieFor(ctx context.Context, movieID, tmdbID int64) (store.Movie, error) {
	switch {
	case movieID > 0:
		return h.Catalog.Store.Movie(ctx, movieID)
	case tmdbID > 0:
		return h.Catalog.Store.MovieByTMDB(ctx, tmdbID)
	}
	return store.Movie{}, invalid("movieId", "say which movie, by movieId or tmdbId")
}

// ownedEditions are the editions of a movie the library has: its copy's,
// when the name gives one ("" when it doesn't), and those kept next to it.
func (h *Handler) ownedEditions(ctx context.Context, m store.Movie) (current string, others []string, err error) {
	if m.File != nil {
		current = quality.Edition(filepath.Base(m.File.Path))
	}
	tw, err := h.Catalog.Store.TitleWant(ctx, "movie", m.ID)
	if err != nil {
		return "", nil, err
	}
	if current == "" {
		current = tw.Edition
	}
	files, err := h.Catalog.Store.EditionFiles(ctx, m.ID)
	if err != nil {
		return "", nil, err
	}
	for _, f := range files {
		if e := quality.Edition(filepath.Base(f.Path)); e != "" && !slices.Contains(others, e) {
			others = append(others, e)
		}
	}
	return current, others, nil
}

// editions is GET /api/v3/boxoffice/editions?movieId= (or tmdbId=): the
// editions of a movie the indexers have, and those the library has. It
// searches the indexers, so it takes a few seconds.
func (h *Handler) editions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	mid, _ := strconv.ParseInt(q.Get("movieId"), 10, 64)
	tid, _ := strconv.ParseInt(q.Get("tmdbId"), 10, 64)
	m, err := h.movieFor(ctx, mid, tid)
	if err != nil {
		return err
	}
	current, others, err := h.ownedEditions(ctx, m)
	if err != nil {
		return err
	}
	owned := others
	if current != "" {
		owned = append([]string{current}, others...)
	}
	found, notes, err := h.Pairing.Editions(ctx, m.ID, owned)
	if err != nil {
		return fail(http.StatusBadGateway, err.Error())
	}
	return write(w, http.StatusOK, map[string]any{
		"movieId": m.ID, "tmdbId": m.TMDBID, "hasFile": m.File != nil, "current": current, "alongside": others,
		"editions": found, "errors": notes, "known": quality.Editions(),
	})
}

// askEdition is POST /api/v3/boxoffice/editions: get another edition of
// a movie, to replace its copy (mode replace) or to keep next to it
// (alongside). It queues a search, and answers at once.
func (h *Handler) askEdition(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MovieID int64  `json:"movieId"`
		TMDBID  int64  `json:"tmdbId"`
		Edition string `json:"edition"`
		Mode    string `json:"mode"`
	}
	if err := read(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	m, err := h.movieFor(ctx, in.MovieID, in.TMDBID)
	if err != nil {
		return err
	}
	in.Edition = strings.TrimSpace(in.Edition)
	if in.Edition == "" {
		return invalid("edition", "say which edition, like Director's Cut")
	}
	if e := quality.Edition(in.Edition); e != "" {
		in.Edition = e
	}
	if in.Mode == "" {
		in.Mode = store.ModeAlongside
	}
	if in.Mode != store.ModeAlongside && in.Mode != store.ModeReplace {
		return invalid("mode", "mode is replace or alongside")
	}
	if m.File == nil {
		// With no copy there's nothing to keep it next to.
		in.Mode = store.ModeReplace
	}
	if err := h.Catalog.Store.CheckUnlocked(ctx, "movie", m.ID); err != nil {
		return fail(http.StatusConflict, err.Error())
	}
	subject := fmt.Sprintf("%s (%s)", catalogName(m.Title, m.Year), in.Edition)
	if err := h.Pairing.Ask(ctx, "movie", m.ID, subject, in.Edition, in.Mode, ""); err != nil {
		return err
	}
	return write(w, http.StatusAccepted, map[string]any{"movieId": m.ID, "edition": in.Edition, "mode": in.Mode, "queued": true})
}

func catalogName(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

// ---- better and replacement copies ----

// upgrade is POST /api/v3/boxoffice/upgrade: look for a better copy of a
// movie or an episode (want better), or another because the one there is
// bad (want replace, with a reason). A bad copy's release goes on the
// blocklist, so it isn't grabbed again. It queues a search, and answers
// at once.
func (h *Handler) upgrade(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MovieID   int64  `json:"movieId"`
		TMDBID    int64  `json:"tmdbId"`
		EpisodeID int64  `json:"episodeId"`
		SeriesID  int64  `json:"seriesId"`
		TVDBID    int64  `json:"tvdbId"`
		Season    int    `json:"seasonNumber"`
		Episode   int    `json:"episodeNumber"`
		Want      string `json:"want"`
		Reason    string `json:"reason"`
	}
	if err := read(r, &in); err != nil {
		return err
	}
	if in.Want != store.WantBetter && in.Want != store.WantReplace {
		return invalid("want", "want is better or replace")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) > 500 {
		in.Reason = in.Reason[:500]
	}
	ctx := r.Context()
	st := h.Catalog.Store
	var kind, subject, titleKind string
	var id, titleID int64
	var file *store.File
	if h.App == Radarr {
		m, err := h.movieFor(ctx, in.MovieID, in.TMDBID)
		if err != nil {
			return err
		}
		kind, id, titleKind, titleID, file, subject = "movie", m.ID, "movie", m.ID, m.File, catalogName(m.Title, m.Year)
	} else {
		e, t, err := h.episodeFor(ctx, in.EpisodeID, in.SeriesID, in.TVDBID, in.Season, in.Episode)
		if err != nil {
			return err
		}
		kind, id, titleKind, titleID, file = "episode", e.ID, "series", t.ID, e.File
		subject = fmt.Sprintf("%s S%02dE%02d", t.Title, e.Season, e.Number)
	}
	if err := st.CheckUnlocked(ctx, titleKind, titleID); err != nil {
		return fail(http.StatusConflict, err.Error())
	}
	if file == nil {
		return fail(http.StatusConflict, "there's no copy of it yet; it's being looked for already")
	}
	blocked := ""
	if in.Want == store.WantReplace {
		// The release the bad copy came from isn't taken again.
		if d, err := st.DownloadImportedTo(ctx, file.Path); err == nil {
			blocked = d.Name
		} else if dir, err := st.DownloadImportedTo(ctx, filepath.Dir(file.Path)); err == nil {
			blocked = dir.Name
		}
		if blocked != "" {
			reason := "reported as a bad copy"
			if in.Reason != "" {
				reason += ": " + in.Reason
			}
			if err := st.Block(ctx, store.Blocked{Kind: titleKind, TitleID: titleID, SourceTitle: blocked, Quality: file.Quality,
				Protocol: "usenet", Reason: reason, At: time.Now(), Source: "matinee"}); err != nil {
				return err
			}
		}
	}
	tw, err := st.TitleWant(ctx, kind, id)
	if err != nil {
		return err
	}
	tw.Want, tw.Note = in.Want, in.Reason
	if err := st.SetTitleWant(ctx, tw); err != nil {
		return err
	}
	if err := h.Pairing.Ask(ctx, kind, id, subject, "", "", in.Want); err != nil {
		return err
	}
	return write(w, http.StatusAccepted, map[string]any{"kind": kind, "id": id, "want": in.Want, "blocked": blocked, "queued": true})
}

// episodeFor is the episode a request names: by Box Office's id, or by
// the show (seriesId or tvdbId) and season and episode numbers.
func (h *Handler) episodeFor(ctx context.Context, episodeID, seriesID, tvdbID int64, season, number int) (store.Episode, store.Series, error) {
	st := h.Catalog.Store
	if episodeID > 0 {
		e, err := st.Episode(ctx, episodeID)
		if err != nil {
			return e, store.Series{}, err
		}
		t, err := st.Series(ctx, e.SeriesID)
		return e, t, err
	}
	var t store.Series
	var err error
	switch {
	case seriesID > 0:
		t, err = st.Series(ctx, seriesID)
	case tvdbID > 0:
		t, err = st.SeriesByTVDB(ctx, tvdbID)
	default:
		return store.Episode{}, t, invalid("episodeId", "say which episode, by episodeId, or the show and its season and episode numbers")
	}
	if err != nil {
		return store.Episode{}, t, err
	}
	eps, err := st.Episodes(ctx, t.ID)
	if err != nil {
		return store.Episode{}, t, err
	}
	for _, e := range eps {
		if e.Season == season && e.Number == number {
			return e, t, nil
		}
	}
	return store.Episode{}, t, errNotFound
}
