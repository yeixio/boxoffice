// Package par2 checks downloaded files against their par2 files and
// repairs damaged or missing parts from the recovery blocks.
//
// A par2 set splits its files into equal slices (the last of each file
// padded with zeros) and stores an MD5 and CRC-32 of each, plus recovery
// slices: Reed-Solomon sums of every input slice over GF(2^16). Any k
// recovery slices rebuild any k lost input slices.
package par2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

var magic = []byte("PAR2\x00PKT")

var (
	typeMain     = pktType("PAR 2.0\x00Main\x00\x00\x00\x00")
	typeFileDesc = pktType("PAR 2.0\x00FileDesc")
	typeIFSC     = pktType("PAR 2.0\x00IFSC\x00\x00\x00\x00")
	typeRecovery = pktType("PAR 2.0\x00RecvSlic")
)

func pktType(s string) (t [16]byte) { copy(t[:], s); return }

// ID is a par2 file or set id.
type ID [16]byte

// File is one file the set protects.
type File struct {
	ID     ID
	Name   string // as the poster named it; a base name, never a path
	Size   int64
	MD5    [16]byte
	MD516k [16]byte // of the first 16 KiB
	slices []sliceCheck
	first  int // the index of its first slice among all the set's slices
}

// Slices is how many slices the file has.
func (f *File) Slices(size int64) int { return int((f.Size + size - 1) / size) }

type sliceCheck struct {
	md5 [16]byte
	crc uint32
}

type recovery struct {
	exponent int
	path     string
	offset   int64 // of the slice data
}

// Set is a par2 recovery set, read from its .par2 files.
type Set struct {
	SliceSize int64
	Files     []*File // in the set's order, by ID
	recovery  []recovery
	setID     ID
	// Progress, if set, hears how far Verify and Repair have got.
	Progress func(done, total int64)
}

func (s *Set) progress(done, total int64) {
	if s.Progress != nil {
		s.Progress(done, total)
	}
}

// countWriter tells of bytes hashed as they go.
type countWriter struct {
	n    *int64
	tell func()
}

func (c countWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	c.tell()
	return len(p), nil
}

// RecoveryBlocks is how many recovery slices the par2 files that were read
// hold.
func (s *Set) RecoveryBlocks() int { return len(s.recovery) }

// ErrNoSet means none of the files had a usable par2 set in them.
var ErrNoSet = errors.New("the par2 files have no usable recovery set")

