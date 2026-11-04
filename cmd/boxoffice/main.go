// Command boxoffice runs Box Office, which finds and keeps the movies and
// shows Matinee plays.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yeixio/matinee/boxoffice/web"
	"github.com/yeixio/matinee/internal/boxoffice/api"
	"github.com/yeixio/matinee/internal/boxoffice/backup"
	"github.com/yeixio/matinee/internal/boxoffice/catalog"
	"github.com/yeixio/matinee/internal/boxoffice/config"
	"github.com/yeixio/matinee/internal/boxoffice/jobs"
	"github.com/yeixio/matinee/internal/boxoffice/migrate"
	"github.com/yeixio/matinee/internal/boxoffice/store"
	"github.com/yeixio/matinee/internal/kube"
	"github.com/yeixio/matinee/internal/version"
)

func main() {
	cmd := run
	if len(os.Args) > 1 && os.Args[1] == "init-db-secret" {
		cmd = initDBSecret
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
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	log := slog.New(handler)

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
	backups := &backup.Manager{Store: db, Dir: cfg.BackupDir(), Log: log}
	srv := &api.Server{
		Store: db, Catalog: cat, Backups: backups, Migrator: &migrate.Migrator{Catalog: cat, Backups: backups, Log: log}, Log: log,
		Version: version.Version, Database: dbKind, DataDir: cfg.DataDir, EnvAPIKey: cfg.APIKey, Web: web.FS(),
	}
	if err := srv.EnsureAPIKey(ctx); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){runner.Run, backups.Run, func(ctx context.Context) { cat.RefreshEvery(ctx, cfg.Refresh) }} {
		wg.Add(1)
		go func() { defer wg.Done(); loop(ctx) }()
	}

	hs := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("Box Office is running", "addr", cfg.Addr, "database", dbKind, "version", version.Version)
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
