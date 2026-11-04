package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// nzbget reads NZBGet's settings over its JSON-RPC API.
type nzbget struct {
	base       *url.URL
	user, pass string
	http       *http.Client
}

func newNZBGet(rawURL, user, pass string) (*nzbget, error) {
	u, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &nzbget{base: u, user: user, pass: pass, http: &http.Client{Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (n *nzbget) call(ctx context.Context, method string, out any) error {
	body, _ := json.Marshal(map[string]any{"version": "1.1", "method": method, "params": []any{}})
	u := *n.base
	u.Path += "/jsonrpc"
	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(n.user, n.pass)
	req.Header.Set("Content-Type", "application/json")
	res, err := n.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("couldn't reach NZBGet at %s: %w", n.base.Redacted(), err)
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errors.New("NZBGet didn't accept that username and password (Settings → Security → ControlUsername)")
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("NZBGet answered HTTP %d", res.StatusCode)
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&r); err != nil {
		return fmt.Errorf("%s doesn't look like NZBGet's address", n.base.Redacted())
	}
	if r.Error != nil {
		return errors.New("NZBGet: " + r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

type nzbgetConfig map[string]string

func (n *nzbget) config(ctx context.Context) (string, nzbgetConfig, error) {
	var version string
	if err := n.call(ctx, "version", &version); err != nil {
		return "", nil, err
	}
	var list []struct{ Name, Value string }
	if err := n.call(ctx, "config", &list); err != nil {
		return "", nil, err
	}
	c := nzbgetConfig{}
	for _, kv := range list {
		c[kv.Name] = kv.Value
	}
	return version, c, nil
}

// numbered lists the n of every "<prefix><n>.<field>" key, in order.
func (c nzbgetConfig) numbered(prefix string) []int {
	seen := map[int]bool{}
	for k := range c {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		num, _, ok := strings.Cut(rest, ".")
		if n, err := strconv.Atoi(num); ok && err == nil {
			seen[n] = true
		}
	}
	var out []int
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func yes(v string) bool { return strings.EqualFold(v, "yes") || strings.EqualFold(v, "true") }

func (c nzbgetConfig) servers() []store.UsenetServer {
	var out []store.UsenetServer
	for _, n := range c.numbered("Server") {
		get := func(f string) string { return c[fmt.Sprintf("Server%d.%s", n, f)] }
		if get("Host") == "" {
			continue
		}
		port, _ := strconv.Atoi(get("Port"))
		conns, _ := strconv.Atoi(get("Connections"))
		level, _ := strconv.Atoi(get("Level"))
		ret, _ := strconv.Atoi(get("Retention"))
		extra := map[string]string{}
		for _, f := range []string{"Group", "JoinGroup", "Cipher", "CertVerification", "IpVersion", "Notes"} {
			if v := get(f); v != "" {
				extra[f] = v
			}
		}
		settings, _ := json.Marshal(extra)
		out = append(out, store.UsenetServer{
			Name: or(get("Name"), get("Host")), Host: get("Host"), Port: port, UseTLS: yes(get("Encryption")),
			Username: get("Username"), Password: get("Password"), Connections: conns, Level: level, Optional: yes(get("Optional")),
			Retention: ret, Enabled: get("Active") == "" || yes(get("Active")), Settings: settings, Source: "nzbget",
		})
	}
	return out
}

// controlKeys are NZBGet's own sign-in, which Box Office has no use for.
var controlKeys = []string{"ControlUsername", "ControlPassword", "RestrictedUsername", "RestrictedPassword", "AddUsername", "AddPassword"}

func (c nzbgetConfig) sections() map[string]map[string]string {
	out := map[string]map[string]string{"categories": {}, "general": {}}
	for k, v := range c {
		switch {
		case slices.Contains(controlKeys, k), strings.HasPrefix(k, "Server"):
		case strings.HasPrefix(k, "Category"):
			out["categories"][k] = v
		default:
			out["general"][k] = v
		}
	}
	return out
}

func (m *Migrator) previewNZBGet(ctx context.Context, req Request) (Plan, *pending, error) {
	n, err := newNZBGet(req.URL, req.Username, req.Password)
	if err != nil {
		return Plan{}, nil, err
	}
	version, c, err := n.config(ctx)
	if err != nil {
		return Plan{}, nil, err
	}
	p := &pending{req: req, nzb: n, nzbConfig: c}
	plan := Plan{PathMap: []PathMap{}, Token: newToken(), App: "nzbget", AppVersion: version, URL: n.base.String(), Warnings: []string{},
		Profiles: []ProfilePlan{}, RootFolders: []FolderPlan{}, Sample: []Sample{}, ExpiresAt: time.Now().Add(planLife)}
	servers := c.servers()
	plan.Settings = SettingsPlan{UsenetServers: len(servers), Categories: len(c.numbered("Category")), Sections: []string{"categories", "general"}}
	for _, s := range servers {
		plan.Servers = append(plan.Servers, ServerPlan{Name: s.Name, Host: s.Host, Port: s.Port, TLS: s.UseTLS, Connections: s.Connections, Level: s.Level, HasPassword: s.Password != ""})
	}
	if len(servers) == 0 {
		plan.Warnings = append(plan.Warnings, "NZBGet has no news servers set up.")
	}
	if rate := c["DownloadRate"]; rate != "" && rate != "0" {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("NZBGet's speed limit (%s KB/s) comes across too.", rate))
	}
	return plan, p, nil
}

// runNZBGet keeps NZBGet's settings as a backup file and brings its news
// servers and settings over.
func (m *Migrator) runNZBGet(ctx context.Context, p *pending, pr *Progress, o Options, step func(int, string, string), fail func(int, error)) {
	if o.BackupLegacy {
		step(1, "running", "")
		data, _ := json.MarshalIndent(p.nzbConfig, "", "  ")
		name := "nzbget-config-" + time.Now().UTC().Format("20060102-150405") + ".json"
		path, err := m.Backups.KeepFile(ctx, name, data)
		if err != nil && path == "" {
			fail(1, fmt.Errorf("didn't migrate, because saving NZBGet's settings failed: %w", err))
			return
		}
		msg := "NZBGet's settings saved as " + path
		if err != nil {
			msg += "; " + err.Error()
		}
		step(1, "done", msg)
		m.update(func() { pr.Backups = append(pr.Backups, filepath.Base(path)) })
	}
	step(2, "running", "")
	servers := p.nzbConfig.servers()
	for _, s := range servers {
		if err := m.Catalog.Store.UpsertUsenetServer(ctx, s); err != nil {
			fail(2, fmt.Errorf("news server %s: %w", s.Name, err))
			return
		}
	}
	for sec, kv := range p.nzbConfig.sections() {
		b, _ := json.Marshal(kv)
		if err := m.Catalog.Store.SaveImportedSetting(ctx, "nzbget", sec, b); err != nil {
			fail(2, err)
			return
		}
	}
	m.update(func() {
		pr.Steps[2].Status = "done"
		pr.Steps[2].Message = count(len(servers), "news server", "news servers") + ", " + count(len(p.nzbConfig.numbered("Category")), "category", "categories") + ", and NZBGet's other settings"
		pr.Status = "done"
	})
	m.finish(ctx, pr)
}
