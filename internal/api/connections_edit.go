package api

import (
	"cmp"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// ---- adding and changing indexers, download clients and news servers ----
//
// A password or key left blank keeps the saved one, so the forms never
// need to show it.

func (s *Server) connectionRoutes() []route {
	return []route{
		{"POST /api/v1/indexers", true, s.saveIndexer(false)},
		{"PUT /api/v1/indexers/{id}", true, s.saveIndexer(true)},
		{"POST /api/v1/indexers/test", true, s.testIndexer},
		{"POST /api/v1/indexers/{id}/reset", true, s.resetIndexer},
		{"POST /api/v1/downloadclients", true, s.saveDownloadClient(false)},
		{"PUT /api/v1/downloadclients/{id}", true, s.saveDownloadClient(true)},
		{"POST /api/v1/usenetservers", true, s.saveUsenetServer(false)},
		{"PUT /api/v1/usenetservers/{id}", true, s.saveUsenetServer(true)},
		{"POST /api/v1/usenetservers/test", true, s.testUsenetServer},
	}
}

type indexerIn struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	URL          string `json:"url"`
	APIPath      string `json:"apiPath"`
	APIKey       string `json:"apiKey"`
	Categories   []int  `json:"categories"`
	EnableRSS    bool   `json:"enableRss"`
	EnableSearch bool   `json:"enableSearch"`
	Priority     int    `json:"priority"`
	DailyQueries int    `json:"dailyQueries"`
	DailyGrabs   int    `json:"dailyGrabs"`
}

func checkURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errorf(http.StatusBadRequest, "enter the indexer's web address, like https://api.nzbgeek.info")
	}
	return raw, nil
}

// indexerFrom fills an indexer from the form, over the saved one.
func (s *Server) indexerFrom(r *http.Request, in indexerIn, id int64) (store.Indexer, error) {
	var ix store.Indexer
	if id > 0 {
		var err error
		if ix, err = s.Store.Indexer(r.Context(), id); err != nil {
			return ix, err
		}
	} else {
		ix.Kind = "newznab"
	}
	u, err := checkURL(in.URL)
	if err != nil {
		return ix, err
	}
	ix.Name = strings.TrimSpace(in.Name)
	if ix.Name == "" {
		ix.Name = hostOf(u)
	}
	ix.URL = u
	ix.Protocol = cmp.Or(in.Protocol, "usenet")
	if ix.Protocol != "usenet" && ix.Protocol != "torrent" {
		return ix, errorf(http.StatusBadRequest, "an indexer is for usenet or torrent")
	}
	if id == 0 && ix.Protocol == "torrent" {
		ix.Kind = "torznab"
	}
	ix.APIPath = strings.TrimSpace(in.APIPath)
	if k := strings.TrimSpace(in.APIKey); k != "" {
		ix.APIKey = k
	}
	ix.Categories = in.Categories
	ix.EnableRSS, ix.EnableSearch = in.EnableRSS, in.EnableSearch
	ix.Priority = min(max(in.Priority, 1), 50)
	ix.DailyQueries, ix.DailyGrabs = min(max(in.DailyQueries, 0), 1e6), min(max(in.DailyGrabs, 0), 1e6)
	return ix, nil
}

// resetIndexer ends an indexer's rest after errors, to try it now.
func (s *Server) resetIndexer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.Store.Indexer(r.Context(), id); err != nil {
		return err
	}
	if err := s.Search.ResetIndexer(r.Context(), id); err != nil {
		return err
	}
	return s.connections(w, r)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(u.Hostname(), "api.")
}

func (s *Server) saveIndexer(edit bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in indexerIn
		if err := readJSON(r, &in); err != nil {
			return err
		}
		var id int64
		if edit {
			var err error
			if id, err = pathID(r, "id"); err != nil {
				return err
			}
		}
		ix, err := s.indexerFrom(r, in, id)
		if err != nil {
			return err
		}
		if ix.APIKey == "" {
			return errorf(http.StatusBadRequest, "enter the indexer's API key")
		}
		if edit {
			err = s.Store.UpdateIndexer(r.Context(), ix)
		} else {
			ix.ID, err = s.Store.CreateIndexer(r.Context(), ix)
		}
		if err != nil {
			return err
		}
		return s.connections(w, r)
	}
}

// testIndexer asks the indexer what it can do, which checks the address and
// key, and lists its categories for the form.
func (s *Server) testIndexer(w http.ResponseWriter, r *http.Request) error {
	var in indexerIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ix, err := s.indexerFrom(r, in, in.ID)
	if err != nil {
		return err
	}
	caps, err := s.Search.Client.Caps(r.Context(), ix)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	return writeJSON(w, http.StatusOK, caps)
}

