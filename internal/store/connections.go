package store

import (
	"context"
	"encoding/json"
	"time"
)

// Indexer is somewhere releases are searched for.
type Indexer struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Protocol      string          `json:"protocol"`
	Kind          string          `json:"kind"`
	URL           string          `json:"url"`
	APIPath       string          `json:"apiPath,omitempty"`
	APIKey        string          `json:"-"`
	HasAPIKey     bool            `json:"hasApiKey"`
	Categories    []int           `json:"categories"`
	EnableRSS     bool            `json:"enableRss"`
	EnableSearch  bool            `json:"enableSearch"`
	Priority      int             `json:"priority"`
	Settings      json.RawMessage `json:"settings"`
	Source        string          `json:"source,omitempty"`
	MissingSecret bool            `json:"missingSecret"`
}

// UpsertIndexer adds an indexer, or updates the one with the same address
// and key.
func (s *Store) UpsertIndexer(ctx context.Context, ix Indexer) error {
	cats, _ := json.Marshal(ix.Categories)
	if ix.Settings == nil {
		ix.Settings = json.RawMessage("{}")
	}
	_, err := s.exec(ctx, `INSERT INTO indexers (name, protocol, kind, url, api_path, api_key, categories, enable_rss, enable_search, priority, settings, source, missing_secret)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (protocol, url, api_key) DO UPDATE SET name = excluded.name, kind = excluded.kind, api_path = excluded.api_path,
		categories = excluded.categories, enable_rss = excluded.enable_rss, enable_search = excluded.enable_search, priority = excluded.priority,
		settings = excluded.settings, source = excluded.source, missing_secret = excluded.missing_secret`,
		ix.Name, ix.Protocol, ix.Kind, ix.URL, ix.APIPath, ix.APIKey, string(cats), b2i(ix.EnableRSS), b2i(ix.EnableSearch), ix.Priority,
		string(ix.Settings), ix.Source, b2i(ix.MissingSecret))
	return err
}

func (s *Store) Indexers(ctx context.Context) ([]Indexer, error) {
	rows, err := s.query(ctx, `SELECT id, name, protocol, kind, url, api_path, api_key, categories, enable_rss, enable_search, priority, settings, source, missing_secret
		FROM indexers ORDER BY priority, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Indexer{}
	for rows.Next() {
		var ix Indexer
		var cats, settings string
		var rss, search, missing int
		if err := rows.Scan(&ix.ID, &ix.Name, &ix.Protocol, &ix.Kind, &ix.URL, &ix.APIPath, &ix.APIKey, &cats, &rss, &search, &ix.Priority, &settings, &ix.Source, &missing); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(cats), &ix.Categories)
		ix.Settings = json.RawMessage(settings)
		ix.EnableRSS, ix.EnableSearch, ix.MissingSecret, ix.HasAPIKey = rss == 1, search == 1, missing == 1, ix.APIKey != ""
		out = append(out, ix)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIndexer(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM indexers WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// DownloadClient is a download client another app used.
type DownloadClient struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Protocol      string          `json:"protocol"`
	Host          string          `json:"host"`
	Port          int             `json:"port"`
	UseTLS        bool            `json:"useTls"`
	URLBase       string          `json:"urlBase,omitempty"`
	Username      string          `json:"username,omitempty"`
	Password      string          `json:"-"`
	HasPassword   bool            `json:"hasPassword"`
	Category      string          `json:"category,omitempty"`
	Priority      int             `json:"priority"`
	Enabled       bool            `json:"enabled"`
	Settings      json.RawMessage `json:"settings"`
	Source        string          `json:"source,omitempty"`
	MissingSecret bool            `json:"missingSecret"`
}

func (s *Store) UpsertDownloadClient(ctx context.Context, c DownloadClient) error {
	if c.Settings == nil {
		c.Settings = json.RawMessage("{}")
	}
	_, err := s.exec(ctx, `INSERT INTO download_clients (name, kind, protocol, host, port, use_tls, url_base, username, password, category, priority, enabled, settings, source, missing_secret)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, host, port, category) DO UPDATE SET name = excluded.name, protocol = excluded.protocol, use_tls = excluded.use_tls,
		url_base = excluded.url_base, username = excluded.username, password = excluded.password, priority = excluded.priority, enabled = excluded.enabled,
		settings = excluded.settings, source = excluded.source, missing_secret = excluded.missing_secret`,
		c.Name, c.Kind, c.Protocol, c.Host, c.Port, b2i(c.UseTLS), c.URLBase, c.Username, c.Password, c.Category, c.Priority, b2i(c.Enabled),
		string(c.Settings), c.Source, b2i(c.MissingSecret))
	return err
}

func (s *Store) DownloadClients(ctx context.Context) ([]DownloadClient, error) {
	rows, err := s.query(ctx, `SELECT id, name, kind, protocol, host, port, use_tls, url_base, username, password, category, priority, enabled, settings, source, missing_secret
		FROM download_clients ORDER BY priority, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DownloadClient{}
	for rows.Next() {
		var c DownloadClient
		var tls, enabled, missing int
		var settings string
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Protocol, &c.Host, &c.Port, &tls, &c.URLBase, &c.Username, &c.Password, &c.Category, &c.Priority, &enabled, &settings, &c.Source, &missing); err != nil {
			return nil, err
		}
		c.Settings = json.RawMessage(settings)
		c.UseTLS, c.Enabled, c.MissingSecret, c.HasPassword = tls == 1, enabled == 1, missing == 1, c.Password != ""
		out = append(out, c)
	}
	return out, rows.Err()
}

