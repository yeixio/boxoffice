package problems

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// Names on disk aren't always written the way Box Office writes a title.
// Two kinds of difference show up in real libraries, and neither should
// make a title's folder look like something that isn't on the list:
//
//   - accents written as a letter plus a combining mark (NFD), as macOS and
//     some SMB clients write them, against the single letter (NFC) the
//     title has: "TÁR" looks the same either way but isn't the same bytes;
//   - garbled names (mojibake), where a tool read UTF-8 as Windows-1252
//     and wrote it back: "WALL·E" became "WALLÂ·E", "Léon" "LÃ©on".

// cp1252 maps the runes Windows-1252 gives bytes 0x80–0x9F to those bytes.
var cp1252 = func() map[rune]byte {
	m := map[rune]byte{}
	for b := 0x80; b <= 0x9f; b++ {
		if r := charmap.Windows1252.DecodeByte(byte(b)); r != utf8.RuneError {
			m[r] = byte(b)
		}
	}
	return m
}()

// Repair is a name as it was meant to be written: a garbled name turned
// back, and accents written the one way. A name that isn't garbled comes
// back as it is, but for its accents.
func Repair(name string) string {
	if !utf8.ValidString(name) {
		// Latin-1 or Windows-1252 bytes, not UTF-8 at all.
		if s, err := charmap.Windows1252.NewDecoder().String(name); err == nil {
			name = s
		}
	}
	// Twice, for a name garbled twice over.
	for range 2 {
		s, ok := ungarble(name)
		if !ok {
			break
		}
		name = s
	}
	return norm.NFC.String(name)
}

// ungarble turns back a name that's UTF-8 read as Windows-1252: each rune
// back to its byte, as long as every rune is one Windows-1252 has and the
// bytes are UTF-8 with something beyond ASCII in it.
func ungarble(s string) (string, bool) {
	var b []byte
	multi := false
	for _, r := range s {
		switch {
		case r < 0x80:
			b = append(b, byte(r))
		case r < 0x100:
			b = append(b, byte(r))
			multi = true
		default:
			c, ok := cp1252[r]
			if !ok {
				return "", false
			}
			b = append(b, c)
			multi = true
		}
	}
	if !multi || !utf8.Valid(b) {
		return "", false
	}
	out := string(b)
	return out, out != s
}

// Garbled says a name is written wrong on disk: Repair changes more than
// how its accents are written.
func Garbled(name string) bool {
	return Repair(name) != norm.NFC.String(name)
}

// same says two names are the same once repaired, ignoring case.
func same(a, b string) bool {
	return strings.EqualFold(Repair(a), Repair(b))
}
