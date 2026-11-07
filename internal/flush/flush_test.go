package flush

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("box office "), (Every*2)/11+7)
	w := &Writer{F: f}
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, data) {
		t.Fatal("file differs")
	}
}
