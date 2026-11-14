package api

import (
	"testing"

	"github.com/yeixio/boxoffice/internal/subtitles"
)

func TestSubtitleLoginKept(t *testing.T) {
	old := subtitles.Config{OpenSubtitles: subtitles.OpenSubtitlesLogin{APIKey: "key", Username: "me", Password: "pw"}}
	// The page sends no key or password back: both are kept.
	c := subtitlesInput{Config: subtitles.Config{OpenSubtitles: subtitles.OpenSubtitlesLogin{Username: "me"}}}.merge(old)
	if c.OpenSubtitles != old.OpenSubtitles {
		t.Fatalf("kept %+v", c.OpenSubtitles)
	}
	// Another account doesn't get the old password.
	c = subtitlesInput{Config: subtitles.Config{OpenSubtitles: subtitles.OpenSubtitlesLogin{Username: "you"}}}.merge(old)
	if c.OpenSubtitles.Password != "" || c.OpenSubtitles.APIKey != "key" {
		t.Fatalf("another account: %+v", c.OpenSubtitles)
	}
	// Forgetting removes it all.
	c = subtitlesInput{Config: subtitles.Config{OpenSubtitles: subtitles.OpenSubtitlesLogin{Username: "me"}}, Forget: true}.merge(old)
	if c.OpenSubtitles != (subtitles.OpenSubtitlesLogin{}) {
		t.Fatalf("forgot %+v", c.OpenSubtitles)
	}
}
