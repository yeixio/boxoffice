// Package migrate moves a Radarr or Sonarr into Box Office, step by step
// and without changing the old app: connect to it, look over what would
// come across, confirm, optionally back both up, import, and optionally
// point Matinee at Box Office. Then the old app can be shut down.
//
// The old app is only ever read from. The one thing asked of it is a
// backup of itself, which Box Office downloads and keeps. Files stay where
// they are: Box Office records them, it doesn't move them.
package migrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// PathMap rewrites paths the old app sees into paths Box Office sees, for
// when the two mount the same folders in different places (such as /movies
// in Radarr's container and /media/movies in Box Office's).
type PathMap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// covered says whether one of maps applies to path.
func covered(maps []PathMap, path string) bool {
	for _, m := range maps {
		if from := strings.TrimRight(m.From, "/"); from != "" && (path == from || strings.HasPrefix(path, from+"/")) {
			return true
		}
	}
	return false
}

func mapPath(maps []PathMap, p string) string {
	if p == "" {
		return p
	}
	best := -1
	for i, m := range maps {
		from := strings.TrimRight(m.From, "/")
		if from == "" {
			continue
		}
		if (p == from || strings.HasPrefix(p, from+"/")) && (best < 0 || len(from) > len(strings.TrimRight(maps[best].From, "/"))) {
			best = i
		}
	}
	if best < 0 {
		return filepath.Clean(p)
	}
	from := strings.TrimRight(maps[best].From, "/")
	return filepath.Clean(filepath.Join(maps[best].To, strings.TrimPrefix(p, from)))
}

// Request is what an admin enters to start: which app, where it is, and
// how its paths map.
type Request struct {
	App     string    `json:"app"` // radarr, sonarr or nzbget
	URL     string    `json:"url"`
	APIKey  string    `json:"apiKey"`
	PathMap []PathMap `json:"pathMap"`
	// Username and Password sign in to NZBGet, which has no API key.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Plan is what a migration would do, for an admin to look over and confirm.
type Plan struct {
	Token       string        `json:"token"`
	App         string        `json:"app"`
	AppVersion  string        `json:"appVersion"`
	URL         string        `json:"url"`
	Profiles    []ProfilePlan `json:"profiles"`
	RootFolders []FolderPlan  `json:"rootFolders"`
	Titles      Counts        `json:"titles"`
	Files       FileCounts    `json:"files"`
	Sample      []Sample      `json:"sample"`
	Settings    SettingsPlan  `json:"settings"`
	Servers     []ServerPlan  `json:"servers,omitempty"` // NZBGet's news servers
	Warnings    []string      `json:"warnings"`
	ExpiresAt   time.Time     `json:"expiresAt"`
	// PathMap is the path maps the import uses. PathMapFound says Box
	// Office worked them out itself, because none were entered.
	PathMap      []PathMap `json:"pathMap"`
	PathMapFound bool      `json:"pathMapFound"`
}

// SettingsPlan counts the settings that would come across besides titles.
type SettingsPlan struct {
	Indexers        int      `json:"indexers"`
	DownloadClients int      `json:"downloadClients"`
	Notifications   int      `json:"notifications"`
	ImportLists     int      `json:"importLists"`
	Blocklist       int      `json:"blocklist"`
	UsenetServers   int      `json:"usenetServers"`
	Categories      int      `json:"categories"`
	Sections        []string `json:"sections"` // every part of the app's settings that's kept
	// HiddenSecrets are passwords and keys the app hides over its API.
	// They're read from its backup, so they come across only when the
	// backup step runs.
	HiddenSecrets int `json:"hiddenSecrets"`
}

// ServerPlan is one of NZBGet's news servers.
type ServerPlan struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         bool   `json:"tls"`
	Connections int    `json:"connections"`
	Level       int    `json:"level"`
	HasPassword bool   `json:"hasPassword"`
}

// ProfilePlan is what happens to one quality profile.
type ProfilePlan struct {
	LegacyID  int      `json:"legacyId"`
	Name      string   `json:"name"`
	Action    string   `json:"action"` // create or match
	LocalID   int      `json:"localId,omitempty"`
	Qualities []string `json:"qualities"`
}

// FolderPlan is what happens to one root folder.
type FolderPlan struct {
	LegacyPath string `json:"legacyPath"`
	Path       string `json:"path"`   // as Box Office sees it
	Exists     bool   `json:"exists"` // Box Office can see it
	Action     string `json:"action"` // add or have
}

// Counts are titles by what happens to them.
type Counts struct {
	Total   int `json:"total"`
	New     int `json:"new"`
	Already int `json:"already"` // on Box Office's list already; left as they are
	Skipped int `json:"skipped"` // can't be imported; see Warnings
}

