package screen

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Duration reads how long a video runs from its own headers: the Segment
// Info of Matroska and WebM, or the movie header of MP4 and QuickTime.
// It's false for other formats, or when the headers don't say.
func Duration(path string) (time.Duration, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".webm":
		return mkvDuration(f)
	case ".mp4", ".m4v", ".mov":
		return mp4Duration(f)
	}
	return 0, false
}

// ---- Matroska ----

const (
	ebmlHeader    = 0x1A45DFA3
	ebmlSegment   = 0x18538067
	ebmlInfo      = 0x1549A966
	ebmlCluster   = 0x1F43B675
	ebmlTimescale = 0x2AD7B1
	ebmlDuration  = 0x4489
)

// readVint reads an EBML variable-length number. For IDs the length marker
// stays in; for sizes it's taken out, and all ones means unknown (-1).
func readVint(r io.ReaderAt, off int64, id bool) (val int64, n int, err error) {
	var b [8]byte
	if _, err := r.ReadAt(b[:1], off); err != nil {
		return 0, 0, err
	}
	l := 1
	for mask := byte(0x80); l <= 8 && b[0]&mask == 0; mask >>= 1 {
		l++
	}
	if l > 8 {
		return 0, 0, errors.New("bad vint")
	}
	if l > 1 {
		if _, err := r.ReadAt(b[1:l], off+1); err != nil {
			return 0, 0, err
		}
	}
	var v uint64
	for i := range l {
		v = v<<8 | uint64(b[i])
	}
	if id {
		return int64(v), l, nil
	}
	v &^= 1 << (7 * l) // the marker bit
	if v == 1<<(7*l)-1 {
		return -1, l, nil
	}
	return int64(v), l, nil
}

func mkvDuration(f *os.File) (time.Duration, bool) {
	info, err := f.Stat()
	if err != nil {
		return 0, false
	}
	end := info.Size()
	// element reads an element's ID and size at off; data starts at off+n.
	element := func(off int64) (id, size int64, n int, err error) {
		id, a, err := readVint(f, off, true)
		if err != nil {
			return 0, 0, 0, err
		}
		size, b, err := readVint(f, off+int64(a), false)
		return id, size, a + b, err
	}
	id, size, n, err := element(0)
	if err != nil || id != ebmlHeader || size < 0 {
		return 0, false
	}
	off := int64(n) + size
	id, size, n, err = element(off)
	if err != nil || id != ebmlSegment {
		return 0, false
	}
	off += int64(n)
	segEnd := end
	if size >= 0 {
		segEnd = min(end, off+size)
	}
	for i := 0; off < segEnd && i < 10000; i++ {
		id, size, n, err = element(off)
		if err != nil || size < 0 || id == ebmlCluster {
			return 0, false
		}
		if id == ebmlInfo {
			return mkvInfo(f, off+int64(n), size, element)
		}
		off += int64(n) + size
	}
	return 0, false
}

func mkvInfo(f *os.File, off, size int64, element func(int64) (int64, int64, int, error)) (time.Duration, bool) {
	scale := int64(1_000_000)
	var dur float64
	for end := off + size; off < end; {
		id, sz, n, err := element(off)
		if err != nil || sz < 0 || sz > 16 {
			if err != nil || sz < 0 {
				return 0, false
			}
			off += int64(n) + sz
			continue
		}
		b := make([]byte, sz)
		if _, err := f.ReadAt(b, off+int64(n)); err != nil {
			return 0, false
		}
		switch id {
		case ebmlTimescale:
			var v int64
			for _, x := range b {
				v = v<<8 | int64(x)
			}
			if v > 0 {
				scale = v
			}
		case ebmlDuration:
			switch sz {
			case 4:
				dur = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
			case 8:
				dur = math.Float64frombits(binary.BigEndian.Uint64(b))
			}
		}
		off += int64(n) + sz
	}
	if dur <= 0 || math.IsNaN(dur) || math.IsInf(dur, 0) {
		return 0, false
	}
	return time.Duration(dur * float64(scale)), true
}

// ---- MP4 ----

// box finds the box named typ among the boxes from off to end, and says
// where its content starts and ends.
func box(f *os.File, off, end int64, typ string) (int64, int64, bool) {
	var h [16]byte
	for i := 0; off+8 <= end && i < 10000; i++ {
		if _, err := f.ReadAt(h[:8], off); err != nil {
			return 0, 0, false
		}
		size, head := int64(binary.BigEndian.Uint32(h[:4])), int64(8)
		switch size {
		case 0:
			size = end - off
		case 1:
			if _, err := f.ReadAt(h[8:16], off+8); err != nil {
				return 0, 0, false
			}
			size, head = int64(binary.BigEndian.Uint64(h[8:16])), 16
		}
		if size < head {
			return 0, 0, false
		}
		if string(h[4:8]) == typ {
			return off + head, min(end, off+size), true
		}
		off += size
	}
	return 0, 0, false
}

func mp4Duration(f *os.File) (time.Duration, bool) {
	info, err := f.Stat()
	if err != nil {
		return 0, false
	}
	start, end, ok := box(f, 0, info.Size(), "moov")
	if !ok {
		return 0, false
	}
	start, _, ok = box(f, start, end, "mvhd")
	if !ok {
		return 0, false
	}
	var b [32]byte
	if _, err := f.ReadAt(b[:], start); err != nil {
		return 0, false
	}
	var scale, dur uint64
	if b[0] == 1 {
		scale, dur = uint64(binary.BigEndian.Uint32(b[20:24])), binary.BigEndian.Uint64(b[24:32])
	} else {
		scale, dur = uint64(binary.BigEndian.Uint32(b[12:16])), uint64(binary.BigEndian.Uint32(b[16:20]))
	}
	if scale == 0 || dur == 0 {
		return 0, false
	}
	return time.Duration(float64(dur) / float64(scale) * float64(time.Second)), true
}
