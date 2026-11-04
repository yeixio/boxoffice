package migrate

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/matinee/internal/boxoffice/quality"
	"github.com/yeixio/matinee/internal/boxoffice/store"
)

// masked is how Radarr and Sonarr show a secret over their API.
const masked = "********"

// provider is an indexer, download client, notification or list as Radarr
// and Sonarr describe them: an implementation and a list of fields.
type provider struct {
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	Implementation string  `json:"implementation"`
	Protocol       string  `json:"protocol"`
	Priority       int     `json:"priority"`
	Enable         *bool   `json:"enable"`
	EnableRSS      *bool   `json:"enableRss"`
	EnableSearch   *bool   `json:"enableAutomaticSearch"`
	Fields         []field `json:"fields"`
}

type field struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func (p provider) get(name string) any {
	for _, f := range p.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value
		}
	}
	return nil
}

func (p provider) str(name string) string {
	switch v := p.get(name).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (p provider) num(name string) int {
	switch v := p.get(name).(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func (p provider) flag(name string) bool { b, _ := p.get(name).(bool); return b }

func (p provider) ints(name string) []int {
	out := []int{}
	if list, ok := p.get(name).([]any); ok {
		for _, v := range list {
			if f, ok := v.(float64); ok {
				out = append(out, int(f))
			}
		}
	}
	return out
}

// maskedFields lists the fields the app hid.
func (p provider) maskedFields() []string {
	var out []string
	for _, f := range p.Fields {
		if s, ok := f.Value.(string); ok && s == masked {
			out = append(out, f.Name)
		}
	}
	return out
}

// fill puts real values from the app's own database in place of hidden ones.
func (p *provider) fill(real map[string]any) {
	for i, f := range p.Fields {
		if s, ok := f.Value.(string); ok && s == masked {
			for k, v := range real {
				if strings.EqualFold(k, f.Name) {
					p.Fields[i].Value = v
				}
			}
		}
	}
}

func (p provider) settingsJSON() json.RawMessage {
	m := map[string]any{"implementation": p.Implementation}
	for _, f := range p.Fields {
		m[f.Name] = f.Value
	}
	b, _ := json.Marshal(m)
	return b
}

// sections are the rest of an app's settings, kept as they are. Endpoints
// an app's version doesn't have are skipped.
var sections = map[string][]string{
	"radarr": {"config/naming", "config/mediamanagement", "config/indexer", "config/downloadclient", "qualitydefinition", "tag",
		"delayprofile", "notification", "importlist", "customformat", "remotepathmapping", "metadata", "autotagging", "indexer", "downloadclient"},
	"sonarr": {"config/naming", "config/mediamanagement", "config/indexer", "config/downloadclient", "qualitydefinition", "tag",
		"delayprofile", "notification", "importlist", "customformat", "remotepathmapping", "releaseprofile", "metadata", "autotagging", "indexer", "downloadclient"},
}

// tableFor names the table in the app's own database that holds a
// section's secrets.
var tableFor = map[string]string{"indexer": "Indexers", "downloadclient": "DownloadClients", "notification": "Notifications", "importlist": "ImportLists"}

// settingsData is what Preview read of an app's settings.
type settingsData struct {
	raw       map[string]json.RawMessage
	providers map[string][]provider // indexer, downloadclient, notification, importlist
	blocklist []lBlocked
}

type lBlocked struct {
	MovieID     int       `json:"movieId"`
	SeriesID    int       `json:"seriesId"`
	SourceTitle string    `json:"sourceTitle"`
	Quality     lQuality  `json:"quality"`
	Date        time.Time `json:"date"`
	Protocol    string    `json:"protocol"`
	Indexer     string    `json:"indexer"`
	Message     string    `json:"message"`
}

func (l *legacy) readSettings(ctx context.Context) (settingsData, error) {
	d := settingsData{raw: map[string]json.RawMessage{}, providers: map[string][]provider{}}
	for _, sec := range sections[l.app] {
		var raw json.RawMessage
		if err := l.get(ctx, "/"+sec, nil, &raw); err != nil {
			if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 405") {
				continue // this version doesn't have it
			}
			return d, err
		}
		d.raw[sec] = raw
		if _, ok := tableFor[sec]; ok {
			var ps []provider
			if err := json.Unmarshal(raw, &ps); err != nil {
				return d, fmt.Errorf("reading %s: %w", sec, err)
			}
			d.providers[sec] = ps
		}
	}
	for page := 1; ; page++ {
		var res struct {
			TotalRecords int        `json:"totalRecords"`
			Records      []lBlocked `json:"records"`
		}
		if err := l.get(ctx, "/blocklist", url.Values{"page": {strconv.Itoa(page)}, "pageSize": {"500"}}, &res); err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				break
			}
			return d, err
		}
		d.blocklist = append(d.blocklist, res.Records...)
		if len(res.Records) == 0 || len(d.blocklist) >= res.TotalRecords || page > 200 {
			break
		}
	}
	return d, nil
}

