// Package yenc decodes yEnc, the encoding nearly every Usenet binary is
// posted in.
//
// An article's body is a "=ybegin" line, a "=ypart" line for multi-part
// posts, the encoded data, and a "=yend" line with the part's size and
// CRC-32. Each data byte is the original plus 42; the few results that
// would break a line ('\0', '\r', '\n', '=') are written as '=' followed by
// the byte plus 64.
//
// The decoder works eight bytes at a time (SWAR, SIMD within a register):
// a word with no '=', '\r' or '\n' in it is decoded with one subtraction,
// which is nearly every word of a typical article.
package yenc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
)

// Part is one decoded article.
type Part struct {
	Name   string // the file's name, from =ybegin
	Number int    // which part, from 1; 0 for a single-part post
	Total  int    // how many parts, when the poster said
	Size   int64  // the whole file's size
	Begin  int64  // where in the file this part goes, from 0
	Data   []byte // the decoded bytes
	CRC    uint32 // the CRC-32 =yend gave, when it gave one
	HasCRC bool
}

// End is where this part's data ends in the file.
func (p *Part) End() int64 { return p.Begin + int64(len(p.Data)) }

// ErrNoYEnc means the article isn't yEnc.
var ErrNoYEnc = errors.New("the article isn't yEnc")

// ErrCRC means the decoded data doesn't match the CRC the poster gave, so
// part of the article was lost or changed.
var ErrCRC = errors.New("the article's data doesn't match its checksum")

// Decode decodes an article body, after the NNTP dot-stuffing is undone.
// It returns the part even with ErrCRC, since damaged data can still be
// repaired with par2.
func Decode(body []byte) (*Part, error) {
	start := bytes.Index(body, []byte("=ybegin "))
	if start < 0 {
		return nil, ErrNoYEnc
	}
	line, rest := cutLine(body[start:])
	p := &Part{}
	begin := attrs(line)
	p.Name = begin.name
	p.Size = begin.int("size")
	p.Number = int(begin.int("part"))
	p.Total = int(begin.int("total"))
	if bytes.HasPrefix(rest, []byte("=ypart ")) {
		line, rest = cutLine(rest)
		a := attrs(line)
		if b := a.int("begin"); b > 0 {
			p.Begin = b - 1
		}
	}
	end := bytes.LastIndex(rest, []byte("\n=yend"))
	var trailer []byte
	if end >= 0 {
		trailer, _ = cutLine(rest[end+1:])
		rest = rest[:end+1]
	} else if bytes.HasPrefix(rest, []byte("=yend")) {
		trailer, _ = cutLine(rest)
		rest = nil
	} else {
		return nil, errors.New("the article ends before its =yend line")
	}
	p.Data = make([]byte, len(rest))
	n := DecodeInto(p.Data, rest)
	p.Data = p.Data[:n]
	if p.Number == 0 && p.Size == 0 {
		p.Size = int64(n)
	}
	t := attrs(trailer)
	key := "crc32"
	if p.Number > 0 {
		key = "pcrc32"
	}
	if v, ok := t.vals[key]; ok {
		if c, err := strconv.ParseUint(v, 16, 32); err == nil {
			p.CRC, p.HasCRC = uint32(c), true
		}
	}
	if want := t.int("size"); want > 0 && want != int64(n) {
		return p, fmt.Errorf("%w (%d bytes, but =yend says %d)", ErrCRC, n, want)
	}
	if p.HasCRC && crc32.ChecksumIEEE(p.Data) != p.CRC {
		return p, ErrCRC
	}
	return p, nil
}

const (
	ones  uint64 = 0x0101010101010101
	highs uint64 = 0x8080808080808080
	lows  uint64 = 0x7f7f7f7f7f7f7f7f
	forty uint64 = 42 * ones
)

// hasByte reports whether any byte of x is b, by the classic test for a
// zero byte in x ^ (b repeated).
func hasByte(x uint64, b byte) bool {
	y := x ^ (uint64(b) * ones)
	return (y-ones)&^y&highs != 0
}

