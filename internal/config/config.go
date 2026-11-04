// Package config reads Box Office's settings from flags and the environment.
// Flags win over the environment, which wins over the defaults.
package config

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is how Box Office is set up on this server.
type Config struct {
	Addr        string        // where it listens; BOXOFFICE_ADDR
	DataDir     string        // its database, backups and imports; BOXOFFICE_DATA_DIR
	DatabaseURL string        // a PostgreSQL URL; empty uses SQLite in DataDir; BOXOFFICE_DATABASE_URL
	Downloads   string        // where downloads go while they're fetched and unpacked; BOXOFFICE_DOWNLOADS_DIR
	TMDBKey     string        // used when none is saved in Settings; BOXOFFICE_TMDB_KEY
	APIKey      string        // fixes the API key, such as from a Kubernetes Secret; BOXOFFICE_API_KEY
	LogFormat   string        // text or json; BOXOFFICE_LOG_FORMAT
	Debug       bool          // BOXOFFICE_DEBUG
	Refresh     time.Duration // how often titles are refreshed from TMDB; BOXOFFICE_REFRESH_EVERY
}

// Load reads the config.
func Load(args []string) (Config, error) {
	return load(args, os.Getenv)
}

func load(args []string, env func(string) string) (Config, error) {
	c := Config{
		Addr:        cmp.Or(env("BOXOFFICE_ADDR"), ":7272"),
		DataDir:     cmp.Or(env("BOXOFFICE_DATA_DIR"), defaultDataDir()),
		DatabaseURL: env("BOXOFFICE_DATABASE_URL"),
		Downloads:   env("BOXOFFICE_DOWNLOADS_DIR"),
		TMDBKey:     env("BOXOFFICE_TMDB_KEY"),
		APIKey:      env("BOXOFFICE_API_KEY"),
		LogFormat:   cmp.Or(env("BOXOFFICE_LOG_FORMAT"), "text"),
		Debug:       env("BOXOFFICE_DEBUG") != "",
		Refresh:     12 * time.Hour,
	}
	if v := env("BOXOFFICE_REFRESH_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("BOXOFFICE_REFRESH_EVERY: %w", err)
		}
		c.Refresh = d
	}
	fs := flag.NewFlagSet("boxoffice", flag.ContinueOnError)
	fs.StringVar(&c.Addr, "addr", c.Addr, "address to listen on")
	fs.StringVar(&c.DataDir, "data", c.DataDir, "folder for the database, backups and imports")
	fs.StringVar(&c.DatabaseURL, "database-url", c.DatabaseURL, "PostgreSQL URL (default: SQLite in the data folder)")
	fs.StringVar(&c.Downloads, "downloads", c.Downloads, "folder for downloads in progress (default: downloads in the data folder)")
	fs.StringVar(&c.LogFormat, "log-format", c.LogFormat, "text or json")
	fs.BoolVar(&c.Debug, "debug", c.Debug, "log more")
	fs.DurationVar(&c.Refresh, "refresh-every", c.Refresh, "how often titles are refreshed from TMDB")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.Downloads == "" {
		c.Downloads = filepath.Join(c.DataDir, "downloads")
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, errors.New("the log format is text or json")
	}
	if c.Refresh < time.Hour {
		return c, errors.New("refresh titles at most once an hour")
	}
	return c, nil
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "boxoffice")
	}
	return "data"
}

// Database is the database to open: the PostgreSQL URL, or the SQLite file.
func (c Config) Database() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return filepath.Join(c.DataDir, "boxoffice.db")
}

// BackupDir is where backups are kept on this server.
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }
