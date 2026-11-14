package subtitles

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// keptExts are the subtitle formats Box Office saves, best first. They're
// text, which Matinee shows without burning them into the picture.
var keptExts = []string{".srt", ".ass", ".ssa", ".vtt"}

// sidecarExts are subtitle files people keep beside videos.
var sidecarExts = []string{".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup"}

// OnDisk is the languages of the subtitle files beside a video, named
// after it as players expect: "Movie.en.srt", "Movie.eng.forced.srt",
// "Movie.English.sdh.srt". Forced ones (only the foreign parts) don't
// count, and nor does one that doesn't say its language.
func OnDisk(video string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Dir(video))
	if err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || !slices.Contains(sidecarExts, ext) || len(name) <= len(stem)+len(ext) || !strings.EqualFold(name[:len(stem)], stem) {
			continue
		}
		middle := name[len(stem) : len(name)-len(ext)]
		if middle[0] != '.' {
			continue // "Movie 2.en.srt" is "Movie 2.mkv"'s
		}
		lang, forced := "", false
		for _, part := range strings.FieldsFunc(middle, func(r rune) bool { return r == '.' || r == '_' || r == ' ' }) {
			switch strings.ToLower(part) {
			case "forced", "foreign":
				forced = true
			case "hi":
				if lang == "" {
					lang = "hi" // Hindi, unless it follows a language: "Movie.en.hi.srt"
				}
			default:
				if c := Code(part); c != "" && lang == "" {
					lang = c
				}
			}
		}
		if lang != "" && !forced {
			out[lang] = true
		}
	}
	return out, nil
}

// SidecarName is where a fetched subtitle goes: beside the video, named
// after it with its language, ".sdh" for one for the deaf and hard of
// hearing, and its format: "Movie (2001).en.srt".
func SidecarName(video, lang string, sdh bool, ext string) string {
	name := strings.TrimSuffix(video, filepath.Ext(video)) + "." + lang
	if sdh {
		name += ".sdh"
	}
	return name + ext
}

// Save writes a downloaded subtitle beside a video as UTF-8 and returns
// where. name is what the provider called it, for its format. A file
// already there is never replaced.
func Save(video, lang string, sdh bool, name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if !slices.Contains(keptExts, ext) {
		ext = ".srt"
	}
	text, err := ToUTF8(data, lang)
	if err != nil {
		return "", err
	}
	if !looksLike(text, ext) {
		return "", errors.New("the download isn't a subtitle file")
	}
	dest := SidecarName(video, lang, sdh, ext)
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("%s is already there", filepath.Base(dest))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(video), ".boxoffice-subtitle-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(text); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	os.Chmod(tmp.Name(), 0o644)
	// A link can't replace a file that's there, so one that appeared
	// meanwhile is kept.
	if err := os.Link(tmp.Name(), dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("%s is already there", filepath.Base(dest))
		}
		// Some shares can't link; a rename is next best.
		if err := os.Rename(tmp.Name(), dest); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
		return dest, nil
	}
	os.Remove(tmp.Name())
	return dest, nil
}

// looksLike checks a file is the subtitle format it's named as.
func looksLike(text []byte, ext string) bool {
	switch ext {
	case ".srt":
		return bytes.Contains(text, []byte("-->"))
	case ".vtt":
		return bytes.HasPrefix(bytes.TrimSpace(text), []byte("WEBVTT"))
	default: // .ass, .ssa
		return bytes.Contains(text, []byte("[Script Info]")) || bytes.Contains(text, []byte("Dialogue:"))
	}
}

// ToUTF8 turns a subtitle file into UTF-8 without a byte order mark.
// Files without one in UTF-16 or UTF-8 are taken to be in their language's
// usual Windows code page, as most older subtitles are.
func ToUTF8(data []byte, lang string) ([]byte, error) {
	var dec *encoding.Decoder
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		dec = unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		dec = unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder()
	case !utf8.Valid(data):
		dec = codePage(lang).NewDecoder()
	}
	if dec != nil {
		out, err := dec.Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("the subtitle's text couldn't be read: %w", err)
		}
		data = bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	}
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), nil
}

// codePage is the Windows code page a language's older subtitles are
// usually in.
func codePage(lang string) encoding.Encoding {
	switch lang {
	case "cs", "pl", "hu", "sk", "sl", "hr", "ro":
		return charmap.Windows1250
	case "ru", "uk", "bg", "sr", "mk":
		return charmap.Windows1251
	case "el":
		return charmap.Windows1253
	case "tr":
		return charmap.Windows1254
	case "he":
		return charmap.Windows1255
	case "ar", "fa", "ur":
		return charmap.Windows1256
	case "et", "lt", "lv":
		return charmap.Windows1257
	case "vi":
		return charmap.Windows1258
	case "th":
		return charmap.Windows874
	}
	return charmap.Windows1252
}