// FileCounts are the files the old app knows of.
type FileCounts struct {
	Total int `json:"total"`
	// Missing files are ones Box Office can't see at their path, usually
	// because a path map is needed.
	Missing int `json:"missing"`
}

// Sample is one of the titles that would come across.
type Sample struct {
	Title   string `json:"title"`
	Year    int    `json:"year,omitempty"`
	Path    string `json:"path"`
	HasFile bool   `json:"hasFile"`
}

// Options are the admin's choices when confirming.
type Options struct {
	BackupBoxOffice bool `json:"backupBoxOffice"`
	BackupLegacy    bool `json:"backupLegacy"`
}

// Step is one step of a running migration.
type Step struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // waiting, running, done, skipped, failed
	Message string `json:"message,omitempty"`
}

// Progress is a migration as it runs, and its report when it's done.
type Progress struct {
	ID       int64     `json:"id"`
	App      string    `json:"app"`
	Status   string    `json:"status"` // running, done, failed
	Steps    []Step    `json:"steps"`
	Imported Counts    `json:"imported"`
	Files    int       `json:"files"`
	Warnings []string  `json:"warnings"`
	Backups  []string  `json:"backups"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	// PathMap and Folders are kept so switching Matinee over can map its
	// saved root folder too.
	PathMap []PathMap `json:"pathMap"`
	// Matinee is set once Matinee has been switched over.
	Matinee string `json:"matinee,omitempty"`
}

// Migrator runs migrations.
type Migrator struct {
	Catalog *catalog.Service
	Backups *backup.Manager
	Log     *slog.Logger

	// DataDir is Box Office's own data folder, never taken for media.
	DataDir string
	// Candidates and MediaDirs replace where Find looks for an app and
	// where Preview looks for the old app's folders; for tests.
	Candidates func(app string) []string
	MediaDirs  func() []string

	mu      sync.Mutex
	plans   map[string]*pending
	scans   map[string]*Scan
	running map[int64]*Progress
}

type pending struct {
	plan     Plan
	req      Request
	l        *legacy
	profiles []lProfile
	roots    []lRoot
	movies   []lMovie
	series   []lSeries
	settings settingsData
	// backupZip is the old app's own backup, once the backup step has run;
	// the secrets it hides over its API are read from it.
	backupZip []byte
	nzb       *nzbget
	nzbConfig nzbgetConfig
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Preview connects to the old app, reads everything, and says what a
// migration would do. Nothing changes anywhere.
func (m *Migrator) Preview(ctx context.Context, req Request) (Plan, error) {
	return m.preview(ctx, req, func(string, int, int) {})
}

// report says what a preview is doing: a step, and how far through it,
// when that's known.
type report func(step string, done, total int)

func (m *Migrator) preview(ctx context.Context, req Request, say report) (Plan, error) {
	req.App = strings.ToLower(strings.TrimSpace(req.App))
	if req.App == "nzbget" {
		say("Reading NZBGet's settings", 0, 0)
		plan, p, err := m.previewNZBGet(ctx, req)
		if err != nil {
			return Plan{}, err
		}
		p.plan = plan
		m.keep(p)
		return plan, nil
	}
	l, err := newLegacy(req.App, req.URL, req.APIKey)
	if err != nil {
		return Plan{}, err
	}
	say("Connecting to "+l.name(), 0, 0)
	st, err := l.status(ctx)
	if err != nil {
		return Plan{}, err
	}
	p := &pending{req: req, l: l}
	say("Reading quality profiles and root folders", 0, 0)
	if err := l.get(ctx, "/qualityprofile", nil, &p.profiles); err != nil {
		return Plan{}, err
	}
	if err := l.get(ctx, "/rootfolder", nil, &p.roots); err != nil {
		return Plan{}, err
	}
	say(map[string]string{"radarr": "Reading the movies", "sonarr": "Reading the shows"}[req.App], 0, 0)
	if req.App == "radarr" {
		if err := l.get(ctx, "/movie", nil, &p.movies); err != nil {
			return Plan{}, err
		}
	} else if err := l.get(ctx, "/series", nil, &p.series); err != nil {
		return Plan{}, err
	}
	say("Reading indexers, download clients and settings", 0, 0)
	if p.settings, err = l.readSettings(ctx); err != nil {
		return Plan{}, err
	}
	// Maps typed in are used as they are; the old app's other root folders
	// get the maps Box Office finds. (A typed map for one library, such as
	// movies, mustn't leave the shows unmapped.)
	say("Finding where Box Office sees your files", 0, 0)
	found := false
	for _, pm := range m.findPathMaps(p) {
		if !covered(req.PathMap, pm.From) {
			req.PathMap = append(req.PathMap, pm)
			found = true
		}
	}
	p.req = req
	plan := Plan{PathMap: orEmpty(req.PathMap), PathMapFound: found, Token: newToken(), App: req.App, AppVersion: st.Version, URL: l.base.String(), Warnings: []string{},
		Profiles: []ProfilePlan{}, RootFolders: []FolderPlan{}, Sample: []Sample{}, ExpiresAt: time.Now().Add(planLife)}

	locals, err := m.Catalog.Store.Profiles(ctx)
	if err != nil {
		return Plan{}, err
	}
	for _, lp := range p.profiles {
		pp := ProfilePlan{LegacyID: lp.ID, Name: lp.Name, Action: "create", Qualities: []string{}}
		for _, id := range mapProfile(lp).Allowed {
			q, _ := quality.ByID(id)
			pp.Qualities = append(pp.Qualities, q.Name)
		}
		for _, lo := range locals {
			if strings.EqualFold(lo.Name, lp.Name) {
				pp.Action, pp.LocalID = "match", lo.ID
			}
		}
		plan.Profiles = append(plan.Profiles, pp)
	}

	kind := map[string]string{"radarr": "movie", "sonarr": "series"}[req.App]
	have, err := m.Catalog.Store.RootFolders(ctx, kind)
	if err != nil {
		return Plan{}, err
	}
	for _, r := range p.roots {
		fp := FolderPlan{LegacyPath: r.Path, Path: mapPath(req.PathMap, r.Path), Action: "add"}
		fp.Exists = isDir(fp.Path)
		if slices.ContainsFunc(have, func(f store.RootFolder) bool { return f.Path == fp.Path }) {
			fp.Action = "have"
		}
		if !fp.Exists {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("Box Office can't see the root folder %s. Mount it in Box Office, or add a path map from %s.", fp.Path, r.Path))
		}
		plan.RootFolders = append(plan.RootFolders, fp)
	}

	sample := func(s Sample) {
		if len(plan.Sample) < 12 {
			plan.Sample = append(plan.Sample, s)
		}
	}
	noTMDB := 0
	for i, mv := range p.movies {
		if i%25 == 0 {
			if err := ctx.Err(); err != nil {
				return Plan{}, err
			}
			say("Checking the movies and their files", i, len(p.movies))
		}
		plan.Titles.Total++
		if mv.TMDBID <= 0 {
			plan.Titles.Skipped++
			noTMDB++
			continue
		}
		if mv.MovieFile != nil {
			plan.Files.Total++
			if !fileExists(mapPath(req.PathMap, mv.MovieFile.Path)) {
				plan.Files.Missing++
			}
		}
		if _, err := m.Catalog.Store.MovieByTMDB(ctx, mv.TMDBID); err == nil {
			plan.Titles.Already++
			continue
		}
		plan.Titles.New++
		sample(Sample{Title: mv.Title, Year: mv.Year, Path: mapPath(req.PathMap, mv.Path), HasFile: mv.HasFile})
	}
	needTMDB := 0
	for i, sr := range p.series {
		if i%25 == 0 {
			say("Checking the shows", i, len(p.series))
		}
		plan.Titles.Total++
		if sr.TMDBID > 0 {
			if _, err := m.Catalog.Store.SeriesByTMDB(ctx, sr.TMDBID); err == nil {
				plan.Titles.Already++
				continue
			}
		} else if _, err := m.Catalog.Store.SeriesByTVDB(ctx, sr.TVDBID); err == nil {
			plan.Titles.Already++
			continue
		} else {
			needTMDB++
		}
		plan.Titles.New++
		sample(Sample{Title: sr.Title, Year: sr.Year, Path: mapPath(req.PathMap, sr.Path)})
	}
	if noTMDB > 0 {
		plan.Warnings = append(plan.Warnings, count(noTMDB, "movie has", "movies have")+" no TMDB id in Radarr and will be left out.")
	}
	if needTMDB > 0 && m.Catalog.TMDB(ctx).Key == "" {
		plan.Warnings = append(plan.Warnings, count(needTMDB, "show has", "shows have")+" no TMDB id in Sonarr. Add a TMDB key under Settings so Box Office can look them up by their TVDB id; without one they're left out.")
	}
	if req.App == "sonarr" {
		plan.Files.Total = -1 // counted while importing; Sonarr lists files per show
	}
	if plan.Files.Missing > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("Box Office can't see %d of the %d files at their paths. They're imported anyway, but check the path maps first.", plan.Files.Missing, plan.Files.Total))
	}
	d := p.settings
	plan.Settings = SettingsPlan{Indexers: len(d.providers["indexer"]), DownloadClients: len(d.providers["downloadclient"]),
		Notifications: len(d.providers["notification"]), ImportLists: len(d.providers["importlist"]), Blocklist: len(d.blocklist),
		Sections: slices.Sorted(maps.Keys(d.raw)), HiddenSecrets: d.hidden()}
	if plan.Settings.HiddenSecrets > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s hides %s from other apps. Keep \"Back up %s\" on and Box Office reads them from the backup; skip it and they come across blank.", l.name(), count(plan.Settings.HiddenSecrets, "password or API key", "passwords and API keys"), l.name()))
	}
	p.plan = plan
	m.keep(p)
	return plan, nil
}

// findPathMaps works out the path maps for the old app's root folders from
// where Box Office finds their titles.
func (m *Migrator) findPathMaps(p *pending) []PathMap {
	roots := make([]string, 0, len(p.roots))
	for _, r := range p.roots {
		roots = append(roots, filepath.Clean(r.Path))
	}
	var paths []string
	for _, mv := range p.movies {
		paths = append(paths, mv.Path)
	}
	for _, sr := range p.series {
		paths = append(paths, sr.Path)
	}
	titles := map[string][]string{}
	for _, t := range paths {
		t = filepath.Clean(t)
		for _, r := range roots {
			if strings.HasPrefix(t, strings.TrimRight(r, "/")+"/") {
				titles[r] = append(titles[r], t)
				break
			}
		}
	}
	dirs := m.MediaDirs
	if dirs == nil {
		dirs = func() []string { return mediaDirs([]string{m.DataDir}) }
	}
	return findPathMaps(roots, titles, dirs())
}

func orEmpty(maps []PathMap) []PathMap {
	if maps == nil {
		return []PathMap{}
	}
	return maps
}

// planLife is how long a plan can wait to be confirmed, such as while
// other apps are being looked over.
const planLife = 3 * time.Hour

// keep holds a plan until it's run or expires.
func (m *Migrator) keep(p *pending) {
	m.mu.Lock()
	if m.plans == nil {
		m.plans = map[string]*pending{}
	}
	for t, old := range m.plans {
		if time.Now().After(old.plan.ExpiresAt) {
			delete(m.plans, t)
		}
	}
	m.plans[p.plan.Token] = p
	m.mu.Unlock()
}

// count says "1 movie" or "2 movies".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// mapProfile turns an old app's quality profile into Box Office's.
func mapProfile(lp lProfile) quality.Profile {
	p := quality.Profile{Name: lp.Name, UpgradeOK: lp.UpgradeAllowed}
	cutoff := -1
	var walk func(items []lProfileItem, parentAllowed bool, groupHasCutoff bool)
	walk = func(items []lProfileItem, parentAllowed, groupHasCutoff bool) {
		for _, it := range items {
			allowed := it.Allowed || parentAllowed
			if it.Quality == nil {
				// A group: its own id can be the cutoff.
				walk(it.Items, allowed, false)
				continue
			}
			q := quality.Parse(it.Quality.Name)
			if q.ID == 0 {
				continue
			}
			if allowed && !slices.Contains(p.Allowed, q.ID) {
				p.Allowed = append(p.Allowed, q.ID)
			}
			if it.Quality.ID == lp.Cutoff && allowed {
				cutoff = q.ID
			}
		}
	}
	walk(lp.Items, false, false)
	// A group's id as the cutoff: its best quality.
	if cutoff < 0 {
		for _, it := range lp.Items {
			if it.Quality == nil && groupID(it) == lp.Cutoff {
				for _, sub := range it.Items {
					if sub.Quality != nil {
						if q := quality.Parse(sub.Quality.Name); q.ID != 0 && (cutoff < 0 || quality.Rank(q.ID) > quality.Rank(cutoff)) {
							cutoff = q.ID
						}
					}
				}
			}
		}
	}
	slices.SortFunc(p.Allowed, func(a, b int) int { return quality.Rank(a) - quality.Rank(b) })
	if len(p.Allowed) == 0 {
		p.Allowed = []int{quality.Parse("WEBDL-1080p").ID}
	}
	if cutoff < 0 || !slices.Contains(p.Allowed, cutoff) {
		cutoff = p.Allowed[len(p.Allowed)-1]
	}
	p.Cutoff = cutoff
	return p
}

// groupID is a profile group's id; Radarr and Sonarr number groups from 1000.
func groupID(it lProfileItem) int { return it.ID }

// Run carries out a plan in the background and returns its migration id.
func (m *Migrator) Run(ctx context.Context, token string, o Options) (int64, error) {
	m.mu.Lock()
	p, ok := m.plans[token]
	if ok {
		delete(m.plans, token)
	}
	for _, pr := range m.running {
		if pr.Status == "running" {
			m.mu.Unlock()
			return 0, errors.New("a migration is already running; wait for it to finish")
		}
	}
	m.mu.Unlock()
	if !ok || time.Now().After(p.plan.ExpiresAt) {
		return 0, errors.New("that preview has expired; connect again to see a fresh one")
	}
	id, err := m.Catalog.Store.StartMigration(ctx, p.req.App, p.plan.URL)
	if err != nil {
		return 0, err
	}
	pr := &Progress{ID: id, App: p.req.App, Status: "running", Started: time.Now().UTC(), Warnings: []string{}, Backups: []string{}, PathMap: p.req.PathMap}
	if p.req.App == "nzbget" {
		pr.Steps = []Step{
			{Name: "Back up Box Office", Status: "waiting"},
			{Name: "Back up NZBGet's settings", Status: "waiting"},
			{Name: "Bring over news servers, categories and settings", Status: "waiting"},
		}
	} else {
		name := p.l.name()
		pr.Steps = []Step{
			{Name: "Back up Box Office", Status: "waiting"},
			{Name: "Back up " + name, Status: "waiting"},
			{Name: "Bring over quality profiles and root folders", Status: "waiting"},
			{Name: "Bring over " + map[string]string{"radarr": "movies", "sonarr": "shows and episodes"}[p.req.App], Status: "waiting"},
			{Name: "Bring over indexers, download clients and settings", Status: "waiting"},
		}
	}
	if !o.BackupBoxOffice {
		pr.Steps[0].Status, pr.Steps[0].Message = "skipped", "not asked for"
	}
	if !o.BackupLegacy {
		pr.Steps[1].Status, pr.Steps[1].Message = "skipped", "not asked for"
	}
	m.mu.Lock()
	if m.running == nil {
		m.running = map[int64]*Progress{}
	}
	m.running[id] = pr
	m.mu.Unlock()
	go m.run(context.WithoutCancel(ctx), p, pr, o)
	return id, nil
}

// update changes a migration's progress under the lock.
func (m *Migrator) update(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn()
}

func (m *Migrator) run(ctx context.Context, p *pending, pr *Progress, o Options) {
	step := func(i int, status, msg string) {
		m.update(func() { pr.Steps[i].Status, pr.Steps[i].Message = status, msg })
	}
	fail := func(i int, err error) {
		m.update(func() {
			pr.Steps[i].Status, pr.Steps[i].Message = "failed", err.Error()
			for j := i + 1; j < len(pr.Steps); j++ {
				if pr.Steps[j].Status == "waiting" {
					pr.Steps[j].Status, pr.Steps[j].Message = "skipped", "stopped after the step above failed"
				}
			}
			pr.Status = "failed"
		})
		m.finish(ctx, pr)
	}
	if o.BackupBoxOffice {
		step(0, "running", "")
		b, err := m.Backups.Make(ctx, backup.ReasonBeforeMigrate)
		if err != nil && b.Name == "" {
			fail(0, fmt.Errorf("didn't migrate, because backing up Box Office failed: %w", err))
			return
		}
		msg := "saved as " + b.Name
		if err != nil {
			msg += "; " + err.Error()
		}
		step(0, "done", msg)
		m.update(func() { pr.Backups = append(pr.Backups, b.Name) })
	}
	if p.req.App == "nzbget" {
		m.runNZBGet(ctx, p, pr, o, step, fail)
		return
	}
	if o.BackupLegacy {
		step(1, "running", "")
		name, data, err := p.l.backup(ctx)
		if err == nil {
			p.backupZip = data
			var path string
			path, err = m.Backups.KeepFile(ctx, name, data)
			if path != "" {
				msg := "saved as " + path
				if err != nil {
					msg += "; " + err.Error()
				}
				err = nil
				step(1, "done", msg)
				m.update(func() { pr.Backups = append(pr.Backups, filepath.Base(path)) })
			}
		}
		if err != nil {
			fail(1, fmt.Errorf("didn't migrate, because the backup of %s failed: %w", p.l.name(), err))
			return
		}
	}
	step(2, "running", "")
	profiles, err := m.importProfiles(ctx, p)
	if err != nil {
		fail(2, err)
		return
	}
	if err := m.importFolders(ctx, p); err != nil {
		fail(2, err)
		return
	}
	step(2, "done", count(len(p.profiles), "quality profile", "quality profiles")+", "+count(len(p.roots), "root folder", "root folders"))
	step(3, "running", "")
	progress := func(done, total int) {
		step(3, "running", fmt.Sprintf("%d of %d", done, total))
	}
	if p.req.App == "radarr" {
		err = m.importMovies(ctx, p, profiles, pr, progress)
	} else {
		err = m.importSeries(ctx, p, profiles, pr, progress)
	}
	if err != nil {
		fail(3, err)
		return
	}
	m.update(func() {
		pr.Steps[3].Status = "done"
		pr.Steps[3].Message = fmt.Sprintf("%d added, %d already here, %d left out", pr.Imported.New, pr.Imported.Already, pr.Imported.Skipped)
	})
	step(4, "running", "")
	msg, err := m.importSettings(ctx, p, pr)
	p.backupZip = nil
	if err != nil {
		fail(4, err)
		return
	}
	m.update(func() {
		pr.Steps[4].Status, pr.Steps[4].Message = "done", msg
		pr.Status = "done"
	})
	m.finish(ctx, pr)
	// Details the old app didn't have, such as episode titles it hadn't
	// fetched, come from TMDB with the next refresh.
	m.Catalog.Jobs.Queue(ctx, catalog.JobRefreshAll, "All titles", "{}")
}

func (m *Migrator) finish(ctx context.Context, pr *Progress) {
	var report []byte
	m.update(func() {
		pr.Finished = time.Now().UTC()
		report, _ = json.Marshal(pr)
	})
	if err := m.Catalog.Store.FinishMigration(ctx, pr.ID, pr.Status, string(report)); err != nil {
		m.Log.Warn("couldn't save a migration's report", "err", err)
	}
	m.Log.Info("migration finished", "app", pr.App, "status", pr.Status)
}

// importProfiles creates or matches each profile, and remembers the old
// app's id for it. It returns the old ids mapped to Box Office's.
func (m *Migrator) importProfiles(ctx context.Context, p *pending) (map[int]int, error) {
	out := map[int]int{}
	locals, err := m.Catalog.Store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, lp := range p.profiles {
		local := 0
		for _, lo := range locals {
			if strings.EqualFold(lo.Name, lp.Name) {
				local = lo.ID
			}
		}
		if local == 0 {
			created, err := m.Catalog.Store.CreateProfile(ctx, mapProfile(lp))
			if err != nil {
				return nil, fmt.Errorf("quality profile %s: %w", lp.Name, err)
			}
			local = created.ID
			locals = append(locals, created)
		}
		out[lp.ID] = local
		if err := m.Catalog.Store.SetLegacyID(ctx, p.req.App, "profile", int64(lp.ID), int64(local)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (m *Migrator) importFolders(ctx context.Context, p *pending) error {
	kind := map[string]string{"radarr": "movie", "sonarr": "series"}[p.req.App]
	for _, r := range p.roots {
		if _, err := m.Catalog.Store.AddRootFolder(ctx, kind, mapPath(p.req.PathMap, r.Path)); err != nil && !errors.Is(err, store.ErrExists) {
			return err
		}
	}
	return nil
}

// ensureFolder adds a title's root folder when the old app listed none for it.
func (m *Migrator) ensureFolder(ctx context.Context, kind, path string) {
	if path == "" {
		return
	}
	if _, err := m.Catalog.Store.AddRootFolder(ctx, kind, path); err != nil && !errors.Is(err, store.ErrExists) {
		m.Log.Warn("couldn't add a root folder", "path", path, "err", err)
	}
}

func rootOf(maps []PathMap, root, path string) string {
	if root != "" {
		return mapPath(maps, root)
	}
	return filepath.Dir(mapPath(maps, path))
}

func (m *Migrator) importMovies(ctx context.Context, p *pending, profiles map[int]int, pr *Progress, progress func(int, int)) error {
	warn := func(s string) { m.update(func() { pr.Warnings = append(pr.Warnings, s) }) }
	for i, mv := range p.movies {
		if i%25 == 0 {
			progress(i, len(p.movies))
		}
		if mv.TMDBID <= 0 {
			m.update(func() { pr.Imported.Skipped++ })
			warn(fmt.Sprintf("%s (%d) has no TMDB id in Radarr, so it was left out", mv.Title, mv.Year))
			continue
		}
		if _, err := m.Catalog.Store.MovieByTMDB(ctx, mv.TMDBID); err == nil {
			m.update(func() { pr.Imported.Already++ })
			continue
		}
		root := rootOf(p.req.PathMap, mv.RootFolderPath, mv.Path)
		m.ensureFolder(ctx, "movie", root)
		pid := profiles[mv.QualityProfileID]
		if pid == 0 {
			pid = firstProfile(profiles)
		}
		// Radarr's own details come across as they are; TMDB fills in
		// the rest with the next refresh.
		info := tmdb.Movie{TMDBID: mv.TMDBID, IMDBID: mv.IMDBID, Title: mv.Title, Year: mv.Year, Overview: mv.Overview, Runtime: mv.Runtime,
			Poster: pickImage(mv.Images, "poster"), Backdrop: pickImage(mv.Images, "fanart"),
			InCinemas: mv.InCinemas, DigitalRelease: mv.DigitalRelease, PhysicalRelease: mv.PhysicalRelease}
		added, err := m.Catalog.AddMovieInfo(ctx, info, catalog.AddOptions{ProfileID: pid, RootFolder: root, Path: mapPath(p.req.PathMap, mv.Path), Monitored: mv.Monitored})
		if errors.Is(err, catalog.ErrExists) {
			m.update(func() { pr.Imported.Already++ })
			continue
		}
		if err != nil {
			m.update(func() { pr.Imported.Skipped++ })
			warn(fmt.Sprintf("%s (%d): %v", mv.Title, mv.Year, err))
			continue
		}
		m.update(func() { pr.Imported.New++ })
		if mv.MovieFile != nil && mv.MovieFile.Path != "" {
			if err := m.addFile(ctx, "movie", added.ID, p.req.PathMap, mv.MovieFile.Path, mv.MovieFile.Size, mv.MovieFile.Quality.Quality.Name); err != nil {
				warn(fmt.Sprintf("%s (%d): its file: %v", mv.Title, mv.Year, err))
			} else {
				m.update(func() { pr.Files++ })
			}
		}
	}
	progress(len(p.movies), len(p.movies))
	return nil
}

func firstProfile(profiles map[int]int) int {
	best := 0
	for _, local := range profiles {
		if best == 0 || local < best {
			best = local
		}
	}
	return best
}

func (m *Migrator) addFile(ctx context.Context, kind string, owner int64, maps []PathMap, path string, size int64, qualityName string) error {
	_, err := m.Catalog.Store.AddFile(ctx, store.File{Kind: kind, OwnerID: owner, Path: mapPath(maps, path), Size: size, Quality: quality.Parse(qualityName).ID})
	if errors.Is(err, store.ErrExists) {
		return errors.New("another title already has this file")
	}
	return err
}

func (m *Migrator) importSeries(ctx context.Context, p *pending, profiles map[int]int, pr *Progress, progress func(int, int)) error {
	warn := func(s string) { m.update(func() { pr.Warnings = append(pr.Warnings, s) }) }
	c := m.Catalog.TMDB(ctx)
	for i, sr := range p.series {
		progress(i, len(p.series))
		tmdbID := sr.TMDBID
		if tmdbID <= 0 {
			if have, err := m.Catalog.Store.SeriesByTVDB(ctx, sr.TVDBID); err == nil {
				tmdbID = have.TMDBID
			} else if c.Key != "" && sr.TVDBID > 0 {
				if info, err := c.ShowByTVDB(ctx, sr.TVDBID); err == nil {
					tmdbID = info.TMDBID
				}
			}
		}
		if tmdbID <= 0 {
			m.update(func() { pr.Imported.Skipped++ })
			warn(fmt.Sprintf("%s (%d): Box Office couldn't find its TMDB id, so it was left out", sr.Title, sr.Year))
			continue
		}
		if _, err := m.Catalog.Store.SeriesByTMDB(ctx, tmdbID); err == nil {
			m.update(func() { pr.Imported.Already++ })
			continue
		}
		root := rootOf(p.req.PathMap, sr.RootFolderPath, sr.Path)
		m.ensureFolder(ctx, "series", root)
		pid := profiles[sr.QualityProfileID]
		if pid == 0 {
			pid = firstProfile(profiles)
		}
		var eps []lEpisode
		if err := p.l.get(ctx, "/episode", map[string][]string{"seriesId": {strconv.Itoa(sr.ID)}}, &eps); err != nil {
			return fmt.Errorf("reading %s's episodes: %w", sr.Title, err)
		}
		var files []lEpisodeFile
		if err := p.l.get(ctx, "/episodefile", map[string][]string{"seriesId": {strconv.Itoa(sr.ID)}}, &files); err != nil {
			return fmt.Errorf("reading %s's files: %w", sr.Title, err)
		}
		info := tmdb.Show{TMDBID: tmdbID, TVDBID: sr.TVDBID, IMDBID: sr.IMDBID, Title: sr.Title, Year: sr.Year, Overview: sr.Overview,
			Poster: pickImage(sr.Images, "poster"), Backdrop: pickImage(sr.Images, "fanart"), Status: sr.Status, Network: sr.Network, Runtime: sr.Runtime}
		seasons := map[int]bool{}
		for _, se := range sr.Seasons {
			info.Seasons = append(info.Seasons, tmdb.SeasonInfo{Number: se.SeasonNumber})
			seasons[se.SeasonNumber] = se.Monitored
		}
		t := store.Series{TMDBID: info.TMDBID, TVDBID: info.TVDBID, IMDBID: info.IMDBID, Title: info.Title, Year: info.Year, Overview: info.Overview,
			Poster: info.Poster, Backdrop: info.Backdrop, Status: info.Status, Network: info.Network, Runtime: info.Runtime,
			Monitored: sr.Monitored, ProfileID: pid, RootFolder: root, Path: mapPath(p.req.PathMap, sr.Path)}
		for _, se := range sr.Seasons {
			t.Seasons = append(t.Seasons, store.Season{Number: se.SeasonNumber, Monitored: se.Monitored})
		}
		added, err := m.Catalog.Store.AddSeries(ctx, t)
		if errors.Is(err, store.ErrExists) {
			m.update(func() { pr.Imported.Already++ })
			continue
		}
		if err != nil {
			m.update(func() { pr.Imported.Skipped++ })
			warn(fmt.Sprintf("%s (%d): %v", sr.Title, sr.Year, err))
			continue
		}
		byFile := map[int]lEpisodeFile{}
		for _, f := range files {
			byFile[f.ID] = f
		}
		stored := 0
		for _, e := range eps {
			if err := m.Catalog.Store.UpsertEpisode(ctx, store.Episode{SeriesID: added.ID, Season: e.SeasonNumber, Number: e.EpisodeNumber,
				Title: e.Title, Overview: e.Overview, AirDate: e.AirDateUTC, Monitored: e.Monitored}); err != nil {
				return err
			}
			f, ok := byFile[e.EpisodeFileID]
			if !ok || e.EpisodeFileID == 0 {
				continue
			}
			ep, err := m.episode(ctx, added.ID, e.SeasonNumber, e.EpisodeNumber)
			if err != nil {
				return err
			}
			if err := m.addFile(ctx, "episode", ep, p.req.PathMap, f.Path, f.Size, f.Quality.Quality.Name); err != nil {
				// One file holding two episodes is recorded for the first.
				continue
			}
			stored++
		}
		m.update(func() { pr.Imported.New++; pr.Files += stored })
	}
	progress(len(p.series), len(p.series))
	return nil
}

func (m *Migrator) episode(ctx context.Context, seriesID int64, season, number int) (int64, error) {
	eps, err := m.Catalog.Store.Episodes(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	for _, e := range eps {
		if e.Season == season && e.Number == number {
			return e.ID, nil
		}
	}
	return 0, store.ErrNotFound
}

// Progress reports a migration: a running one as it goes, an earlier one
// from its saved report.
func (m *Migrator) Progress(ctx context.Context, id int64) (Progress, error) {
	m.mu.Lock()
	if pr, ok := m.running[id]; ok {
		cp := *pr
		cp.Steps = slices.Clone(pr.Steps)
		cp.Warnings = slices.Clone(pr.Warnings)
		cp.Backups = slices.Clone(pr.Backups)
		m.mu.Unlock()
		return cp, nil
	}
	m.mu.Unlock()
	list, err := m.Catalog.Store.Migrations(ctx)
	if err != nil {
		return Progress{}, err
	}
	for _, mg := range list {
		if mg.ID == id {
			var pr Progress
			if err := json.Unmarshal([]byte(mg.Report), &pr); err != nil || pr.ID == 0 {
				return Progress{ID: mg.ID, App: mg.App, Status: mg.Status, Started: mg.CreatedAt, Finished: mg.FinishedAt}, nil
			}
			return pr, nil
		}
	}
	return Progress{}, store.ErrNotFound
}

// List lists every migration, newest first.
func (m *Migrator) List(ctx context.Context) ([]Progress, error) {
	list, err := m.Catalog.Store.Migrations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Progress, 0, len(list))
	for _, mg := range list {
		pr, err := m.Progress(ctx, mg.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, nil
}

// setMatinee records that Matinee was switched over after a migration.
func (m *Migrator) setMatinee(ctx context.Context, id int64, note string) {
	pr, err := m.Progress(ctx, id)
	if err != nil {
		return
	}
	pr.Matinee = note
	m.update(func() {
		if r, ok := m.running[id]; ok {
			r.Matinee = note
		}
	})
	report, _ := json.Marshal(pr)
	m.Catalog.Store.FinishMigration(ctx, id, pr.Status, string(report))
}
