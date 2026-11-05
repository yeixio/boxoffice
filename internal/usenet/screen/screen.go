// Package screen looks a finished download over for what has no place in
// a movie or TV release: programs and scripts, files dressed up as
// videos, and releases with no video at all. Box Office never runs or
// opens what it downloads, but people do, so these are quarantined
// rather than imported.
package screen

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A Finding is one file that shouldn't be there, and why.
type Finding struct {
	File string `json:"file"`
	Why  string `json:"why"`
}

// Programs, scripts and shortcuts that run when opened, on Windows, macOS,
// Linux or Android.
var risky = []string{
	".exe", ".scr", ".com", ".pif", ".bat", ".cmd", ".lnk", ".js", ".jse", ".vbs", ".vbe", ".wsf", ".wsh",
	".ps1", ".psm1", ".msi", ".msp", ".jar", ".hta", ".cpl", ".reg", ".dll", ".sys", ".application", ".gadget",
	".apk", ".app", ".dmg", ".pkg", ".command", ".sh", ".run", ".desktop", ".appimage",
}

var videoExts = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mts", ".wmv", ".mov", ".mpg", ".mpeg", ".webm", ".vob", ".iso", ".divx", ".flv"}

// IsVideo says whether a name is a video file's.
func IsVideo(name string) bool {
	return slices.Contains(videoExts, strings.ToLower(filepath.Ext(name)))
}

// Check looks over files (names in dir). wantVideo is set when the release
// is for a movie or episode, so one with no video in it is fake.
func Check(dir string, files []string, wantVideo bool) []Finding {
	var out []Finding
	videos := 0
	for _, f := range files {
		p := filepath.Join(dir, f)
		base := strings.ToLower(filepath.Base(f))
		ext := filepath.Ext(base)
		inner := filepath.Ext(strings.TrimSuffix(base, ext))
		switch {
		case slices.Contains(risky, ext) && IsVideo(inner):
			out = append(out, Finding{f, fmt.Sprintf("it's a %s file pretending to be a video", ext)})
			continue
		case slices.Contains(risky, ext):
			out = append(out, Finding{f, fmt.Sprintf("%s files are programs or scripts, not part of a movie or TV release", ext)})
			continue
		}
		head := readHead(p)
		if what := program(head); what != "" {
			out = append(out, Finding{f, "it's " + what + " under another name"})
			continue
		}
		if IsVideo(base) {
			if what := archive(head); what != "" {
				out = append(out, Finding{f, "it's named like a video but it's " + what})
				continue
			}
			videos++
		}
	}
	if wantVideo && videos == 0 && len(out) == 0 {
		out = append(out, Finding{"", "there's no video in it, so it isn't the release it said it was"})
	}
	return out
}

func readHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, 4096)
	n, _ := io.ReadFull(f, b)
	return b[:n]
}

// program says what kind of program b starts, or "".
func program(b []byte) string {
	switch {
	case len(b) >= 0x40 && b[0] == 'M' && b[1] == 'Z':
		off := int(binary.LittleEndian.Uint32(b[0x3c:]))
		if off+4 <= len(b) && string(b[off:off+4]) == "PE\x00\x00" {
			return "a Windows program"
		}
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return "a Linux program"
	case bytes.HasPrefix(b, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(b, []byte{0xce, 0xfa, 0xed, 0xfe}),
		bytes.HasPrefix(b, []byte{0xca, 0xfe, 0xba, 0xbe}) && len(b) > 7 && b[7] < 0x20:
		return "a Mac program"
	case bytes.HasPrefix(b, []byte("#!")):
		return "a script"
	case bytes.HasPrefix(b, []byte{0x4c, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00}):
		return "a Windows shortcut"
	}
	return ""
}

// archive says what kind of archive b starts, or "".
func archive(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("Rar!\x1a\x07")):
		return "a RAR archive"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		return "a ZIP archive"
	case bytes.HasPrefix(b, []byte("7z\xbc\xaf\x27\x1c")):
		return "a 7-Zip archive"
	}
	return ""
}

// Summary says what was found, in a sentence.
func Summary(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	first := fs[0]
	s := first.Why
	if first.File != "" {
		s = filepath.Base(first.File) + ": " + first.Why
	}
	if len(fs) > 1 {
		s += fmt.Sprintf(" (and %d more)", len(fs)-1)
	}
	return s
}