// Load reads par2 files: an index file, volumes, or both. Damaged packets
// are skipped.
func Load(paths []string) (*Set, error) {
	type found struct {
		main  []byte
		descs map[ID]*File
		ifsc  map[ID][]sliceCheck
		rec   map[int]recovery
	}
	sets := map[ID]*found{}
	get := func(id ID) *found {
		f := sets[id]
		if f == nil {
			f = &found{descs: map[ID]*File{}, ifsc: map[ID][]sliceCheck{}, rec: map[int]recovery{}}
			sets[id] = f
		}
		return f
	}
	for _, p := range paths {
		err := scan(p, func(setID ID, typ [16]byte, body []byte, offset int64) {
			f := get(setID)
			switch typ {
			case typeMain:
				if f.main == nil {
					f.main = body
				}
			case typeFileDesc:
				if len(body) < 56 {
					return
				}
				d := &File{}
				copy(d.ID[:], body[0:16])
				copy(d.MD5[:], body[16:32])
				copy(d.MD516k[:], body[32:48])
				d.Size = int64(binary.LittleEndian.Uint64(body[48:56]))
				d.Name = cleanName(string(bytes.TrimRight(body[56:], "\x00")))
				f.descs[d.ID] = d
			case typeIFSC:
				if len(body) < 16 {
					return
				}
				var id ID
				copy(id[:], body[:16])
				var checks []sliceCheck
				for b := body[16:]; len(b) >= 20; b = b[20:] {
					var c sliceCheck
					copy(c.md5[:], b[:16])
					c.crc = binary.LittleEndian.Uint32(b[16:20])
					checks = append(checks, c)
				}
				f.ifsc[id] = checks
			case typeRecovery:
				e := int(binary.LittleEndian.Uint32(body[:4]))
				if _, ok := f.rec[e]; !ok {
					f.rec[e] = recovery{exponent: e, path: p, offset: offset + 4}
				}
			}
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	// The set with a main packet; the biggest if more than one.
	var best *found
	var bestID ID
	for id, f := range sets {
		if f.main != nil && (best == nil || len(f.descs) > len(best.descs)) {
			best, bestID = f, id
		}
	}
	if best == nil || len(best.main) < 12 {
		return nil, ErrNoSet
	}
	s := &Set{SliceSize: int64(binary.LittleEndian.Uint64(best.main[:8])), setID: bestID}
	n := int(binary.LittleEndian.Uint32(best.main[8:12]))
	if s.SliceSize <= 0 || s.SliceSize%4 != 0 || len(best.main) < 12+16*n {
		return nil, ErrNoSet
	}
	first := 0
	for i := range n {
		var id ID
		copy(id[:], best.main[12+16*i:])
		d := best.descs[id]
		if d == nil {
			return nil, fmt.Errorf("the par2 files don't describe every file they protect: %w", ErrNoSet)
		}
		d.slices = best.ifsc[id]
		d.first = first
		first += d.Slices(s.SliceSize)
		s.Files = append(s.Files, d)
	}
	for _, r := range best.rec {
		s.recovery = append(s.recovery, r)
	}
	slices.SortFunc(s.recovery, func(a, b recovery) int { return a.exponent - b.exponent })
	return s, nil
}

func cleanName(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = filepath.Base(filepath.Clean("/" + n))
	if n == "/" || n == "." {
		return ""
	}
	return n
}

// scan walks the packets in one file, skipping damage, and calls fn with
// each packet whose MD5 checks out. Recovery packets aren't read whole:
// fn gets only their first 4 bytes (the exponent), and the offset of the
// body; their MD5 is checked when they're used.
func scan(path string, fn func(set ID, typ [16]byte, body []byte, offset int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	hdr := make([]byte, 64)
	for off := int64(0); off+64 <= size; {
		if _, err := f.ReadAt(hdr, off); err != nil {
			return nil
		}
		length := int64(binary.LittleEndian.Uint64(hdr[8:16]))
		if !bytes.Equal(hdr[:8], magic) || length < 64 || length%4 != 0 || off+length > size {
			next, ok := seekMagic(f, off+4, size)
			if !ok {
				return nil
			}
			off = next
			continue
		}
		var set ID
		var typ [16]byte
		copy(set[:], hdr[32:48])
		copy(typ[:], hdr[48:64])
		if typ == typeRecovery {
			ex := make([]byte, 4)
			if _, err := f.ReadAt(ex, off+64); err == nil {
				fn(set, typ, ex, off+64)
			}
			off += length
			continue
		}
		if length > 64<<20 { // no other packet is anywhere near this big
			off += 4
			continue
		}
		body := make([]byte, length-64)
		if _, err := f.ReadAt(body, off+64); err != nil {
			return nil
		}
		h := md5.New()
		h.Write(hdr[32:])
		h.Write(body)
		if bytes.Equal(h.Sum(nil), hdr[16:32]) {
			fn(set, typ, body, off+64)
			off += length
		} else {
			off += 4
		}
	}
	return nil
}

func seekMagic(f *os.File, from, size int64) (int64, bool) {
	buf := make([]byte, 1<<20)
	for off := from &^ 3; off < size; off += int64(len(buf)) - 8 {
		n, _ := f.ReadAt(buf, off)
		if n < 8 {
			return 0, false
		}
		if i := bytes.Index(buf[:n], magic); i >= 0 {
			return off + int64(i), true
		}
	}
	return 0, false
}

// checkRecovery checks a recovery packet's MD5, reading it in pieces.
func (s *Set) checkRecovery(r recovery) bool {
	f, err := os.Open(r.path)
	if err != nil {
		return false
	}
	defer f.Close()
	start := r.offset - 4 - 64
	hdr := make([]byte, 64)
	if _, err := f.ReadAt(hdr, start); err != nil {
		return false
	}
	length := int64(binary.LittleEndian.Uint64(hdr[8:16]))
	if length-68 != s.SliceSize {
		return false
	}
	h := md5.New()
	h.Write(hdr[32:])
	if n, err := io.Copy(h, io.NewSectionReader(f, start+64, length-64)); err != nil || n != length-64 {
		return false
	}
	return bytes.Equal(h.Sum(nil), hdr[16:32])
}

// ---- matching names ----

// Match finds the files in dir the set protects, by name or, for files
// posted under other names (obfuscated), by the MD5 of their first 16 KiB,
// and renames those to the names the set gives. It returns the renames.
func (s *Set) Match(dir string, candidates []string) (map[string]string, error) {
	want := map[[16]byte]*File{}
	for _, f := range s.Files {
		if f.Name == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, f.Name)); err == nil {
			continue
		}
		want[f.MD516k] = f
	}
	renamed := map[string]string{}
	if len(want) == 0 {
		return renamed, nil
	}
	buf := make([]byte, 16<<10)
	for _, c := range candidates {
		c = filepath.Base(c)
		if slices.ContainsFunc(s.Files, func(f *File) bool { return f.Name == c }) {
			continue
		}
		p := filepath.Join(dir, c)
		fh, err := os.Open(p)
		if err != nil {
			continue
		}
		n, _ := io.ReadFull(fh, buf)
		fh.Close()
		f := want[md5.Sum(buf[:n])]
		if f == nil {
			continue
		}
		// A file shorter than 16 KiB hashes all of itself, so a match is
		// only right if the sizes agree too.
		if st, err := os.Stat(p); err != nil || (f.Size < 16<<10 && st.Size() != f.Size) {
			continue
		}
		if err := os.Rename(p, filepath.Join(dir, f.Name)); err != nil {
			return renamed, err
		}
		renamed[c] = f.Name
		delete(want, f.MD516k)
	}
	return renamed, nil
}

// ---- checking ----

// FileState is how one file stands.
type FileState struct {
	Name    string `json:"name"`
	Missing bool   `json:"missing,omitempty"`
	Damaged int    `json:"damaged,omitempty"` // slices that need repair
	OK      bool   `json:"ok"`
}

// Report is what Verify found.
type Report struct {
	Files      []FileState `json:"files"`
	NeedBlocks int         `json:"needBlocks"` // slices to rebuild
	HaveBlocks int         `json:"haveBlocks"` // recovery slices on hand
	bad        []int       // the bad slices, by global index
	trim       bool        // a file has extra bytes at its end
}

// OK says every file is whole.
func (r *Report) OK() bool { return r.NeedBlocks == 0 && !r.trim }

// Repairable says there are enough recovery slices to fix everything.
func (r *Report) Repairable() bool { return r.HaveBlocks >= r.NeedBlocks }

// Verify checks every file in dir against the set.
func (s *Set) Verify(ctx context.Context, dir string) (*Report, error) {
	rep := &Report{HaveBlocks: len(s.recovery)}
	buf := make([]byte, s.SliceSize)
	var total, done int64
	for _, f := range s.Files {
		total += f.Size
	}
	tell := func() { s.progress(min(done, total), total) }
	for _, f := range s.Files {
		start := done
		st := FileState{Name: f.Name}
		n := f.Slices(s.SliceSize)
		p := filepath.Join(dir, f.Name)
		fh, err := os.Open(p)
		if f.Name == "" || err != nil {
			st.Missing, st.Damaged = true, n
			for i := range n {
				rep.bad = append(rep.bad, f.first+i)
			}
		} else {
			info, _ := fh.Stat()
			whole := false
			if info != nil && info.Size() == f.Size {
				h := md5.New()
				if _, err := io.Copy(io.MultiWriter(h, countWriter{&done, tell}), io.NewSectionReader(fh, 0, f.Size)); err == nil && bytes.Equal(h.Sum(nil), f.MD5[:]) {
					whole = true
				}
			}
			if !whole {
				done = start
				for i := range n {
					if err := ctx.Err(); err != nil {
						fh.Close()
						return nil, err
					}
					clear(buf)
					want := int(min(s.SliceSize, f.Size-int64(i)*s.SliceSize))
					m, _ := fh.ReadAt(buf[:want], int64(i)*s.SliceSize)
					good := m >= want && i < len(f.slices) &&
						md5.Sum(buf) == f.slices[i].md5 && crc32.ChecksumIEEE(buf) == f.slices[i].crc
					if !good {
						st.Damaged++
						rep.bad = append(rep.bad, f.first+i)
					}
					done += int64(want)
					tell()
				}
				// IFSC was missing: the file's wrong but which slices is
				// unknown, so all of them are rebuilt.
				if st.Damaged == 0 && len(f.slices) < n {
					st.Damaged = n
					for i := range n {
						rep.bad = append(rep.bad, f.first+i)
					}
				}
				if st.Damaged == 0 && info != nil && info.Size() > f.Size {
					rep.trim = true // only extra bytes at the end
				}
			}
			fh.Close()
		}
		done = start + f.Size
		tell()
		st.OK = st.Damaged == 0 && !st.Missing
		rep.NeedBlocks += st.Damaged
		rep.Files = append(rep.Files, st)
	}
	return rep, nil
}

// ---- repairing ----

// ErrNotEnough means there aren't enough recovery blocks.
var ErrNotEnough = errors.New("there aren't enough recovery blocks to repair the download")

// Repair rebuilds the slices Verify found bad and writes them into place,
// then trims every file to its right size. memLimit caps the memory the
// rebuilt slices take at once (0: 512 MiB); a bigger job takes several
// passes over the files.
func (s *Set) Repair(ctx context.Context, dir string, rep *Report, memLimit int64) error {
	k := len(rep.bad)
	if k == 0 {
		return s.trim(dir)
	}
	if k > len(s.recovery) {
		return ErrNotEnough
	}
	if memLimit <= 0 {
		memLimit = 512 << 20
	}
	total := 0
	for _, f := range s.Files {
		total += f.Slices(s.SliceSize)
	}
	consts := inputConstants(total)
	if len(consts) < total {
		return errors.New("the par2 set has more slices than par2 allows")
	}
	bad := map[int]int{} // global slice -> row
	for i, b := range rep.bad {
		bad[b] = i
	}
	// Pick k recovery slices whose matrix inverts, skipping damaged ones.
	var used []recovery
	var matrix [][]uint16
	for _, r := range s.recovery {
		if len(used) == k {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.checkRecovery(r) {
			used = append(used, r)
		}
	}
	if len(used) < k {
		return ErrNotEnough
	}
	matrix = make([][]uint16, k)
	for row, r := range used {
		matrix[row] = make([]uint16, k)
		for col, b := range rep.bad {
			matrix[row][col] = gfPow(consts[b], r.exponent)
		}
	}
	if !invert(matrix) {
		return errors.New("the recovery blocks can't rebuild these slices")
	}

	// Work through the slices in column chunks that fit in memory.
	chunk := s.SliceSize
	if int64(k)*2*chunk > memLimit {
		chunk = max(memLimit/(2*int64(k)), 4096) &^ 3
	}
	recFiles := map[string]*os.File{}
	for _, r := range used {
		if recFiles[r.path] == nil {
			fh, err := os.Open(r.path)
			if err != nil {
				return err
			}
			defer fh.Close()
			recFiles[r.path] = fh
		}
	}
	files := make([]*os.File, len(s.Files))
	for i, f := range s.Files {
		if f.Name == "" {
			return errors.New("the par2 set has a file with no name")
		}
		fh, err := os.OpenFile(filepath.Join(dir, f.Name), os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		defer fh.Close()
		files[i] = fh
	}
	acc := make([][]byte, k)
	out := make([][]byte, k)
	for i := range acc {
		acc[i] = make([]byte, chunk)
		out[i] = make([]byte, chunk)
	}
	in := make([]byte, chunk)
	workers := max(runtime.GOMAXPROCS(0), 1)
	passes := (s.SliceSize + chunk - 1) / chunk
	steps, step := passes*int64(total), int64(0)
	for start := int64(0); start < s.SliceSize; start += chunk {
		width := min(chunk, s.SliceSize-start)
		for i, r := range used {
			if _, err := recFiles[r.path].ReadAt(acc[i][:width], r.offset+start); err != nil {
				return fmt.Errorf("read a recovery block: %w", err)
			}
		}
		// acc[row] = R_row - sum over good slices c_i^e_row * D_i
		for fi, f := range s.Files {
			for j := range f.Slices(s.SliceSize) {
				g := f.first + j
				step++
				if step%16 == 0 {
					s.progress(step, steps)
				}
				if _, isBad := bad[g]; isBad {
					continue
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				clear(in[:width])
				off := int64(j)*s.SliceSize + start
				if n := min(width, f.Size-off); n > 0 {
					files[fi].ReadAt(in[:n], off)
				}
				parallelRows(workers, k, func(row int) {
					newMulTable(gfPow(consts[g], used[row].exponent)).mulAdd(acc[row][:width], in[:width])
				})
			}
		}
		// The lost slices are the inverse matrix times acc.
		parallelRows(workers, k, func(row int) {
			clear(out[row][:width])
			for col := range k {
				if c := matrix[row][col]; c != 0 {
					newMulTable(c).mulAdd(out[row][:width], acc[col][:width])
				}
			}
		})
		for row, g := range rep.bad {
			fi, j := s.locate(g)
			f := s.Files[fi]
			off := int64(j)*s.SliceSize + start
			if off >= f.Size {
				continue
			}
			n := min(width, f.Size-off)
			if _, err := files[fi].WriteAt(out[row][:n], off); err != nil {
				return err
			}
		}
	}
	for _, fh := range files {
		if err := fh.Sync(); err != nil {
			return err
		}
	}
	return s.trim(dir)
}

// trim cuts each file to the size the set gives, dropping anything after.
func (s *Set) trim(dir string) error {
	for _, f := range s.Files {
		p := filepath.Join(dir, f.Name)
		if st, err := os.Stat(p); err == nil && st.Size() > f.Size {
			if err := os.Truncate(p, f.Size); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Set) locate(g int) (file, slice int) {
	for i, f := range s.Files {
		if g < f.first+f.Slices(s.SliceSize) {
			return i, g - f.first
		}
	}
	return -1, -1
}

func parallelRows(workers, rows int, fn func(int)) {
	if rows == 1 || workers == 1 {
		for r := range rows {
			fn(r)
		}
		return
	}
	var wg sync.WaitGroup
	next := make(chan int, rows)
	for r := range rows {
		next <- r
	}
	close(next)
	for range min(workers, rows) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range next {
				fn(r)
			}
		}()
	}
	wg.Wait()
}
