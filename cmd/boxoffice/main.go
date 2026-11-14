// Command boxoffice runs Box Office, which finds and keeps the movies and
// shows Matinee plays.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/announce"
	"github.com/yeixio/boxoffice/internal/api"
	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/config"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/ereader"
	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/importer"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/kube"
	"github.com/yeixio/boxoffice/internal/library"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/migrate"
	"github.com/yeixio/boxoffice/internal/monitor"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/notify"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/problems"
	"github.com/yeixio/boxoffice/internal/qualitycheck"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/version"
	"github.com/yeixio/boxoffice/internal/wikidata"
	"github.com/yeixio/boxoffice/web"
)

func main() {
	cmd := run
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init-db-secret":
			cmd = initDBSecret
		case "healthcheck":
			cmd = healthcheck
		}
	}
	err := cmd()
	if errors.Is(err, errRestart) {
		err = restart()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "boxoffice:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	// The level can change while Box Office runs: the Health page switches
	// debug logging on and off. BOXOFFICE_DEBUG keeps it on.
	level := new(slog.LevelVar)
	if cfg.Debug {
		level.Set(slog.LevelDebug)
	}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	// The Health page shows the latest lines and the problems among them.
	logs := monitor.NewLogs(0)
	log := slog.New(logs.Handler(handler))
	// Packages that use the standard log, such as net/http when a page
	// panics, log here too.
	slog.SetDefault(log)
	mon := &api.Monitoring{Monitor: monitor.NewMonitor(logs), Level: level, ForcedDebug: cfg.Debug}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("make the data folder: %w", err)
	}
	// The log and a crash are written to the data folder too, so the next
	// start can show them: a pod's own output goes with the pod.
	prev, err := monitor.OpenJournal(cfg.LogDir(), logs)
	if err != nil {
		log.Warn("not keeping the log on disk; the Log page will only have this run", "dir", cfg.LogDir(), "err", err)
	}
	defer logs.CloseJournal()
	mon.Monitor.Previous = prev
	if prev.Crashed() {
		log.Warn("Box Office crashed last time; Health → Log → Previous run has what it wrote", "crash", prev.CrashSummary())
	}
	db, err := store.Open(cfg.Database())
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer db.Close()
	dbKind := "sqlite"
	if store.IsPostgres(cfg.Database()) {
		dbKind = "postgres"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The Restart button in Settings stops Box Office the same way a
	// signal does, then starts it again (see restart).
	ctx, restartNow := context.WithCancelCause(ctx)
	defer restartNow(nil)

	runner := jobs.New(db, log)
	cat := &catalog.Service{Store: db, Jobs: runner, Log: log, EnvTMDBKey: cfg.TMDBKey,
		TMDBKeyBuiltIn: cfg.TMDBKey != "" && cfg.TMDBKey == config.DefaultTMDBKey}
	cat.Register()
	mb := musicbrainz.New(version.Version)
	if cfg.MusicBrainz != "" {
		mb.Base = cfg.MusicBrainz
	}
	backups := &backup.Manager{Store: db, Dir: cfg.BackupDir(), Log: log}
	libImporter := &downloader.LibraryImporter{Store: db}
	downloads := &downloader.Engine{Store: db, Dir: cfg.Downloads, NZBDir: filepath.Join(cfg.DataDir, "nzb"), Log: log, Importer: libImporter,
		Libraries: func(ctx context.Context) ([]string, error) {
			roots, err := db.RootFolders(ctx, "")
			var out []string
			for _, r := range roots {
				out = append(out, r.Path)
			}
			return out, err
		}}
	finder := &search.Service{Store: db, Engine: downloads, Client: &search.Client{Spacing: search.DefaultSpacing}, Log: log}
	finder.Register(runner)
	// A download that fails is blocked, and something else looked for.
	downloads.Failed = func(ctx context.Context, d store.Download, why string) {
		// Releases gone from the news servers are common and need nothing
		// done, so they don't count towards the failed downloads alert.
		if !d.Missing {
			mon.DownloadFailures.Add(1)
		}
		if d.Source != "search" || d.Kind == "" {
			return
		}
		db.Block(ctx, store.Blocked{Kind: d.Kind, TitleID: d.TitleID, SourceTitle: d.Name, Quality: d.Quality, Protocol: "usenet",
			Indexer: d.Indexer, Reason: why, At: time.Now()})
		switch {
		case d.Kind == "movie":
			cat.SearchMovies(ctx, d.TitleID)
		case store.IsBookKind(d.Kind):
			runner.Queue(ctx, search.JobSearchBook, d.Name, search.BookJob(d.TitleID, d.Kind))
		case d.Kind == store.AlbumKind:
			runner.Queue(ctx, search.JobSearchAlbum, d.Name, search.AlbumJob(d.TitleID))
		case d.Kind == store.GameKind:
			runner.Queue(ctx, search.JobSearchGame, d.Name, search.GameJob(d.TitleID))
		case len(d.EpisodeIDs) > 0:
			cat.SearchEpisodes(ctx, d.EpisodeIDs...)
		}
	}
	// What happens to downloads is kept for Matinee to pass on to people.
	notes := &notify.Recorder{Store: db, Log: log}
	downloads.Notify = notes.Download
	tunes := &music.Service{Store: db, MusicBrainz: mb, Jobs: runner}
	ol := openlibrary.New(version.Version)
	if cfg.OpenLibrary != "" {
		ol.Base = cfg.OpenLibrary
	}
	shelf := &books.Service{Store: db, OpenLibrary: ol, Jobs: runner, Log: log}
	if !strings.EqualFold(cfg.Wikidata, "off") {
		shelf.Wikidata = wikidata.New(version.Version)
		if cfg.Wikidata != "" {
			shelf.Wikidata.Base = cfg.Wikidata
		}
	}
	// Games are checked when a games root folder is added, on Check
	// again, and weekly.
	gameShelf := &games.Service{Store: db, Log: log, Dir: filepath.Join(cfg.DataDir, "games"),
		MAMEReleases: cfg.MAMEReleases, LibretroDB: cfg.LibretroDB, TitleDB: cfg.TitleDB, Art: cfg.GameArt, UserAgent: "BoxOffice/" + version.Version,
		Search: search.QueueGame(runner)}
	// Games wanted are downloaded, checked against their system's list and
	// copied into its folder.
	libImporter.Games = gameShelf
	// The problem files look runs daily, and on Look again.
	finds := &problems.Service{Store: db, Log: log}
	// The quality check looks inside new files every few minutes.
	checker := &qualitycheck.Service{Store: db, Log: log}
	scanner := &libscan.Service{Store: db, Catalog: cat, Music: tunes, Books: shelf, Log: log}
	// Files brought in by hand wait in the downloads folder's imports
	// folder until they're filed in the library. A copy into it that a
	// restart cut off carries on.
	imports := &importer.Service{Store: db, Downloads: downloads, Scan: scanner, Catalog: cat, Music: tunes, Books: shelf, Games: gameShelf, Log: log}
	go imports.Resume()
	// An NZB added by hand for no title is filed through it once it's
	// downloaded.
	downloads.FileAway = imports.Adopt
	srv := &api.Server{
		Store: db, Catalog: cat, Music: tunes, LibScan: scanner, Problems: finds, QualityCheck: checker, Backups: backups, Migrator: &migrate.Migrator{Catalog: cat, Books: shelf, Backups: backups, Log: log, DataDir: cfg.DataDir}, Log: log,
		Books:     shelf,
		Games:     gameShelf,
		Imports:   imports,
		Portal:    &portal.Service{Store: db, Catalog: cat, Notify: notes, Log: log},
		Downloads: downloads, Search: finder,
		Version: version.Version, Database: dbKind, DataDir: cfg.DataDir, EnvAPIKey: cfg.APIKey, Web: web.FS(),
		Monitoring: mon,
		Restart:    func() { restartNow(errRestart) },
	}
	alerter := srv.Alerts()
	alerter.Crash = prev.CrashSummary()
	// Ebooks go to the e-readers set to get them as they download.
	readers := &ereader.Service{Store: db, Log: log, Convert: cfg.EbookConvert,
		AlertMail: func(ctx context.Context) (alerts.Email, error) {
			c, err := alerter.Config(ctx)
			return c.Email, err
		}}
	srv.Ereaders = readers
	downloads.Imported = func(ctx context.Context, d store.Download) {
		if d.Kind == store.Ebook {
			go readers.Imported(ctx, d)
		}
	}
	// Admin alerts reach Matinee's admins too, with download news.
	alerter.Forward = notes.Alert
	finds.Alert = alerter.ProblemFiles
	finds.Deleted = func(ctx context.Context, n int, freed int64) {
		db.RecordJob(ctx, api.JobDeleteFiles, "Problem files", "done", fmt.Sprintf("Deleted the originals of %d fixes, freeing %s.", n, library.Bytes(freed)))
	}
	backups.Failed = alerter.BackupFailed
	if err := srv.EnsureAPIKey(ctx); err != nil {
		return err
	}
	srv.RestoreDebugLogging(ctx)

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){runner.Run, backups.Run, downloads.Run, mon.Monitor.Run, alerter.Run,
		func(ctx context.Context) { cat.RefreshEvery(ctx, cfg.Refresh) }, func(ctx context.Context) { finder.AutoSearchEvery(ctx, runner) },
		finder.RSSEvery, finder.HeldEvery, finder.PruneEvery, shelf.DetailsEvery, finds.Run, checker.Run, gameShelf.Run, func(ctx context.Context) { api.RecycleSweepEvery(ctx, db, log) },
		func(ctx context.Context) { retryImports(ctx, downloads, log) }} {
		wg.Add(1)
		go func() { defer wg.Done(); loop(ctx) }()
	}

	// Matinee looks for Box Office on the home network to fill in its address.
	if cfg.Announce {
		if port, err := announce.Port(cfg.Addr); err != nil {
			log.Warn("not announcing on the local network", "addr", cfg.Addr, "err", err)
		} else if id, err := db.InstanceID(ctx); err != nil {
			log.Warn("not announcing on the local network", "err", err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := announce.Run(ctx, announce.Announcement{Port: port, ID: id, Version: version.Version}, log); err != nil {
					log.Warn("not announcing on the local network", "err", err)
				}
			}()
		}
	}

	hs := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("Box Office is running", "addr", cfg.Addr, "database", dbKind, "version", version.Version,
		"data", cfg.DataDir, "downloads", cfg.Downloads, "debug", level.Level() == slog.LevelDebug)
	select {
	case err := <-errc:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
	}
	restarting := errors.Is(context.Cause(ctx), errRestart)
	if restarting {
		log.Info("stopping to restart")
	} else {
		log.Info("stopping")
	}
	// Something asked Box Office to stop (Kubernetes, Docker, Ctrl-C, or
	// the Restart button), so
	// the next start is a planned restart, not a crash. Note that now,
	// before waiting on work that may be stuck on a network share: a slow
	// stop, or a kill when the grace period runs out, is still planned.
	mark, unmark := context.WithTimeout(context.Background(), 5*time.Second)
	alerter.Stopped(mark)
	unmark()
	// Everything has stopWait to finish. Work stuck on a network share
	// that stopped answering may never come back, and waiting for it
	// would hold up the stop until Kubernetes or Docker gave up on it.
	shut, cancel := context.WithTimeout(context.Background(), stopWait)
	defer cancel()
	stopped := make(chan struct{})
	go func() { wg.Wait(); close(stopped) }()
	if err := hs.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Warn("stopping the web server", "err", err)
	}
	select {
	case <-stopped:
	case <-shut.Done():
		log.Warn("stopping without waiting for work that's stuck; a network share may have stopped answering")
	}
	log.Info("stopped")
	if restarting {
		return errRestart
	}
	return nil
}

