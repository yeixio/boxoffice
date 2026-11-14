package subtitles

import (
	"context"
	"slices"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Status is where subtitle fetching stands, for Settings.
type Status struct {
	Running bool   `json:"running"`
	Done    int    `json:"done"` // files this pass has looked up
	Error   string `json:"error,omitempty"`
	// FFprobe says whether ffprobe is there to look inside files.
	FFprobe bool `json:"ffprobe"`
	Counts  `json:"counts"`
	Today   Today     `json:"today"`
	Recent  []Fetched `json:"recent"`
}

// Counts are the library's files by how their subtitles stand. A file is
// counted once: complete when it has every language, else by what its
// missing ones are waiting on.
type Counts struct {
	Files    int `json:"files"`    // movie and episode files, not counting Do not touch ones
	Complete int `json:"complete"` // has subtitles in every language
	Missing  int `json:"missing"`  // looked for, not found yet
	Waiting  int `json:"waiting"`  // found, to fetch once a provider can
	Pending  int `json:"pending"`  // not looked at yet
	Fetched  int `json:"fetched"`  // subtitles Box Office downloaded
}

// Today is the day's lookups against the limit.
type Today struct {
	Looked    int                 `json:"looked"`
	Limit     int                 `json:"limit"`
	Providers map[string]Provided `json:"providers"`
}

// Provided is a day's use of one provider.
type Provided struct {
	store.SubtitleUse
	// UsedUp means its downloads for today ran out.
	UsedUp bool `json:"usedUp,omitempty"`
}

// Fetched is a subtitle Box Office downloaded, for the list in Settings.
type Fetched struct {
	Kind     string    `json:"kind"`    // movie or series
	TitleID  int64     `json:"titleId"` // the movie or the show
	Title    string    `json:"title"`
	Season   int       `json:"season,omitempty"`
	Episode  int       `json:"episode,omitempty"`
	Language string    `json:"language"`
	Provider string    `json:"provider"`
	At       time.Time `json:"at"`
}

// maxRecent is how many fetched subtitles Settings lists.
const maxRecent = 20

// State is the status, with counts from the database.
func (s *Service) State(ctx context.Context, c Config) (Status, error) {
	s.mu.Lock()
	st := Status{Running: s.running, Done: s.done, Error: s.lastErr, Recent: []Fetched{}}
	s.mu.Unlock()
	st.FFprobe = s.Probe != nil || s.ffprobe() != ""
	files, err := s.Store.SubtitleFiles(ctx)
	if err != nil {
		return st, err
	}
	rows, err := s.Store.Subtitles(ctx)
	if err != nil {
		return st, err
	}
	for _, f := range files {
		if f.Locked {
			continue
		}
		st.Files++
		have := map[string]string{}
		for _, r := range rows[f.ID] {
			have[r.Language] = r.State
			if r.State == store.SubFetched {
				st.Fetched++
				kind := "movie"
				if f.Kind == "episode" {
					kind = "series"
				}
				st.Recent = append(st.Recent, Fetched{Kind: kind, TitleID: f.TitleID, Title: f.Title, Season: f.Season, Episode: f.Episode,
					Language: r.Language, Provider: r.Provider, At: r.CheckedAt})
			}
		}
		pending, missing, waiting := false, false, false
		for _, l := range c.Languages {
			switch v, ok := have[l]; {
			case !ok:
				pending = true
			case v == store.SubMissing:
				missing = true
			case v == store.SubWaiting:
				waiting = true
			}
		}
		switch {
		case pending:
			st.Pending++
		case waiting:
			st.Waiting++
		case missing:
			st.Missing++
		default:
			st.Complete++
		}
	}
	slices.SortFunc(st.Recent, func(a, b Fetched) int { return b.At.Compare(a.At) })
	st.Recent = st.Recent[:min(len(st.Recent), maxRecent)]

	use, err := s.Store.SubtitleUseOn(ctx, store.DataDay(s.now()))
	if err != nil {
		return st, err
	}
	st.Today = Today{Looked: use[""].Searches, Limit: c.DailyLimit, Providers: map[string]Provided{}}
	for _, p := range []string{"opensubtitles", "podnapisi"} {
		st.Today.Providers[p] = Provided{SubtitleUse: use[p], UsedUp: s.isUsedUp(p)}
	}
	return st, nil
}

// For is a file's subtitles in each language wanted, in the settings'
// order: what's known, or "pending" for one not looked at yet.
func For(c Config, known []store.Subtitle) []store.Subtitle {
	out := make([]store.Subtitle, 0, len(c.Languages))
	for _, l := range c.Languages {
		i := slices.IndexFunc(known, func(x store.Subtitle) bool { return x.Language == l })
		if i < 0 {
			out = append(out, store.Subtitle{Language: l, State: "pending"})
			continue
		}
		x := known[i]
		x.Path = "" // the page shows the video's path already
		out = append(out, x)
	}
	return out
}
