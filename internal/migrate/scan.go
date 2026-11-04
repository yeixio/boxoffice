package migrate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// A Scan is a preview running in the background. Reading a large library
// can take minutes, longer than a proxy in front of Box Office waits for
// one request, and an admin can look over or connect another app meanwhile.
type Scan struct {
	ID      string    `json:"id"`
	App     string    `json:"app"`
	URL     string    `json:"url"`
	Status  string    `json:"status"` // running, ready, failed
	Step    string    `json:"step"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	Error   string    `json:"error,omitempty"`
	Plan    *Plan     `json:"plan,omitempty"`
	Started time.Time `json:"started"`
	// Finished is when the preview ended, or for a ready one, when its plan
	// was used or expires.
	Finished time.Time `json:"finished,omitzero"`
}

// scanTimeout bounds one preview, however slow the old app or the media.
const scanTimeout = 30 * time.Minute

// StartScan checks req, then previews it in the background.
func (m *Migrator) StartScan(req Request) (Scan, error) {
	req.App = strings.ToLower(strings.TrimSpace(req.App))
	if _, ok := defaultPorts[req.App]; !ok {
		return Scan{}, errors.New("Box Office can't move in from " + req.App + " yet")
	}
	if _, err := normalizeURL(req.URL); err != nil {
		return Scan{}, err
	}
	if req.App == "nzbget" {
		if _, err := newNZBGet(req.URL, req.Username, req.Password); err != nil {
			return Scan{}, err
		}
	} else if _, err := newLegacy(req.App, req.URL, req.APIKey); err != nil {
		return Scan{}, err
	}
	sc := &Scan{ID: newToken(), App: req.App, URL: strings.TrimSpace(req.URL), Status: "running", Step: "Starting", Started: time.Now().UTC()}
	m.mu.Lock()
	if m.scans == nil {
		m.scans = map[string]*Scan{}
	}
	m.pruneScans()
	m.scans[sc.ID] = sc
	out := *sc
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		plan, err := m.preview(ctx, req, func(step string, done, total int) {
			m.mu.Lock()
			sc.Step, sc.Done, sc.Total = step, done, total
			m.mu.Unlock()
		})
		m.mu.Lock()
		defer m.mu.Unlock()
		sc.Finished = time.Now().UTC()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = errors.New("looking it over took longer than " + scanTimeout.String())
			}
			sc.Status, sc.Error = "failed", err.Error()
			m.Log.Warn("move-in preview failed", "app", req.App, "err", err)
			return
		}
		sc.Status, sc.Plan, sc.URL, sc.Finished = "ready", &plan, plan.URL, plan.ExpiresAt
		sc.Step, sc.Done, sc.Total = "Ready to look over", 0, 0
	}()
	return out, nil
}

// pruneScans drops scans that ended long ago, and ready ones whose plan was
// used or has expired. m.mu is held.
func (m *Migrator) pruneScans() {
	for id, sc := range m.scans {
		switch {
		case sc.Status == "ready" && (time.Now().After(sc.Plan.ExpiresAt) || m.plans[sc.Plan.Token] == nil):
			delete(m.scans, id)
		case sc.Status == "failed" && time.Since(sc.Finished) > planLife:
			delete(m.scans, id)
		}
	}
}

// Scans lists the scans running, ready to look over, or failed, newest
// first.
func (m *Migrator) Scans() []Scan {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneScans()
	out := make([]Scan, 0, len(m.scans))
	for _, sc := range m.scans {
		out = append(out, *sc)
	}
	slices.SortFunc(out, func(a, b Scan) int { return b.Started.Compare(a.Started) })
	return out
}

// ScanByID returns one scan, and false if there's no such scan (any more).
func (m *Migrator) ScanByID(id string) (Scan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneScans()
	sc, ok := m.scans[id]
	if !ok {
		return Scan{}, false
	}
	return *sc, true
}

// DismissScan forgets a scan, and its plan if it was ready. One still
// running finishes and is then dropped.
func (m *Migrator) DismissScan(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sc, ok := m.scans[id]; ok {
		if sc.Plan != nil {
			delete(m.plans, sc.Plan.Token)
		}
		delete(m.scans, id)
	}
}
