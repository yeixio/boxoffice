// Package namefix repairs titles read from file and folder names.
//
// Names on disk aren't always written the way a title is. Two kinds of
// difference show up in real libraries:
//
//   - accents written as a letter plus a combining mark (NFD), as macOS
//     and some SMB clients write them, against the single letter (NFC)
//     everything else uses: "Amélie" looks the same either way but isn't
//     the same bytes, so it doesn't match the title online;
//   - garbled names (mojibake), where a tool read UTF-8 as Latin-1 or
//     Windows-1252 and wrote it back: "Æon Flux" became "Ã\u0086on Flux"
//     or "Ã†on Flux", "Léon" "LÃ©on".
package namefix

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

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
// back, and accents written the one way (NFC). A name that isn't garbled
// comes back as it is, but for how its accents are written.
func Repair(name string) string {
	if !utf8.ValidString(name) {
		// Latin-1 or Windows-1252 bytes, not UTF-8 at all.
		if s, err := charmap.Windows1252.NewDecoder().String(name); err == nil {
			name = s
		}
	}
	// A garbled name may have had its accents split since, as "Ã" into
	// "A" and a combining tilde; join them up first.
	name = norm.NFC.String(name)
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

// ungarble turns back a name that's UTF-8 read as Windows-1252 or Latin-1:
// each rune back to its byte, as long as every rune is one those have and
// the bytes are UTF-8 with something beyond ASCII in it.
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

// Fold is a name for comparing: repaired, in lower case and without
// accents, so "Amélie", "Amélie" and "Amelie" are the same.
func Fold(name string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(Repair(name)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		switch r {
		case 'æ', 'Æ':
			b.WriteString("ae")
			continue
		case 'œ', 'Œ':
			b.WriteString("oe")
			continue
		case 'ø', 'Ø':
			r = 'o'
		case 'ß':
			b.WriteString("ss")
			continue
		case 'ł', 'Ł':
			r = 'l'
		case 'đ', 'Đ':
			r = 'd'
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Garbled says a name is written wrong on disk: Repair changes more than
// how its accents are written.
func Garbled(name string) bool {
	return Repair(name) != norm.NFC.String(name)
}