// sub42 takes 42 from every byte of x, each wrapping on its own.
func sub42(x uint64) uint64 {
	return ((x | highs) - (forty & lows)) ^ ((x ^ ^forty) & highs)
}

// DecodeInto decodes yEnc data (no header or trailer lines) into dst, which
// must be at least len(src) long, and says how many bytes it wrote.
func DecodeInto(dst, src []byte) int {
	n := 0
	i := 0
	for i < len(src) {
		// The fast path: eight plain bytes at once.
		for i+8 <= len(src) {
			w := binary.LittleEndian.Uint64(src[i:])
			if hasByte(w, '=') || hasByte(w, '\n') || hasByte(w, '\r') {
				break
			}
			binary.LittleEndian.PutUint64(dst[n:], sub42(w))
			i += 8
			n += 8
		}
		// The slow path, until the next clean word.
		for stop := min(i+8, len(src)); i < stop; i++ {
			c := src[i]
			switch c {
			case '\r', '\n':
				continue
			case '=':
				i++
				if i >= len(src) {
					return n
				}
				c = src[i] - 64
			}
			dst[n] = c - 42
			n++
		}
	}
	return n
}

func cutLine(b []byte) (line, rest []byte) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return bytes.TrimRight(b, "\r"), nil
	}
	return bytes.TrimRight(b[:i], "\r"), b[i+1:]
}

type attrSet struct {
	vals map[string]string
	name string
}

func (a attrSet) int(k string) int64 {
	v, _ := strconv.ParseInt(a.vals[k], 10, 64)
	return v
}

// attrs reads a "=ybegin key=value ... name=the rest" line. The name runs
// to the end of the line, spaces and all.
func attrs(line []byte) attrSet {
	a := attrSet{vals: map[string]string{}}
	s := string(line)
	if i := indexName(s); i >= 0 {
		a.name = s[i+5:]
		s = s[:i]
	}
	for _, f := range bytes.Fields([]byte(s)) {
		if k, v, ok := bytes.Cut(f, []byte("=")); ok && len(k) > 0 {
			a.vals[string(k)] = string(v)
		}
	}
	return a
}

func indexName(s string) int {
	for i := 0; i+5 <= len(s); i++ {
		if s[i:i+5] == "name=" && (i == 0 || s[i-1] == ' ') {
			return i
		}
	}
	return -1
}

// Encode encodes data as a yEnc article body, for tests and fakes. part is
// from 1; begin is where data goes in a file of size total.
func Encode(name string, part, parts int, begin, total int64, data []byte) []byte {
	var b bytes.Buffer
	if parts > 1 {
		fmt.Fprintf(&b, "=ybegin part=%d total=%d line=128 size=%d name=%s\r\n", part, parts, total, name)
		fmt.Fprintf(&b, "=ypart begin=%d end=%d\r\n", begin+1, begin+int64(len(data)))
	} else {
		fmt.Fprintf(&b, "=ybegin line=128 size=%d name=%s\r\n", total, name)
	}
	col := 0
	for i, c := range data {
		e := c + 42
		esc := e == 0 || e == '\n' || e == '\r' || e == '=' ||
			((e == '\t' || e == ' ') && (col == 0 || i == len(data)-1)) || (e == '.' && col == 0)
		if esc {
			b.WriteByte('=')
			e += 64
			col++
		}
		b.WriteByte(e)
		col++
		if col >= 128 {
			b.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		b.WriteString("\r\n")
	}
	crc := crc32.ChecksumIEEE(data)
	if parts > 1 {
		fmt.Fprintf(&b, "=yend size=%d part=%d pcrc32=%08x\r\n", len(data), part, crc)
	} else {
		fmt.Fprintf(&b, "=yend size=%d crc32=%08x\r\n", len(data), crc)
	}
	return b.Bytes()
}
