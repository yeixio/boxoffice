package problems

import (
	"strings"

	"github.com/yeixio/boxoffice/internal/namefix"
)

// Names on disk aren't always written the way Box Office writes a title:
// accents written as a letter plus a combining mark (NFD), or garbled
// names (mojibake) such as "WALLÂ·E" for "WALL·E". package namefix has the
// details.

// Repair is a name as it was meant to be written. See namefix.Repair.
func Repair(name string) string { return namefix.Repair(name) }

// Garbled says a name is written wrong on disk. See namefix.Garbled.
func Garbled(name string) bool { return namefix.Garbled(name) }

// same says two names are the same once repaired, ignoring case.
func same(a, b string) bool {
	return strings.EqualFold(Repair(a), Repair(b))
}
