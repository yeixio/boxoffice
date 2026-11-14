package subtitles

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Query is what's known about a video file to find its subtitles.
type Query struct {
	Movie bool // a movie, else an episode
	// TMDBID is the movie's, or the episode's show's; IMDBID likewise.
	TMDBID  int64
	IMDBID  string
	Title   string // the movie's, or the show's
	Year    int
	Season  int
	Episode int
	// Hash is the file's OpenSubtitles hash (see FileHash): subtitles made
	// for this very file match it.
	Hash string
	// Release is the name the file came with: its download's, or its own.
	Release string
	// Languages are ISO 639-1 codes.
	Languages []string
}

// Found is a subtitle a provider has for a video.
type Found struct {
	Provider string
	ID       string // the provider's, to download it
	Language string // ISO 639-1
	Release  string // the release names it was made for, space-separated
	SDH      bool   // for the deaf and hard of hearing
	Forced   bool   // only the foreign parts
	Machine  bool   // machine or AI translated
	// MatchesFile is true when it was made for this very file, so its
	// timing fits.
	MatchesFile bool
	Trusted     bool
	Downloads   int
}

// Provider is a site subtitles come from.
type Provider interface {
	// Name is the provider's id: opensubtitles or podnapisi.
	Name() string
	// Search finds subtitles for a video in its query's languages.
	Search(ctx context.Context, q Query) ([]Found, error)
	// Download fetches one, as subtitle text, with the name it had (for
	// its format).
	Download(ctx context.Context, f Found) (data []byte, name string, err error)
}

// ErrQuota means a provider's downloads for today are used up.
var ErrQuota = errors.New("today's downloads are used up")

// maxSubtitleSize is far more than any film's subtitles need.
const maxSubtitleSize = 5 << 20

// httpClient is what providers use when they're given none.
var httpClient = &http.Client{Timeout: 30 * time.Second}
