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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/announce"
	"github.com/yeixio/boxoffice/internal/api"
	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/config"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/kube"
	"github.com/yeixio/boxoffice/internal/migrate"
	"github.com/yeixio/boxoffice/internal/monitor"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/notify"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/version"
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
	if err := cmd(); err != nil {
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
	mon := &api.Monitoring{Monitor: monitor.NewMonitor(logs), Level: level, ForcedDebug: cfg.Debug}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("make the data folder: %w", err)
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

	runner := jobs.New(db, log)
	cat := &catalog.Service{Store: db, Jobs: runner, Log: log, EnvTMDBKey: cfg.TMDBKey}
	cat.Register()
	mb := musicbrainz.New(version.Version)
	if cfg.MusicBrainz != "" {
		mb.Base = cfg.MusicBrainz
	}
	backups := &backup.Manager{Store: db, Dir: cfg.BackupDir(), Log: log}
	downloads := &downloader.Engine{Store: db, Dir: cfg.Downloads, Log: log, Importer: &downloader.LibraryImporter{Store: db},
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
		mon.DownloadFailures.Add(1)
		if d.Source != "search" || d.Kind == "" {
			return
		}
		db.Block(ctx, store.Blocked{Kind: d.Kind, TitleID: d.TitleID, SourceTitle: d.Name, Quality: d.Quality, Protocol: "usenet",
			Indexer: d.Indexer, Reason: why, At: time.Now()})
		if d.Kind == "movie" {
			cat.SearchMovies(ctx, d.TitleID)
		} else if len(d.EpisodeIDs) > 0 {
			cat.SearchEpisodes(ctx, d.EpisodeIDs...)
		}
	}
	// What happens to downloads is kept for Matinee to pass on to people.
	notes := &notify.Recorder{Store: db, Log: log}
	downloads.Notify = notes.Download
	srv := &api.Server{
		Store: db, Catalog: cat, Music: &music.Service{Store: db, MusicBrainz: mb}, Backups: backups, Migrator: &migrate.Migrator{Catalog: cat, Backups: backups, Log: log, DataDir: cfg.DataDir}, Log: log,
		Downloads: downloads, Search: finder,
		Version: version.Version, Database: dbKind, DataDir: cfg.DataDir, EnvAPIKey: cfg.APIKey, Web: web.FS(),
		Monitoring: mon,
	}
	alerter := srv.Alerts()
	// Admin alerts reach Matinee's admins too, with download news.
	alerter.Forward = notes.Alert
	backups.Failed = alerter.BackupFailed
	if err := srv.EnsureAPIKey(ctx); err != nil {
		return err
	}
	srv.RestoreDebugLogging(ctx)

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){runner.Run, backups.Run, downloads.Run, mon.Monitor.Run, alerter.Run,
		func(ctx context.Context) { cat.RefreshEvery(ctx, cfg.Refresh) }, func(ctx context.Context) { finder.AutoSearchEvery(ctx, runner) },
		finder.RSSEvery, finder.PruneEvery, func(ctx context.Context) { api.RecycleSweepEvery(ctx, db, log) }} {
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
	log.Info("stopping")
	shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := hs.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Warn("stopping the web server", "err", err)
	}
	wg.Wait()
	// A clean stop, so the next start isn't taken for a crash.
	alerter.Stopped(context.Background())
	return nil
}

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
