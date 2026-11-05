package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Adding and changing indexers, download clients and news servers by hand.

func (s *Store) Indexer(ctx context.Context, id int64) (Indexer, error) {
	list, err := s.Indexers(ctx)
	if err != nil {
		return Indexer{}, err
	}
	for _, ix := range list {
		if ix.ID == id {
			return ix, nil
		}
	}
	return Indexer{}, ErrNotFound
}

func (s *Store) CreateIndexer(ctx context.Context, ix Indexer) (int64, error) {
	cats, _ := json.Marshal(ix.Categories)
	if ix.Settings == nil {
		ix.Settings = json.RawMessage("{}")
	}
	return s.insert(ctx, `INSERT INTO indexers (name, protocol, kind, url, api_path, api_key, categories, enable_rss, enable_search, priority, settings, source, missing_secret,
		daily_queries, daily_grabs) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		ix.Name, ix.Protocol, ix.Kind, ix.URL, ix.APIPath, ix.APIKey, string(cats), b2i(ix.EnableRSS), b2i(ix.EnableSearch), ix.Priority, string(ix.Settings), ix.Source,
		ix.DailyQueries, ix.DailyGrabs)
}

// UpdateIndexer saves an indexer as given. A key filled in clears the
// mark that one is missing.
func (s *Store) UpdateIndexer(ctx context.Context, ix Indexer) error {
	cats, _ := json.Marshal(ix.Categories)
	res, err := s.exec(ctx, `UPDATE indexers SET name = ?, protocol = ?, kind = ?, url = ?, api_path = ?, api_key = ?, categories = ?, enable_rss = ?,
		enable_search = ?, priority = ?, missing_secret = ?, daily_queries = ?, daily_grabs = ? WHERE id = ?`,
		ix.Name, ix.Protocol, ix.Kind, ix.URL, ix.APIPath, ix.APIKey, string(cats), b2i(ix.EnableRSS), b2i(ix.EnableSearch), ix.Priority,
		b2i(ix.MissingSecret && ix.APIKey == ""), ix.DailyQueries, ix.DailyGrabs, ix.ID)
	if err != nil {
		return uniqueErr(err)
	}
	return affected(res)
}

func (s *Store) DownloadClient(ctx context.Context, id int64) (DownloadClient, error) {
	list, err := s.DownloadClients(ctx)
	if err != nil {
		return DownloadClient{}, err
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return DownloadClient{}, ErrNotFound
}

func (s *Store) CreateDownloadClient(ctx context.Context, c DownloadClient) (int64, error) {
	return s.insert(ctx, `INSERT INTO download_clients (name, kind, protocol, host, port, use_tls, url_base, username, password, category, priority, enabled, settings, source, missing_secret)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', '', 0)`,
		c.Name, c.Kind, c.Protocol, c.Host, c.Port, b2i(c.UseTLS), c.URLBase, c.Username, c.Password, c.Category, c.Priority, b2i(c.Enabled))
}

func (s *Store) UpdateDownloadClient(ctx context.Context, c DownloadClient) error {
	res, err := s.exec(ctx, `UPDATE download_clients SET name = ?, kind = ?, protocol = ?, host = ?, port = ?, use_tls = ?, url_base = ?, username = ?,
		password = ?, category = ?, priority = ?, enabled = ?, missing_secret = ? WHERE id = ?`,
		c.Name, c.Kind, c.Protocol, c.Host, c.Port, b2i(c.UseTLS), c.URLBase, c.Username, c.Password, c.Category, c.Priority, b2i(c.Enabled),
		b2i(c.MissingSecret && c.Password == ""), c.ID)
	if err != nil {
		return uniqueErr(err)
	}
	return affected(res)
}

func (s *Store) UsenetServer(ctx context.Context, id int64) (UsenetServer, error) {
	list, err := s.UsenetServers(ctx)
	if err != nil {
		return UsenetServer{}, err
	}
	for _, u := range list {
		if u.ID == id {
			return u, nil
		}
	}
	return UsenetServer{}, ErrNotFound
}

func (s *Store) CreateUsenetServer(ctx context.Context, u UsenetServer) (int64, error) {
	return s.insert(ctx, `INSERT INTO usenet_servers (name, host, port, use_tls, username, password, connections, level, optional, retention, enabled, settings, source,
		quota, quota_since, expires) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', '', ?, ?, ?)`,
		u.Name, u.Host, u.Port, b2i(u.UseTLS), u.Username, u.Password, u.Connections, u.Level, b2i(u.Optional), u.Retention, b2i(u.Enabled),
		u.Quota, secs(u.QuotaSince), secs(u.Expires))
}

func (s *Store) UpdateUsenetServer(ctx context.Context, u UsenetServer) error {
	res, err := s.exec(ctx, `UPDATE usenet_servers SET name = ?, host = ?, port = ?, use_tls = ?, username = ?, password = ?, connections = ?, level = ?,
		optional = ?, retention = ?, enabled = ?, quota = ?, quota_since = ?, expires = ? WHERE id = ?`,
		u.Name, u.Host, u.Port, b2i(u.UseTLS), u.Username, u.Password, u.Connections, u.Level, b2i(u.Optional), u.Retention, b2i(u.Enabled),
		u.Quota, secs(u.QuotaSince), secs(u.Expires), u.ID)
	if err != nil {
		return uniqueErr(err)
	}
	return affected(res)
}

// Categories splits a download client's categories ("movies, tv").
func (c DownloadClient) Categories() []string {
	var out []string
	for _, p := range strings.Split(c.Category, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// MergeDuplicateDownloadClients folds download clients that are the same
// app at the same address (Radarr's and Sonarr's copies of one NZBGet,
// say, differing only by category) into one, with every category.
func (s *Store) MergeDuplicateDownloadClients(ctx context.Context) error {
	list, err := s.DownloadClients(ctx)
	if err != nil {
		return err
	}
	type key struct {
		kind, host string
		port       int
	}
	first := map[key]*DownloadClient{}
	var drop []int64
	changed := map[int64]bool{}
	slices.SortFunc(list, func(a, b DownloadClient) int { return int(a.ID - b.ID) })
	for i := range list {
		c := &list[i]
		k := key{c.Kind, strings.ToLower(c.Host), c.Port}
		keep := first[k]
		if keep == nil {
			first[k] = c
			continue
		}
		cats := append(keep.Categories(), c.Categories()...)
		slices.Sort(cats)
		keep.Category = strings.Join(slices.Compact(cats), ", ")
		if keep.Password == "" && c.Password != "" {
			keep.Password, keep.MissingSecret = c.Password, false
		}
		keep.Enabled = keep.Enabled || c.Enabled
		changed[keep.ID] = true
		drop = append(drop, c.ID)
	}
	for _, id := range drop {
		if _, err := s.exec(ctx, "DELETE FROM download_clients WHERE id = ?", id); err != nil {
			return err
		}
	}
	for _, c := range first {
		if changed[c.ID] {
			if err := s.UpdateDownloadClient(ctx, *c); err != nil {
				return err
			}
		}
	}
	return nil
}

// BlockedFor is the blocklist for one title.
func (s *Store) BlockedFor(ctx context.Context, kind string, titleID int64) ([]Blocked, error) {
	rows, err := s.query(ctx, "SELECT source_title FROM blocklist WHERE kind = ? AND title_id = ?", kind, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blocked
	for rows.Next() {
		var b Blocked
		if err := rows.Scan(&b.SourceTitle); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
