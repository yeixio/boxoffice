package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ---- connections: indexers, download clients, news servers, blocklist ----
//
// These come over from the old apps. Box Office's downloader uses them
// once it lands; until then they can be looked over and removed.

// secretish reports whether a settings key holds a password or key.
func secretish(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "apikey", "api_key", "token", "secret", "passkey", "cookie", "privatekey", "accesskey"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redact hides every password and key in a settings object, at any depth,
// so the API never hands one out.
func redact(raw json.RawMessage) json.RawMessage {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return raw
	}
	b, _ := json.Marshal(redactValue(v))
	return b
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		// Radarr and Sonarr list fields as {"name": ..., "value": ...}.
		if name, ok := t["name"].(string); ok && secretish(name) {
			if s, ok := t["value"].(string); ok && s != "" {
				t["value"] = "********"
			}
		}
		for k, x := range t {
			if s, ok := x.(string); ok && s != "" && secretish(k) {
				t[k] = "********"
				continue
			}
			t[k] = redactValue(x)
		}
	case []any:
		for i, x := range t {
			t[i] = redactValue(x)
		}
	}
	return v
}

func (s *Server) connections(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	idx, err := s.Store.Indexers(ctx)
	if err != nil {
		return err
	}
	for i := range idx {
		idx[i].Settings = redact(idx[i].Settings)
	}
	dcs, err := s.Store.DownloadClients(ctx)
	if err != nil {
		return err
	}
	for i := range dcs {
		dcs[i].Settings = redact(dcs[i].Settings)
	}
	servers, err := s.Store.UsenetServers(ctx)
	if err != nil {
		return err
	}
	for i := range servers {
		servers[i].Settings = redact(servers[i].Settings)
	}
	blocked, err := s.Store.CountBlocklist(ctx)
	if err != nil {
		return err
	}
	states, err := s.Search.IndexerStates(ctx)
	if err != nil {
		return err
	}
	quotas, err := s.Downloads.Quotas(ctx)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"indexers": idx, "downloadClients": dcs, "usenetServers": servers, "blocklist": blocked,
		"indexerStates": states, "quotas": quotas})
}

func (s *Server) deleteConnection(del func(*http.Request, int64) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		if err := del(r, id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func (s *Server) blocklist(w http.ResponseWriter, r *http.Request) error {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	list, err := s.Store.Blocklist(r.Context(), limit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

// importedSettings lists what came over from each old app, section by
// section, with secrets hidden.
func (s *Server) importedSettings(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Store.ImportedSettings(r.Context())
	if err != nil {
		return err
	}
	for i := range list {
		list[i].Data = redact(list[i].Data)
	}
	return writeJSON(w, http.StatusOK, list)
}
