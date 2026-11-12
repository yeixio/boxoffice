package gamedat

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// CHDSHA1 reads the SHA-1 a CHD file has written in its header: the one
// MAME's list gives for the disk. Only the header is read.
func CHDSHA1(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 124)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	head = head[:n]
	if len(head) < 16 || string(head[:8]) != "MComprHD" {
		return "", errors.New("not a CHD file")
	}
	// The SHA-1 of the whole disk, with its metadata, is where each
	// version of the format put it.
	var at int
	switch binary.BigEndian.Uint32(head[12:16]) {
	case 3:
		at = 80
	case 4:
		at = 48
	case 5:
		at = 84
	default:
		return "", errors.New("a CHD version this can't read")
	}
	if len(head) < at+20 {
		return "", errors.New("the CHD file is cut short")
	}
	return hex.EncodeToString(head[at : at+20]), nil
}
