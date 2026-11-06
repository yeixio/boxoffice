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
	"strings"
	"time"
)

// DefaultTMDBKey is the project's own TMDB key, linked into official images with
// -ldflags "-X github.com/yeixio/boxoffice/internal/config.DefaultTMDBKey=...",
// so they look titles up with no key to paste. Builds from source leave it empty.
var DefaultTMDBKey = ""

// Config is how Box Office is set up on this server.
type Config struct {
	Addr        string        // where it listens; BOXOFFICE_ADDR
	DataDir     string        // its database, backups and imports; BOXOFFICE_DATA_DIR
	DatabaseURL string        // a PostgreSQL URL; empty uses SQLite in DataDir; BOXOFFICE_DATABASE_URL
	Downloads   string        // where downloads go while they're fetched and unpacked; BOXOFFICE_DOWNLOADS_DIR
	TMDBKey     string        // used when none is saved in Settings; BOXOFFICE_TMDB_KEY, else DefaultTMDBKey
	APIKey      string        // fixes the API key, such as from a Kubernetes Secret; BOXOFFICE_API_KEY
	LogFormat   string        // text or json; BOXOFFICE_LOG_FORMAT
	Debug       bool          // BOXOFFICE_DEBUG
	Refresh     time.Duration // how often titles are refreshed from TMDB; BOXOFFICE_REFRESH_EVERY
	// MusicBrainz is a MusicBrainz mirror's web service, such as
	// http://musicbrainz.local:5000/ws/2, in place of musicbrainz.org;
	// BOXOFFICE_MUSICBRAINZ_URL.
	MusicBrainz string
	// OpenLibrary is another Open Library to look books up on, in place
	// of openlibrary.org; BOXOFFICE_OPENLIBRARY_URL.
	OpenLibrary string
	// EbookConvert is Calibre's ebook-convert, to turn ebooks an e-reader
	// won't take by email into EPUB; BOXOFFICE_EBOOK_CONVERT. Empty looks
	// for it on the PATH.
	EbookConvert string
	// Announce tells the home network Box Office is here, so Matinee can
	// find it; BOXOFFICE_ANNOUNCE=false turns it off.
	Announce bool
}

// Load reads the config.
func Load(args []string) (Config, error) {
	return load(args, os.Getenv)
}

func load(args []string, env func(string) string) (Config, error) {
	c := Config{
		Addr:         cmp.Or(env("BOXOFFICE_ADDR"), ":7272"),
		DataDir:      cmp.Or(env("BOXOFFICE_DATA_DIR"), defaultDataDir()),
		DatabaseURL:  env("BOXOFFICE_DATABASE_URL"),
		Downloads:    env("BOXOFFICE_DOWNLOADS_DIR"),
		TMDBKey:      cmp.Or(env("BOXOFFICE_TMDB_KEY"), DefaultTMDBKey),
		APIKey:       env("BOXOFFICE_API_KEY"),
		LogFormat:    cmp.Or(env("BOXOFFICE_LOG_FORMAT"), "text"),
		Debug:        env("BOXOFFICE_DEBUG") != "",
		Announce:     !off(env("BOXOFFICE_ANNOUNCE")),
		Refresh:      12 * time.Hour,
		MusicBrainz:  strings.TrimRight(env("BOXOFFICE_MUSICBRAINZ_URL"), "/"),
		OpenLibrary:  strings.TrimRight(env("BOXOFFICE_OPENLIBRARY_URL"), "/"),
		EbookConvert: env("BOXOFFICE_EBOOK_CONVERT"),
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
	fs.BoolVar(&c.Announce, "announce", c.Announce, "tell the home network Box Office is here, so Matinee can find it")
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

// off reads a setting that's on unless it says otherwise.
func off(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
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
