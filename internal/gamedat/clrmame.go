package gamedat

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Entry is one game in a ClrMamePro DAT, the text format No-Intro and
// libretro's database use.
type Entry struct {
	// Fields are the game's own words: name, description, year, genre,
	// developer, publisher, users and so on, the first of each.
	Fields map[string]string
	// Comments are its comment lines; libretro's metadata DATs name the
	// game in one.
	Comments []string
	Roms     []Rom
	// SHA1s are the files' SHA-1s, by file name, where the DAT has them.
	SHA1s map[string]string
}

// Name is the game's name.
func (e Entry) Name() string { return e.Fields["name"] }

// DAT is a parsed ClrMamePro DAT.
type DAT struct {
	Header map[string]string
	Games  []Entry
}

// ParseClrMame reads a ClrMamePro DAT.
func ParseClrMame(r io.Reader) (*DAT, error) {
	lx := &lexer{r: bufio.NewReaderSize(r, 64<<10)}
	dat := &DAT{Header: map[string]string{}}
	for {
		key, err := lx.next()
		if err == io.EOF {
			return dat, nil
		}
		if err != nil {
			return nil, err
		}
		if key.paren != 0 {
			return nil, errors.New("not a ClrMamePro DAT: a block opens without a name")
		}
		block, err := lx.block()
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(key.text) {
		case "clrmamepro":
			for _, kv := range block.pairs {
				dat.Header[kv.key] = kv.value
			}
		case "game", "machine", "resource":
			e := Entry{Fields: map[string]string{}}
			for _, kv := range block.pairs {
				if kv.key == "comment" {
					e.Comments = append(e.Comments, kv.value)
				} else if _, ok := e.Fields[kv.key]; !ok {
					e.Fields[kv.key] = kv.value
				}
			}
			for _, sub := range block.subs {
				if sub.name != "rom" {
					continue
				}
				var rom Rom
				var sha1 string
				for _, kv := range sub.pairs {
					switch kv.key {
					case "name":
						rom.Name = kv.value
					case "size":
						rom.Size, _ = strconv.ParseInt(kv.value, 10, 64)
					case "crc":
						if c, err := strconv.ParseUint(kv.value, 16, 32); err == nil {
							rom.CRC, rom.HasCRC = uint32(c), true
						}
					case "merge":
						rom.Merge = kv.value
					case "sha1":
						sha1 = strings.ToLower(kv.value)
					case "flags", "status":
						rom.NoDump = rom.NoDump || kv.value == "nodump"
					}
				}
				e.Roms = append(e.Roms, rom)
				if sha1 != "" {
					if e.SHA1s == nil {
						e.SHA1s = map[string]string{}
					}
					e.SHA1s[rom.Name] = sha1
				}
			}
			dat.Games = append(dat.Games, e)
		}
	}
}

type token struct {
	text  string
	paren byte // '(' or ')' for a bracket, 0 for a word
}

type pair struct{ key, value string }

type sub struct {
	name  string
	pairs []pair
}

type block struct {
	pairs []pair
	subs  []sub
}

type lexer struct {
	r *bufio.Reader
}

func (lx *lexer) next() (token, error) {
	for {
		c, err := lx.r.ReadByte()
		if err != nil {
			return token{}, err
		}
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			continue
		case c == '(' || c == ')':
			return token{paren: c}, nil
		case c == '"':
			var b strings.Builder
			for {
				c, err := lx.r.ReadByte()
				if err != nil {
					return token{}, err
				}
				if c == '"' {
					return token{text: b.String()}, nil
				}
				if c == '\\' {
					// libretro escapes quotes inside names.
					n, err := lx.r.ReadByte()
					if err != nil {
						return token{}, err
					}
					if n != '"' && n != '\\' {
						b.WriteByte(c)
					}
					c = n
				}
				b.WriteByte(c)
			}
		default:
			var b strings.Builder
			b.WriteByte(c)
			for {
				c, err := lx.r.ReadByte()
				if err == io.EOF {
					return token{text: b.String()}, nil
				}
				if err != nil {
					return token{}, err
				}
				if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == ')' {
					lx.r.UnreadByte()
					return token{text: b.String()}, nil
				}
				b.WriteByte(c)
			}
		}
	}
}

// block reads "( key value key ( ... ) ... )" after its name.
func (lx *lexer) block() (block, error) {
	var b block
	open, err := lx.next()
	if err != nil {
		return b, err
	}
	if open.paren != '(' {
		return b, errors.New("not a ClrMamePro DAT: expected (")
	}
	for {
		k, err := lx.next()
		if err != nil {
			return b, err
		}
		if k.paren == ')' {
			return b, nil
		}
		if k.paren == '(' {
			return b, errors.New("not a ClrMamePro DAT: a block opens without a name")
		}
		v, err := lx.next()
		if err != nil {
			return b, err
		}
		key := strings.ToLower(k.text)
		if v.paren == '(' {
			s := sub{name: key}
			for {
				sk, err := lx.next()
				if err != nil {
					return b, err
				}
				if sk.paren == ')' {
					break
				}
				sv, err := lx.next()
				if err != nil {
					return b, err
				}
				if sk.paren != 0 || sv.paren != 0 {
					return b, errors.New("not a ClrMamePro DAT: a block inside a block inside a block")
				}
				s.pairs = append(s.pairs, pair{strings.ToLower(sk.text), sv.text})
			}
			b.subs = append(b.subs, s)
			continue
		}
		if v.paren == ')' {
			// A key with no value closes the block.
			return b, nil
		}
		b.pairs = append(b.pairs, pair{key, v.text})
	}
}
