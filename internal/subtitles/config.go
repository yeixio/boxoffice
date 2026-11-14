package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/store"
)

// settingConfig is where Settings → Subtitles is kept.
const settingConfig = "subtitles"

// Config is what admins set in Settings → Subtitles.
type Config struct {
	// On fetches subtitles for new and existing movies and episodes.
	On bool `json:"on"`
	// Languages are ISO 639-1 codes, most wanted first.
	Languages []string `json:"languages"`
	// PreferSDH picks subtitles for the deaf and hard of hearing, with
	// sounds and who's speaking, over plain ones when both fit.
	PreferSDH bool `json:"preferSdh"`
	// DailyLimit is how many files a day are looked up, so a large
	// library is gone through slowly and providers aren't hammered. New
	// downloads go first.
	DailyLimit int `json:"dailyLimit"`
	// Podnapisi needs no account, so it's on unless turned off.
	Podnapisi     bool               `json:"podnapisi"`
	OpenSubtitles OpenSubtitlesLogin `json:"openSubtitles"`
}

// OpenSubtitlesLogin is an opensubtitles.com API key, and the account
// that raises how many files can be downloaded a day.
type OpenSubtitlesLogin struct {
	APIKey   string `json:"apiKey,omitempty"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// Defaults.
const (
	DefaultDailyLimit = 300
	MaxDailyLimit     = 5000
	MaxLanguages      = 10
)

// Normalize fills in defaults and tidies what was typed.
func (c *Config) Normalize() {
	var langs []string
	for _, l := range c.Languages {
		if code := Code(l); code != "" && !slices.Contains(langs, code) {
			langs = append(langs, code)
		}
	}
	if langs == nil {
		langs = []string{}
	}
	c.Languages = langs
	if c.DailyLimit <= 0 {
		c.DailyLimit = DefaultDailyLimit
	}
	c.DailyLimit = min(c.DailyLimit, MaxDailyLimit)
	o := &c.OpenSubtitles
	o.APIKey, o.Username = strings.TrimSpace(o.APIKey), strings.TrimSpace(o.Username)
}

// Check says what's wrong with the settings, in words for the admin.
func (c Config) Check() error {
	if len(c.Languages) > MaxLanguages {
		return errors.New("pick at most 10 languages")
	}
	if c.On && len(c.Languages) == 0 {
		return errors.New("pick the languages you want subtitles in")
	}
	o := c.OpenSubtitles
	if o.Username != "" && o.APIKey == "" {
		return errors.New("enter the OpenSubtitles API key too: the account alone isn't enough")
	}
	if o.Username != "" && o.Password == "" {
		return errors.New("enter the OpenSubtitles password")
	}
	if c.On && !c.Podnapisi && o.APIKey == "" {
		return errors.New("turn on Podnapisi or enter an OpenSubtitles API key, so there's somewhere to look")
	}
	return nil
}

// LoadConfig reads the settings. Until they're saved, subtitles are off
// and Podnapisi is on.
func LoadConfig(ctx context.Context, st *store.Store) (Config, error) {
	c := Config{Podnapisi: true}
	v, err := st.Setting(ctx, settingConfig)
	if err != nil {
		return c, err
	}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &c); err != nil {
			return c, err
		}
	}
	c.Normalize()
	return c, nil
}

// SaveConfig stores the settings.
func SaveConfig(ctx context.Context, st *store.Store, c Config) error {
	c.Normalize()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, settingConfig, string(b))
}
