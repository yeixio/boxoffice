package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// SwitchRequest is what it takes to point Matinee at Box Office: an admin's
// sign-in to Matinee, used once and not kept, and the address Matinee
// reaches Box Office at.
type SwitchRequest struct {
	MatineeURL   string `json:"matineeUrl"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	BoxOfficeURL string `json:"boxOfficeUrl"`
	// App is radarr or sonarr: which of Matinee's connections to switch.
	App string `json:"app"`
}

// SwitchResult says what changed in Matinee.
type SwitchResult struct {
	App        string `json:"app"`
	Before     string `json:"before"` // the address Matinee used
	After      string `json:"after"`
	RootFolder string `json:"rootFolder,omitempty"`
	ProfileID  int    `json:"qualityProfileId,omitempty"`
}

type matineeIntegration struct {
	Connected        bool   `json:"connected"`
	URL              string `json:"url"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolder       string `json:"rootFolder"`
}

// SwitchMatinee points Matinee's Radarr or Sonarr connection at Box Office,
// keeping its default quality profile (Box Office answers to the old
// app's profile ids) and its root folder (mapped like the migration's
// paths). Matinee tests the new connection before saving it, so a wrong
// address changes nothing. apiKey is Box Office's.
func (m *Migrator) SwitchMatinee(ctx context.Context, migrationID int64, req SwitchRequest, apiKey string) (SwitchResult, error) {
	req.App = strings.ToLower(req.App)
	if req.App != "radarr" && req.App != "sonarr" {
		return SwitchResult{}, errors.New("pick radarr or sonarr")
	}
	var maps []PathMap
	if migrationID > 0 {
		pr, err := m.Progress(ctx, migrationID)
		if err != nil {
			return SwitchResult{}, err
		}
		if pr.Status != "done" {
			return SwitchResult{}, errors.New("finish the migration before switching Matinee over")
		}
		maps = pr.PathMap
	}
	mu, err := normalizeURL(req.MatineeURL)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("Matinee's address: %w", err)
	}
	bu, err := normalizeURL(req.BoxOfficeURL)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("Box Office's address: %w", err)
	}
	c := &matineeClient{base: strings.TrimRight(mu.String(), "/"), http: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var login struct {
		Token string `json:"token"`
		User  struct {
			Admin bool `json:"admin"`
		} `json:"user"`
	}
	if err := c.do(ctx, "POST", "/api/v1/auth/login", map[string]string{"username": req.Username, "password": req.Password, "deviceName": "Box Office"}, &login); err != nil {
		return SwitchResult{}, err
	}
	c.token = login.Token
	defer c.do(context.WithoutCancel(ctx), "POST", "/api/v1/auth/logout", nil, nil)
	var current map[string]matineeIntegration
	if err := c.do(ctx, "GET", "/api/v1/integrations", nil, &current); err != nil {
		return SwitchResult{}, err
	}
	before := current[req.App]
	after := strings.TrimRight(bu.String(), "/") + "/" + req.App
	res := SwitchResult{App: req.App, Before: before.URL, After: after, ProfileID: before.QualityProfileID}
	if res.RootFolder, err = m.matineeRoot(ctx, req.App, mapPath(maps, before.RootFolder)); err != nil {
		return SwitchResult{}, err
	}
	body := map[string]any{"url": after, "apiKey": apiKey, "qualityProfileId": res.ProfileID, "rootFolder": res.RootFolder}
	if err := c.do(ctx, "PUT", "/api/v1/integrations/"+req.App, body, nil); err != nil {
		return SwitchResult{}, fmt.Errorf("Matinee didn't save the new connection, so it still uses %s: %w", or(before.URL, "nothing"), err)
	}
	if migrationID > 0 {
		m.setMatinee(ctx, migrationID, fmt.Sprintf("Matinee's %s connection now goes to %s", req.App, after))
	}
	m.Log.Info("Matinee switched over", "app", req.App, "from", before.URL, "to", after)
	return res, nil
}

// matineeRoot picks the root folder Matinee adds titles to, exactly as
// Box Office lists it, since Matinee only saves one Box Office has: the
// one Matinee used (after the path maps), the one holding it, or else Box
// Office's only or first one.
func (m *Migrator) matineeRoot(ctx context.Context, app, want string) (string, error) {
	kind := map[string]string{"radarr": "movie", "sonarr": "series"}[app]
	roots, err := m.Catalog.Store.RootFolders(ctx, kind)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("Box Office has no %s root folder yet. Move in from %s first, or add one under Settings", map[string]string{"movie": "movie", "series": "TV"}[kind], map[string]string{"radarr": "Radarr", "sonarr": "Sonarr"}[app])
	}
	if want != "" {
		want = filepath.Clean(want)
		for _, r := range roots {
			if filepath.Clean(r.Path) == want {
				return r.Path, nil
			}
		}
		for _, r := range roots {
			if strings.HasPrefix(want, strings.TrimRight(filepath.Clean(r.Path), "/")+"/") {
				return r.Path, nil
			}
		}
	}
	return roots[0].Path, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type matineeClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *matineeClient) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach Matinee at %s: %w", c.base, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		json.Unmarshal(b, &e)
		msg := or(e.Error, e.Message)
		switch {
		case res.StatusCode == 401 && path == "/api/v1/auth/login":
			return errors.New("Matinee didn't accept that username and password")
		case res.StatusCode == 403:
			return errors.New("that Matinee account isn't an admin")
		case msg != "":
			return errors.New(msg)
		}
		return fmt.Errorf("Matinee answered HTTP %d", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}