// hidden counts the secrets the app hid.
func (d settingsData) hidden() int {
	n := 0
	for _, ps := range d.providers {
		for _, p := range ps {
			n += len(p.maskedFields())
		}
	}
	return n
}

// secretsFromBackup reads indexer, download client, notification and list
// settings from the app's database inside its backup, by table and id.
func secretsFromBackup(app string, zipData []byte) (map[string]map[int]map[string]any, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("the backup isn't a zip file: %w", err)
	}
	var dbFile *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), app+".db") {
			dbFile = f
		}
	}
	if dbFile == nil {
		return nil, fmt.Errorf("the backup has no %s.db", app)
	}
	tmp, err := os.CreateTemp("", "boxoffice-import-*.db")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	rc, err := dbFile.Open()
	if err != nil {
		tmp.Close()
		return nil, err
	}
	_, err = io.Copy(tmp, io.LimitReader(rc, 8<<30))
	rc.Close()
	tmp.Close()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+tmp.Name()+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	out := map[string]map[int]map[string]any{}
	for sec, table := range tableFor {
		rows, err := db.Query("SELECT Id, Settings FROM " + table)
		if err != nil {
			continue // an older app without this table
		}
		out[sec] = map[int]map[string]any{}
		for rows.Next() {
			var id int
			var settings sql.NullString
			if rows.Scan(&id, &settings) != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(settings.String), &m) == nil {
				out[sec][id] = m
			}
		}
		rows.Close()
	}
	return out, nil
}

// fillSecrets puts the backup's secrets into the settings read over the
// API, and returns how many it couldn't fill.
func (d *settingsData) fillSecrets(secrets map[string]map[int]map[string]any) int {
	left := 0
	for sec, ps := range d.providers {
		for i := range ps {
			if real, ok := secrets[sec][ps[i].ID]; ok {
				ps[i].fill(real)
			}
			left += len(ps[i].maskedFields())
		}
		// Keep the raw copy in step.
		if b, err := json.Marshal(ps); err == nil {
			d.raw[sec] = b
		}
	}
	return left
}

// splitURL reads host, port, TLS and base path out of an address.
func splitURL(raw string) (host string, port int, tls bool, base string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw, 0, false, ""
	}
	tls = u.Scheme == "https"
	port, _ = strconv.Atoi(u.Port())
	return u.Hostname(), port, tls, strings.TrimRight(u.Path, "/")
}

