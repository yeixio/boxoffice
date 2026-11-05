// Package downloader is Box Office's own Usenet downloader, in place of
// NZBGet or SABnzbd: it fetches a release's articles from the news servers,
// decodes them, checks and repairs the files with par2, unpacks them, and
// (when that's turned on) moves the result into the library.
//
// Downloading is off until an admin turns it on, and so is moving finished
// downloads into the library. It only ever writes in its own downloads
// folder, except for that last step, which checks first that the library
// folder takes writes.
package downloader

import (
	"context"
	"strconv"

	"github.com/yeixio/boxoffice/internal/store"
)

// Settings keys.
const (
	SettingEnabled    = "downloads_enabled"
	SettingPaused     = "downloads_paused"
	SettingSpeedLimit = "downloads_speed_limit_kb" // KB/s; 0 or none is no limit
	SettingImport     = "downloads_import"
)

// Settings say what the downloader may do.
type Settings struct {
	Enabled    bool  `json:"enabled"`    // may connect to news servers and download
	Paused     bool  `json:"paused"`     // everything waits, without forgetting the queue
	SpeedLimit int64 `json:"speedLimit"` // KB/s, 0 for none
	Import     bool  `json:"import"`     // move finished downloads into the library
}

// LoadSettings reads the settings; with none saved, everything is off.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	var s Settings
	get := func(k string) (string, error) { return st.Setting(ctx, k) }
	v, err := get(SettingEnabled)
	if err != nil {
		return s, err
	}
	s.Enabled = v == "1"
	if v, err = get(SettingPaused); err != nil {
		return s, err
	}
	s.Paused = v == "1"
	if v, err = get(SettingImport); err != nil {
		return s, err
	}
	s.Import = v == "1"
	if v, err = get(SettingSpeedLimit); err != nil {
		return s, err
	}
	s.SpeedLimit, _ = strconv.ParseInt(v, 10, 64)
	return s, nil
}

// SaveSettings writes the settings.
func SaveSettings(ctx context.Context, st *store.Store, s Settings) error {
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return ""
	}
	limit := ""
	if s.SpeedLimit > 0 {
		limit = strconv.FormatInt(s.SpeedLimit, 10)
	}
	for k, v := range map[string]string{SettingEnabled: flag(s.Enabled), SettingPaused: flag(s.Paused), SettingImport: flag(s.Import), SettingSpeedLimit: limit} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}
