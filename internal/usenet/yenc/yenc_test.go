package yenc

import (
	"bytes"
	"errors"
	"hash/crc32"
	"math/rand/v2"
	"testing"
)

func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 9, 127, 128, 129, 1000, 384000} {
		data := randomBytes(n, uint64(n))
		// Every byte that needs escaping, in every position of a word.
		data = append(data, 0xd6, 0xe0, 0xe3, 0x13, 0xdf, 0x04, 0xf6, 0xd6, 0xd6, 0xe0)
		p, err := Decode(Encode("a file.bin", 0, 1, 0, int64(len(data)), data))
		if err != nil {
			t.Fatalf("%d: %v", n, err)
		}
		if !bytes.Equal(p.Data, data) {
			t.Fatalf("%d bytes: decoded data differs", n)
		}
		if p.Name != "a file.bin" || p.Size != int64(len(data)) || p.Begin != 0 {
			t.Fatalf("header: %+v", p)
		}
	}
}

func TestMultipart(t *testing.T) {
	data := randomBytes(5000, 1)
	body := Encode("x.rar", 2, 3, 2000, 5000, data[2000:4000])
	p, err := Decode(append([]byte("junk before\r\n"), body...))
	if err != nil {
		t.Fatal(err)
	}
	if p.Number != 2 || p.Total != 3 || p.Begin != 2000 || p.End() != 4000 || p.Size != 5000 {
		t.Fatalf("got %+v", p)
	}
	if !bytes.Equal(p.Data, data[2000:4000]) || p.CRC != crc32.ChecksumIEEE(data[2000:4000]) {
		t.Fatal("data differs")
	}
}

func TestDamaged(t *testing.T) {
	data := randomBytes(3000, 2)
	body := Encode("x", 0, 1, 0, 3000, data)
	body[200] ^= 0x01
	if _, err := Decode(body); !errors.Is(err, ErrCRC) {
		t.Fatalf("got %v, want ErrCRC", err)
	}
	if _, err := Decode([]byte("hello\r\n")); !errors.Is(err, ErrNoYEnc) {
		t.Fatalf("got %v", err)
	}
}

func TestSub42(t *testing.T) {
	for i := range 256 {
		w := uint64(i) * ones
		if got, want := sub42(w), uint64(byte(i-42))*ones; got != want {
			t.Fatalf("%d: %x want %x", i, got, want)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	data := randomBytes(768000, 3)
	enc := Encode("x", 1, 2, 0, int64(len(data)), data)
	dst := make([]byte, len(enc))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		DecodeInto(dst, enc)
	}
}
