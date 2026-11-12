package gamedat

import (
	"archive/zip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"strings"

	"github.com/bodgit/sevenzip"
)

// File is one file in a game's archive, or a game file on its own.
type File struct {
	// Name is its name inside the archive, with any folder.
	Name string
	Size int64
	CRC  uint32
}

// Base is the file's name without its folder inside the archive.
func (f File) Base() string { return path.Base(strings.ReplaceAll(f.Name, `\`, "/")) }

// IsArchive says whether a file name is a zip or 7z, which hold a game's
// files.
func IsArchive(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".zip" || ext == ".7z"
}

// ListArchive lists the files in a zip or 7z with the CRC each one has
// written down for it. Nothing is unpacked: both keep their files' CRCs in
// their directory, so this reads only a little of a big archive.
func ListArchive(p string) ([]File, error) {
	switch strings.ToLower(path.Ext(p)) {
	case ".zip":
		z, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		out := make([]File, 0, len(z.File))
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			out = append(out, File{Name: f.Name, Size: int64(f.UncompressedSize64), CRC: f.CRC32})
		}
		return out, nil
	case ".7z":
		z, err := sevenzip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		out := make([]File, 0, len(z.File))
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			out = append(out, File{Name: f.Name, Size: int64(f.UncompressedSize), CRC: f.CRC32})
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s isn't a zip or 7z", path.Base(p))
}

// OpenInArchive opens one file inside a zip or 7z, for hashing.
func OpenInArchive(p, name string) (io.ReadCloser, error) {
	switch strings.ToLower(path.Ext(p)) {
	case ".zip":
		z, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					z.Close()
					return nil, err
				}
				return closeBoth{rc, z}, nil
			}
		}
		z.Close()
	case ".7z":
		z, err := sevenzip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					z.Close()
					return nil, err
				}
				return closeBoth{rc, z}, nil
			}
		}
		z.Close()
	}
	return nil, os.ErrNotExist
}

type closeBoth struct {
	io.ReadCloser
	outer io.Closer
}

func (c closeBoth) Close() error { return errors.Join(c.ReadCloser.Close(), c.outer.Close()) }

// MaxHashSize is the biggest game file hashed: a Nintendo DS game can be
// 512 MB. Bigger ones are known by what's written in them, or their names.
const MaxHashSize = 1 << 30

// maxHeaderSize is the biggest file read whole to hash without its header.
const maxHeaderSize = 256 << 20

// Header says how a system's files may start with a copier header that the
// lists leave out, or come in another byte order than theirs.
type Header int

const (
	NoHeader Header = iota
	// INES is the 16-byte header NES files start with.
	INES
	// Copier is the 512-byte header old SNES copiers added.
	Copier
	// N64Order is a Nintendo 64 file in either of the swapped byte
	// orders; the lists use big-endian (.z64).
	N64Order
	// Lynx is the 64-byte header Atari Lynx files start with.
	Lynx
	// A7800 is the 128-byte header Atari 7800 files start with.
	A7800
	// FDS is the 16-byte header Famicom Disk System files start with.
	FDS
)

// CRCs hashes a game file the ways its system's list might know it: as
// it is, and without its header or with its bytes put in order. The first
// is the file as it is.
func CRCs(r io.Reader, size int64, h Header) ([]uint32, error) {
	if h == NoHeader {
		c := crc32.NewIEEE()
		if _, err := io.Copy(c, r); err != nil {
			return nil, err
		}
		return []uint32{c.Sum32()}, nil
	}
	if size > maxHeaderSize {
		return nil, errors.New("too big to check")
	}
	b, err := io.ReadAll(io.LimitReader(r, maxHeaderSize+1))
	if err != nil {
		return nil, err
	}
	out := []uint32{crc32.ChecksumIEEE(b)}
	skip := 0
	switch h {
	case INES:
		if len(b) > 16 && string(b[:4]) == "NES\x1a" {
			skip = 16
		}
	case Copier:
		if len(b)%1024 == 512 {
			skip = 512
		}
	case Lynx:
		if len(b) > 64 && string(b[:4]) == "LYNX" {
			skip = 64
		}
	case A7800:
		if len(b) > 128 && string(b[1:10]) == "ATARI7800" {
			skip = 128
		}
	case FDS:
		if len(b) > 16 && string(b[:4]) == "FDS\x1a" {
			skip = 16
		}
	case N64Order:
		if len(b) >= 4 && len(b)%4 == 0 {
			switch {
			case b[0] == 0x37 && b[1] == 0x80: // .v64: every two bytes swapped
				z := make([]byte, len(b))
				for i := 0; i+1 < len(b); i += 2 {
					z[i], z[i+1] = b[i+1], b[i]
				}
				out = append(out, crc32.ChecksumIEEE(z))
			case b[0] == 0x40 && b[1] == 0x12: // .n64: every four bytes reversed
				z := make([]byte, len(b))
				for i := 0; i+3 < len(b); i += 4 {
					z[i], z[i+1], z[i+2], z[i+3] = b[i+3], b[i+2], b[i+1], b[i]
				}
				out = append(out, crc32.ChecksumIEEE(z))
			}
		}
	}
	if skip > 0 {
		out = append(out, crc32.ChecksumIEEE(b[skip:]))
	}
	return out, nil
}
