package unpack

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

// Locked says whether a file is an archive, or a volume of one, that can't
// be opened without a password. It reads only the first headers, so it can
// tell from any one volume of a set, as soon as that volume is downloaded,
// long before the rest are. It's false for anything else, including an
// archive it can't read the headers of.
//
// It knows RAR (1.5 to 7) and ZIP. A 7-Zip archive keeps its headers at
// the end, so one is only found out when it's unpacked.
func Locked(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sig := make([]byte, 8)
	if _, err := io.ReadFull(f, sig); err != nil {
		return false
	}
	switch {
	case bytes.Equal(sig, sigRAR5):
		return lockedRAR5(bufio.NewReader(f))
	case bytes.Equal(sig[:7], sigRAR4):
		if _, err := f.Seek(7, io.SeekStart); err != nil {
			return false
		}
		return lockedRAR4(f)
	case bytes.Equal(sig[:4], []byte("PK\x03\x04")):
		// A local file header's general purpose flags: bit 0 is encrypted.
		return binary.LittleEndian.Uint16(sig[6:])&0x1 != 0
	}
	return false
}

// lockedRAR5 walks the headers to the first file: an archive encryption
// header means every header is encrypted, and a file header's encryption
// record means its data is.
func lockedRAR5(r *bufio.Reader) bool {
	for range 64 {
		if _, err := r.Discard(4); err != nil { // CRC
			return false
		}
		size, err := readVint(r)
		if err != nil || size == 0 || size > 2<<20 {
			return false
		}
		head := make([]byte, size)
		if _, err := io.ReadFull(r, head); err != nil {
			return false
		}
		h := bytes.NewReader(head)
		typ, err1 := readVint(h)
		flags, err2 := readVint(h)
		if err1 != nil || err2 != nil {
			return false
		}
		var extra, data uint64
		if flags&0x1 != 0 {
			if extra, err = readVint(h); err != nil {
				return false
			}
		}
		if flags&0x2 != 0 {
			if data, err = readVint(h); err != nil {
				return false
			}
		}
		switch typ {
		case 4: // archive encryption header
			return true
		case 5: // end of archive
			return false
		case 2: // file
			if extra == 0 || extra > size {
				return false
			}
			return hasRecord(head[size-extra:], 0x01)
		}
		// The main header, or a service header (a comment, say): on to the
		// next.
		if data > 0 {
			if _, err := r.Discard(int(min(data, 1<<40))); err != nil {
				return false
			}
		}
	}
	return false
}

// hasRecord says whether a RAR 5 extra area holds a record of a type.
func hasRecord(extra []byte, want uint64) bool {
	r := bytes.NewReader(extra)
	for r.Len() > 0 {
		size, err := readVint(r)
		if err != nil || size == 0 || size > uint64(r.Len()) {
			return false
		}
		start := r.Len()
		typ, err := readVint(r)
		if err != nil {
			return false
		}
		if typ == want {
			return true
		}
		if _, err := r.Seek(int64(size)-int64(start-r.Len()), io.SeekCurrent); err != nil {
			return false
		}
	}
	return false
}

// lockedRAR4 reads the main header's flags, then walks the blocks to the
// first file header and reads its flags.
func lockedRAR4(f *os.File) bool {
	var h [7]byte
	if _, err := io.ReadFull(f, h[:]); err != nil || h[2] != 0x73 {
		return false
	}
	if binary.LittleEndian.Uint16(h[3:])&0x0080 != 0 { // MHD_PASSWORD
		return true
	}
	pos := 7 + int64(binary.LittleEndian.Uint16(h[5:]))
	for range 64 {
		var b [11]byte
		if _, err := f.ReadAt(b[:7], pos); err != nil {
			return false
		}
		typ := b[2]
		bf := binary.LittleEndian.Uint16(b[3:])
		hsize := int64(binary.LittleEndian.Uint16(b[5:]))
		if hsize < 7 {
			return false
		}
		switch typ {
		case 0x74: // file
			return bf&0x0004 != 0 // LHD_PASSWORD
		case 0x7b: // end of archive
			return false
		}
		data := int64(0)
		if bf&0x8000 != 0 || typ == 0x7a {
			if _, err := f.ReadAt(b[7:11], pos+7); err != nil {
				return false
			}
			data = int64(binary.LittleEndian.Uint32(b[7:11]))
		}
		pos += hsize + data
	}
	return false
}