type downloadClientIn struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	UseTLS   bool   `json:"useTls"`
	URLBase  string `json:"urlBase"`
	Username string `json:"username"`
	Password string `json:"password"`
	Category string `json:"category"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

var clientProtocol = map[string]string{"nzbget": "usenet", "sabnzbd": "usenet", "deluge": "torrent", "qbittorrent": "torrent", "transmission": "torrent", "rtorrent": "torrent"}

func (s *Server) saveDownloadClient(edit bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in downloadClientIn
		if err := readJSON(r, &in); err != nil {
			return err
		}
		var c store.DownloadClient
		if edit {
			id, err := pathID(r, "id")
			if err != nil {
				return err
			}
			if c, err = s.Store.DownloadClient(r.Context(), id); err != nil {
				return err
			}
		}
		kind := strings.ToLower(strings.TrimSpace(in.Kind))
		proto, ok := clientProtocol[kind]
		if !ok {
			return errorf(http.StatusBadRequest, "pick which app it is")
		}
		host := strings.TrimSpace(in.Host)
		if strings.Contains(host, "://") {
			if u, err := url.Parse(host); err == nil {
				host = u.Hostname()
			}
		}
		if host == "" {
			return errorf(http.StatusBadRequest, "enter the address it's at")
		}
		if in.Port <= 0 || in.Port > 65535 {
			return errorf(http.StatusBadRequest, "enter the port it listens on")
		}
		c.Name = cmp.Or(strings.TrimSpace(in.Name), kind)
		c.Kind, c.Protocol, c.Host, c.Port, c.UseTLS = kind, proto, host, in.Port, in.UseTLS
		c.URLBase, c.Username, c.Category = strings.TrimSpace(in.URLBase), strings.TrimSpace(in.Username), strings.TrimSpace(in.Category)
		if in.Password != "" {
			c.Password = in.Password
		}
		c.Priority, c.Enabled = max(in.Priority, 1), in.Enabled
		var err error
		if edit {
			err = s.Store.UpdateDownloadClient(r.Context(), c)
		} else {
			_, err = s.Store.CreateDownloadClient(r.Context(), c)
		}
		if err != nil {
			return err
		}
		return s.connections(w, r)
	}
}

type usenetServerIn struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UseTLS      bool   `json:"useTls"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Connections int    `json:"connections"`
	Level       int    `json:"level"`
	Enabled     bool   `json:"enabled"`
	// A block account: GB left on it now (0 for an unlimited plan), and
	// the day it expires (YYYY-MM-DD, blank for never).
	QuotaGB float64 `json:"quotaGb"`
	Expires string  `json:"expires"`
}

func (s *Server) usenetServerFrom(r *http.Request, in usenetServerIn, id int64) (store.UsenetServer, error) {
	var u store.UsenetServer
	if id > 0 {
		var err error
		if u, err = s.Store.UsenetServer(r.Context(), id); err != nil {
			return u, err
		}
	}
	host := strings.TrimSpace(in.Host)
	if strings.Contains(host, "://") {
		if p, err := url.Parse(host); err == nil {
			host = p.Hostname()
		}
	}
	if host == "" {
		return u, errorf(http.StatusBadRequest, "enter the news server's address, like news.example.com")
	}
	u.Host, u.UseTLS = host, in.UseTLS
	u.Port = in.Port
	if u.Port <= 0 {
		u.Port = 119
		if u.UseTLS {
			u.Port = 563
		}
	}
	u.Name = cmp.Or(strings.TrimSpace(in.Name), host)
	u.Username = strings.TrimSpace(in.Username)
	if in.Password != "" {
		u.Password = in.Password
	}
	u.Connections = min(max(in.Connections, 1), 100)
	u.Level = min(max(in.Level, 0), 9)
	u.Enabled = in.Enabled
	// Data left is counted from when it's entered: a new number starts
	// the count again.
	quota := int64(max(in.QuotaGB, 0) * 1e9)
	if quota == 0 {
		u.Quota, u.QuotaSince = 0, time.Time{}
	} else if quota != u.Quota {
		left := u.Quota
		if u.Quota > 0 && !u.QuotaSince.IsZero() {
			if qs, err := s.Downloads.Quotas(r.Context()); err == nil {
				left = qs[u.ID].Left
			}
		}
		// The form shows what's left, rounded; sending that back unchanged
		// keeps the count going.
		if u.Quota == 0 || int64(math.Round(float64(left)/1e8)) != int64(math.Round(float64(quota)/1e8)) {
			u.Quota, u.QuotaSince = quota, time.Now()
		}
	}
	u.Expires = time.Time{}
	if e := strings.TrimSpace(in.Expires); e != "" {
		t, err := time.ParseInLocation(time.DateOnly, e, time.Local)
		if err != nil {
			return u, errorf(http.StatusBadRequest, "enter the day the account expires as a date, like 2027-06-30")
		}
		u.Expires = t.AddDate(0, 0, 1) // it works through the day it expires
	}
	return u, nil
}

func (s *Server) saveUsenetServer(edit bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in usenetServerIn
		if err := readJSON(r, &in); err != nil {
			return err
		}
		var id int64
		if edit {
			var err error
			if id, err = pathID(r, "id"); err != nil {
				return err
			}
		}
		u, err := s.usenetServerFrom(r, in, id)
		if err != nil {
			return err
		}
		if edit {
			err = s.Store.UpdateUsenetServer(r.Context(), u)
		} else {
			_, err = s.Store.CreateUsenetServer(r.Context(), u)
		}
		if err != nil {
			return err
		}
		return s.connections(w, r)
	}
}

func (s *Server) testUsenetServer(w http.ResponseWriter, r *http.Request) error {
	var in usenetServerIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u, err := s.usenetServerFrom(r, in, in.ID)
	if err != nil {
		return err
	}
	if err := downloader.TestServer(r.Context(), u); err != nil {
		return errorf(http.StatusBadRequest, "Box Office couldn't sign in to "+u.Host+": "+err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