// importSettings brings over an app's indexers, download clients,
// blocklist and every other section of its settings.
func (m *Migrator) importSettings(ctx context.Context, p *pending, pr *Progress) (string, error) {
	st := m.Catalog.Store
	app := p.req.App
	d := p.settings
	warn := func(s string) { m.update(func() { pr.Warnings = append(pr.Warnings, s) }) }
	if d.hidden() > 0 {
		if p.backupZip != nil {
			secrets, err := secretsFromBackup(app, p.backupZip)
			if err != nil {
				warn(fmt.Sprintf("couldn't read passwords and keys from %s's backup: %v", p.l.name(), err))
			} else if left := d.fillSecrets(secrets); left > 0 {
				warn(fmt.Sprintf("%d passwords or keys weren't in %s's backup; Box Office marks the settings that need them", left, p.l.name()))
			}
		} else {
			warn(fmt.Sprintf("%s hides passwords and API keys from other apps, and its backup was skipped, so %d of them came over blank. Box Office marks the settings that need them.", p.l.name(), d.hidden()))
		}
	}
	existing, err := st.Indexers(ctx)
	if err != nil {
		return "", err
	}
	nIdx := 0
	for _, ix := range d.providers["indexer"] {
		proto := strings.ToLower(ix.Protocol)
		if proto == "" {
			proto = "usenet"
		}
		key := ix.str("apiKey")
		if key == masked {
			key = ""
		}
		cats := ix.ints("categories")
		cats = append(cats, ix.ints("animeCategories")...)
		// The same indexer in Radarr and Sonarr is one indexer with both
		// sets of categories.
		baseURL := strings.TrimRight(ix.str("baseUrl"), "/")
		for _, e := range existing {
			if e.Protocol == proto && e.URL == baseURL && e.APIKey == key {
				cats = append(cats, e.Categories...)
			}
		}
		slices.Sort(cats)
		cats = slices.Compact(cats)
		row := store.Indexer{Name: ix.Name, Protocol: proto, Kind: strings.ToLower(ix.Implementation), URL: baseURL,
			APIPath: ix.str("apiPath"), APIKey: key, Categories: cats, EnableRSS: ix.EnableRSS == nil || *ix.EnableRSS,
			EnableSearch: ix.EnableSearch == nil || *ix.EnableSearch, Priority: ix.Priority, Settings: ix.settingsJSON(), Source: app,
			MissingSecret: len(ix.maskedFields()) > 0}
		if err := st.UpsertIndexer(ctx, row); err != nil {
			return "", fmt.Errorf("indexer %s: %w", ix.Name, err)
		}
		nIdx++
	}
	nDC := 0
	for _, dc := range d.providers["downloadclient"] {
		host := dc.str("host")
		port := dc.num("port")
		tls := dc.flag("useSsl")
		base := dc.str("urlBase")
		if strings.Contains(host, "://") {
			host, port, tls, base = splitURL(host)
		}
		password := dc.str("password")
		if password == "" {
			password = dc.str("apiKey")
		}
		if password == masked {
			password = ""
		}
		category := dc.str("movieCategory")
		if category == "" {
			category = dc.str("tvCategory")
		}
		row := store.DownloadClient{Name: dc.Name, Kind: strings.ToLower(dc.Implementation), Protocol: strings.ToLower(dc.Protocol), Host: host, Port: port,
			UseTLS: tls, URLBase: base, Username: dc.str("username"), Password: password, Category: category, Priority: max(dc.Priority, 1),
			Enabled: dc.Enable == nil || *dc.Enable, Settings: dc.settingsJSON(), Source: app, MissingSecret: len(dc.maskedFields()) > 0}
		if err := st.UpsertDownloadClient(ctx, row); err != nil {
			return "", fmt.Errorf("download client %s: %w", dc.Name, err)
		}
		nDC++
	}
	for sec, raw := range d.raw {
		if err := st.SaveImportedSetting(ctx, app, sec, raw); err != nil {
			return "", err
		}
	}
	nBlock := 0
	byLegacy := map[int]int64{} // the app's title id -> Box Office's
	if app == "radarr" {
		for _, mv := range p.movies {
			if have, err := st.MovieByTMDB(ctx, mv.TMDBID); err == nil {
				byLegacy[mv.ID] = have.ID
			}
		}
	} else {
		for _, sr := range p.series {
			if have, err := st.SeriesByTMDB(ctx, sr.TMDBID); err == nil && sr.TMDBID > 0 {
				byLegacy[sr.ID] = have.ID
			} else if have, err := st.SeriesByTVDB(ctx, sr.TVDBID); err == nil {
				byLegacy[sr.ID] = have.ID
			}
		}
	}
	for _, b := range d.blocklist {
		kind, legacyID := "movie", b.MovieID
		if app == "sonarr" {
			kind, legacyID = "series", b.SeriesID
		}
		id, ok := byLegacy[legacyID]
		if !ok {
			continue
		}
		if err := st.Block(ctx, store.Blocked{Kind: kind, TitleID: id, SourceTitle: b.SourceTitle, Quality: quality.Parse(b.Quality.Quality.Name).ID,
			Protocol: b.Protocol, Indexer: b.Indexer, Reason: b.Message, At: b.Date, Source: app}); err != nil {
			return "", err
		}
		nBlock++
	}
	return fmt.Sprintf("%d indexers, %d download clients, %d blocked releases, %d other sections", nIdx, nDC, nBlock, len(d.raw)), nil
}
