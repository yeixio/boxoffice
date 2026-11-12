package gamedat

import (
	"strings"
	"testing"
)

// libretro leaves quotes inside some values unescaped.
func TestParseClrMameStrayQuotes(t *testing.T) {
	dat, err := ParseClrMame(strings.NewReader(`game (
	comment "Golden Sun (USA)"
	developer ""Natsume", Vicarious Visions"
	rom ( crc 12345678 )
)
game ( comment "Next" developer "Camelot" )
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(dat.Games) != 2 || dat.Games[0].Fields["developer"] != `"Natsume", Vicarious Visions` || dat.Games[1].Fields["developer"] != "Camelot" {
		t.Fatalf("%+v", dat.Games)
	}
}