// UsenetServer is a news server for the downloader.
type UsenetServer struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Host        string          `json:"host"`
	Port        int             `json:"port"`
	UseTLS      bool            `json:"useTls"`
	Username    string          `json:"username,omitempty"`
	Password    string          `json:"-"`
	HasPassword bool            `json:"hasPassword"`
	Connections int             `json:"connections"`
	Level       int             `json:"level"`
	Optional    bool            `json:"optional"`
	Retention   int             `json:"retention"`
	Enabled     bool            `json:"enabled"`
	Settings    json.RawMessage `json:"settings"`
	Source      string          `json:"source,omitempty"`
}

func (s *Store) UpsertUsenetServer(ctx context.Context, u UsenetServer) error {
	if u.Settings == nil {
		u.Settings = json.RawMessage("{}")
	}
	_, err := s.exec(ctx, `INSERT INTO usenet_servers (name, host, port, use_tls, username, password, connections, level, optional, retention, enabled, settings, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (host, port, username) DO UPDATE SET name = excluded.name, use_tls = excluded.use_tls, password = excluded.password,
		connections = excluded.connections, level = excluded.level, optional = excluded.optional, retention = excluded.retention,
		enabled = excluded.enabled, settings = excluded.settings, source = excluded.source`,
		u.Name, u.Host, u.Port, b2i(u.UseTLS), u.Username, u.Password, u.Connections, u.Level, b2i(u.Optional), u.Retention, b2i(u.Enabled), string(u.Settings), u.Source)
	return err
}

func (s *Store) UsenetServers(ctx context.Context) ([]UsenetServer, error) {
	rows, err := s.query(ctx, `SELECT id, name, host, port, use_tls, username, password, connections, level, optional, retention, enabled, settings, source
		FROM usenet_servers ORDER BY level, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsenetServer{}
	for rows.Next() {
		var u UsenetServer
		var tls, optional, enabled int
		var settings string
		if err := rows.Scan(&u.ID, &u.Name, &u.Host, &u.Port, &tls, &u.Username, &u.Password, &u.Connections, &u.Level, &optional, &u.Retention, &enabled, &settings, &u.Source); err != nil {
			return nil, err
		}
		u.Settings = json.RawMessage(settings)
		u.UseTLS, u.Optional, u.Enabled, u.HasPassword = tls == 1, optional == 1, enabled == 1, u.Password != ""
		out = append(out, u)
	}
	return out, rows.Err()
}

// Blocked is a release not to grab again.
type Blocked struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	TitleID     int64     `json:"titleId"`
	SourceTitle string    `json:"sourceTitle"`
	Quality     int       `json:"quality"`
	Protocol    string    `json:"protocol,omitempty"`
	Indexer     string    `json:"indexer,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	At          time.Time `json:"at"`
	Source      string    `json:"source,omitempty"`
}

// Block adds a release to the blocklist; one that's there already is kept.
func (s *Store) Block(ctx context.Context, b Blocked) error {
	_, err := s.exec(ctx, `INSERT INTO blocklist (kind, title_id, source_title, quality, protocol, indexer, reason, at, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, title_id, source_title) DO NOTHING`,
		b.Kind, b.TitleID, b.SourceTitle, b.Quality, b.Protocol, b.Indexer, b.Reason, b.At.Unix(), b.Source)
	return err
}

func (s *Store) Blocklist(ctx context.Context, limit int) ([]Blocked, error) {
	rows, err := s.query(ctx, "SELECT id, kind, title_id, source_title, quality, protocol, indexer, reason, at, source FROM blocklist ORDER BY at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Blocked{}
	for rows.Next() {
		var b Blocked
		var at int64
		if err := rows.Scan(&b.ID, &b.Kind, &b.TitleID, &b.SourceTitle, &b.Quality, &b.Protocol, &b.Indexer, &b.Reason, &at, &b.Source); err != nil {
			return nil, err
		}
		b.At = time.Unix(at, 0).UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) CountBlocklist(ctx context.Context) (int, error) {
	var n int
	return n, s.row(ctx, "SELECT COUNT(*) FROM blocklist").Scan(&n)
}

// ImportedSetting is one section of settings brought over from another app.
type ImportedSetting struct {
	App        string          `json:"app"`
	Section    string          `json:"section"`
	Data       json.RawMessage `json:"data"`
	ImportedAt time.Time       `json:"importedAt"`
}

func (s *Store) SaveImportedSetting(ctx context.Context, app, section string, data []byte) error {
	_, err := s.exec(ctx, `INSERT INTO imported_settings (app, section, data, imported_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (app, section) DO UPDATE SET data = excluded.data, imported_at = excluded.imported_at`, app, section, string(data), now())
	return err
}

func (s *Store) ImportedSettings(ctx context.Context) ([]ImportedSetting, error) {
	rows, err := s.query(ctx, "SELECT app, section, data, imported_at FROM imported_settings ORDER BY app, section")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportedSetting{}
	for rows.Next() {
		var is ImportedSetting
		var data string
		var at int64
		if err := rows.Scan(&is.App, &is.Section, &data, &at); err != nil {
			return nil, err
		}
		is.Data, is.ImportedAt = json.RawMessage(data), time.Unix(at, 0).UTC()
		out = append(out, is)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDownloadClient(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM download_clients WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

func (s *Store) DeleteUsenetServer(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM usenet_servers WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}
