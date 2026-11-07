package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// checkLanguage turns down a replacement for a copy reported in Matinee as
// being in the wrong language, when its sound turns out to be in another
// language again: its audio tracks are tagged, and none is in the language
// asked for. Tracks that aren't tagged can't tell, so they pass. The error
// fails the download, so its release is blocked and another looked for.
func (e *Engine) checkLanguage(ctx context.Context, d *store.Download, dir string, files []string) error {
	lang := e.wantedLanguage(ctx, d)
	if lang == "" {
		return nil
	}
	probe := e.AudioLanguages
	if probe == nil {
		probe = ffprobeLanguages
	}
	for _, v := range mainVideos(dir, files, d.Kind == "series") {
		langs, err := probe(ctx, filepath.Join(dir, v.name))
		if err != nil || len(langs) == 0 || slices.Contains(langs, lang) {
			continue
		}
		names := make([]string, len(langs))
		for i, l := range langs {
			names[i] = quality.LanguageName(l)
		}
		return hopeless(fmt.Errorf("its sound is in %s, and %s was asked for", strings.Join(names, " and "), quality.LanguageName(lang)))
	}
	return nil
}

// wantedLanguage is the language a download's sound must be in: the one
// asked for when its movie, or one of its episodes, was reported in
// Matinee as being in the wrong language; "" for any.
func (e *Engine) wantedLanguage(ctx context.Context, d *store.Download) string {
	var wants []store.TitleWant
	switch d.Kind {
	case "movie":
		if tw, err := e.Store.TitleWant(ctx, "movie", d.TitleID); err == nil {
			wants = append(wants, tw)
		}
	case "series":
		for _, id := range d.EpisodeIDs {
			if tw, err := e.Store.TitleWant(ctx, "episode", id); err == nil {
				wants = append(wants, tw)
			}
		}
	}
	for _, tw := range wants {
		if tw.Want == store.WantReplace && tw.Problem == store.ProblemLanguage && tw.Language != "" {
			return tw.Language
		}
	}
	return ""
}

// ffprobeLanguages lists the languages (ISO 639-1) a video's audio tracks
// are tagged with, leaving out untagged ones and any Box Office doesn't
// know. Without ffprobe it lists none.
func ffprobeLanguages(ctx context.Context, path string) ([]string, error) {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-v", "error", "-select_streams", "a", "-show_entries", "stream_tags=language",
		"-of", "json", path).Output()
	if err != nil {
		return nil, err
	}
	var r struct {
		Streams []struct {
			Tags map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	var langs []string
	for _, s := range r.Streams {
		if l := quality.LanguageCode(s.Tags["language"]); l != "" && !slices.Contains(langs, l) {
			langs = append(langs, l)
		}
	}
	return langs, nil
}