// stopWait is how long Box Office takes to stop, at most: under the 30
// seconds Kubernetes and Docker give before killing it.
const stopWait = 20 * time.Second

// initDBSecret makes the bundled PostgreSQL's credentials in Kubernetes,
// so a fresh Helm install needs no hand-made Secret.
func initDBSecret() error {
	fs := flag.NewFlagSet("init-db-secret", flag.ContinueOnError)
	s := kube.DatabaseSecret{App: "boxoffice"}
	fs.StringVar(&s.Name, "name", "boxoffice-db", "secret to create")
	fs.StringVar(&s.User, "user", "boxoffice", "database user")
	fs.StringVar(&s.Database, "database", "boxoffice", "database name")
	fs.StringVar(&s.Host, "host", "boxoffice-postgresql", "PostgreSQL service")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	client, ns, err := kube.InCluster()
	if err != nil {
		return err
	}
	s.Namespace = ns
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	created, err := client.EnsureDatabaseSecret(ctx, s)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("created database credentials in secret", s.Name)
	} else {
		fmt.Println("secret", s.Name, "already exists; keeping it")
	}
	return nil
}

// healthcheck asks the running server whether it's up, for Docker's
// HEALTHCHECK in an image that has no shell or wget.
func healthcheck() error {
	addr := cmp.Or(os.Getenv("BOXOFFICE_ADDR"), ":7272")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz answered %d", res.StatusCode)
	}
	return nil
}

// retryImports gives finished downloads that couldn't go into the library
// one more go after an update, a minute after starting so network shares
// are up.
func retryImports(ctx context.Context, downloads *downloader.Engine, log *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	n, err := downloads.RetryImports(ctx, version.Version)
	if err != nil {
		log.Warn("couldn't retry moving waiting downloads into the library", "err", err)
	} else if n > 0 {
		log.Info("moved waiting downloads into the library after an update", "downloads", n)
	}
}
