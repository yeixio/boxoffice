package gamedat

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// SFO is a PlayStation PARAM.SFO: the facts PlayStation Portable, 3, 4
// and Vita games carry about themselves, such as TITLE and TITLE_ID.
type SFO map[string]string

// ParseSFO reads a PARAM.SFO. Only its text values are kept.
func ParseSFO(b []byte) (SFO, error) {
	if len(b) < 20 || string(b[:4]) != "\x00PSF" {
		return nil, errors.New("not a PARAM.SFO")
	}
	keys := int(binary.LittleEndian.Uint32(b[8:]))
	data := int(binary.LittleEndian.Uint32(b[12:]))
	n := int(binary.LittleEndian.Uint32(b[16:]))
	out := SFO{}
	for i := range n {
		at := 20 + i*16
		if at+16 > len(b) {
			break
		}
		ko := keys + int(binary.LittleEndian.Uint16(b[at:]))
		format := binary.LittleEndian.Uint16(b[at+2:])
		length := int(binary.LittleEndian.Uint32(b[at+4:]))
		do := data + int(binary.LittleEndian.Uint32(b[at+12:]))
		if ko >= len(b) || do > len(b) || do+length > len(b) || length < 0 {
			continue
		}
		key := cString(b[ko:])
		if format == 0x0404 { // a number
			continue
		}
		out[key] = string(bytes.TrimRight(b[do:do+length], "\x00"))
	}
	return out, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
