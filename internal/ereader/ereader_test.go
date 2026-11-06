package ereader

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestKindOf(t *testing.T) {
	for addr, want := range map[string]string{
		"mike_42@kindle.com":     KindKindle,
		" Mike@Free.Kindle.com ": KindKindle,
		"mike@pbsync.com":        KindPocketBook,
		"me@example.com":         KindOther,
		"not an address":         KindOther,
	} {
		if got := KindOf(addr); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	alertMail := alerts.Email{Host: "smtp.example.com", Port: 587, Security: "starttls", From: "boxoffice@example.com"}
	ok := Config{Mail: Mail{SameAsAlerts: true}, Devices: []Device{{ID: "a", Kind: KindKindle, Address: "me@kindle.com"}}}
	if err := ok.Check(alertMail); err != nil {
		t.Fatalf("good settings: %v", err)
	}
	if got := ok.Server(alertMail).From; got != "boxoffice@example.com" {
		t.Errorf("from = %q, want the alerts' address", got)
	}
	for name, c := range map[string]Config{
		"no alert mail":  ok,
		"bad address":    {Mail: Mail{SameAsAlerts: true}, Devices: []Device{{Kind: KindOther, Address: "kindle"}}},
		"twice":          {Mail: Mail{SameAsAlerts: true}, Devices: []Device{{Kind: KindKindle, Address: "me@kindle.com"}, {Kind: KindKindle, Address: "ME@kindle.com"}}},
		"own, no server": {Mail: Mail{From: "a@example.com", Port: 587, Security: "starttls"}, Devices: ok.Devices},
		"own, no from":   {Mail: Mail{Host: "smtp.example.com", Port: 587, Security: "starttls"}, Devices: ok.Devices},
		"unknown kind":   {Mail: Mail{SameAsAlerts: true}, Devices: []Device{{Kind: "kobo", Address: "me@example.com"}}},
	} {
		am := alertMail
		if name == "no alert mail" {
			am = alerts.Email{}
		}
		if err := c.Check(am); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// No readers needs no mail server.
	if err := (Config{}).Check(alerts.Email{}); err != nil {
		t.Errorf("empty: %v", err)
	}
}

type sentMail struct {
	server alerts.Email
	from   string
	to     []string
	msg    []byte
}

func fixture(t *testing.T, file string) (*Service, *[]sentMail, store.Book) {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	a, err := st.AddAuthor(ctx, store.Author{OLKey: "OL1A", Name: "Ursula K. Le Guin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertBook(ctx, store.Book{AuthorID: a.ID, OLKey: "OL1W", Title: "A Wizard of Earthsea", Year: 1968}); err != nil {
		t.Fatal(err)
	}
	list, err := st.Books(ctx, a.ID)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	b := list[0]
	if file != "" {
		path := filepath.Join(t.TempDir(), file)
		os.WriteFile(path, []byte("the book"), 0o644)
		if _, err := st.AddFile(ctx, store.File{Kind: store.Ebook, OwnerID: b.ID, Path: path, Size: 8}); err != nil {
			t.Fatal(err)
		}
	}
	var sent []sentMail
	s := &Service{Store: st, Convert: "/nonexistent/ebook-convert",
		AlertMail: func(context.Context) (alerts.Email, error) {
			return alerts.Email{On: true, Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "u", Password: "p", From: "Box Office <boxoffice@example.com>", To: "admin@example.com"}, nil
		},
		Deliver: func(_ context.Context, e alerts.Email, from string, to []string, msg []byte) error {
			sent = append(sent, sentMail{e, from, to, msg})
			return nil
		}}
	c := Config{Mail: Mail{SameAsAlerts: true}, Devices: []Device{
		{ID: "k", Name: "Mike's Kindle", Address: "mike@kindle.com", Auto: true},
		{ID: "p", Name: "PocketBook", Address: "mike@pbsync.com"},
	}}
	if err := s.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	return s, &sent, b
}

// attached reads the attachment out of a sent email.
func attached(t *testing.T, msg []byte) (name, ctype string, data []byte) {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(m.Body, params["boundary"])
	for {
		p, err := r.NextPart()
		if err != nil {
			t.Fatal("no attachment")
		}
		if p.FileName() == "" {
			continue
		}
		raw, _ := io.ReadAll(p)
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		return p.FileName(), p.Header.Get("Content-Type"), data
	}
}

// A download that goes into the library goes to each reader set to get
// new ebooks, once.
func TestImportedSendsOnce(t *testing.T) {
	ctx := context.Background()
	s, sent, b := fixture(t, "A Wizard of Earthsea (1968).epub")
	s.Imported(ctx, store.Download{Kind: store.Ebook, TitleID: b.ID})
	if len(*sent) != 1 {
		t.Fatalf("sent %d emails, want 1 (to the Kindle only)", len(*sent))
	}
	m := (*sent)[0]
	if m.from != "boxoffice@example.com" || len(m.to) != 1 || m.to[0] != "mike@kindle.com" || m.server.Host != "smtp.example.com" || m.server.Password != "p" {
		t.Errorf("sent %+v", m)
	}
	name, ctype, data := attached(t, m.msg)
	if name != "A Wizard of Earthsea - Ursula K. Le Guin.epub" || !strings.HasPrefix(ctype, "application/epub+zip") || string(data) != "the book" {
		t.Errorf("attachment %q %q %q", name, ctype, data)
	}
	if !strings.Contains(string(m.msg), "Subject: A Wizard of Earthsea\r\n") {
		t.Errorf("subject isn't the title:\n%s", m.msg[:300])
	}
	// Again (an upgrade, say): it's there already.
	s.Imported(ctx, store.Download{Kind: store.Ebook, TitleID: b.ID})
	// An audiobook isn't for e-readers.
	s.Imported(ctx, store.Download{Kind: store.Audiobook, TitleID: b.ID})
	if len(*sent) != 1 {
		t.Fatalf("sent %d emails, want still 1", len(*sent))
	}
	hist, _ := s.Store.EreaderSends(ctx, 10)
	if len(hist) != 1 || !hist[0].Auto || hist[0].Error != "" || hist[0].DeviceName != "Mike's Kindle" {
		t.Errorf("history %+v", hist)
	}
}

// A Kindle doesn't take AZW3 by email; without Calibre it's not sent,
// and the history says why. A PocketBook takes it as it is.
func TestFormats(t *testing.T) {
	ctx := context.Background()
	s, sent, b := fixture(t, "book.azw3")
	rec, err := s.Send(ctx, "k", b.ID, false)
	if err == nil || !strings.Contains(rec.Error, "Kindle doesn't take azw3 files") || len(*sent) != 0 {
		t.Fatalf("to Kindle: %v %+v %d", err, rec, len(*sent))
	}
	if _, err := s.Send(ctx, "p", b.ID, false); err != nil || len(*sent) != 1 {
		t.Fatalf("to PocketBook: %v", err)
	}
	if name, _, _ := attached(t, (*sent)[0].msg); name != "A Wizard of Earthsea - Ursula K. Le Guin.azw3" {
		t.Errorf("name %q", name)
	}
	// A failed send doesn't count as sent.
	if ok, _ := s.Store.SentToEreader(ctx, "k", b.ID); ok {
		t.Error("the failed send counts")
	}
}

func TestKepubGoesAsEpub(t *testing.T) {
	s, sent, b := fixture(t, "book.kepub")
	if _, err := s.Send(context.Background(), "k", b.ID, false); err != nil {
		t.Fatal(err)
	}
	if name, _, _ := attached(t, (*sent)[0].msg); !strings.HasSuffix(name, ".epub") {
		t.Errorf("name %q", name)
	}
}

func TestNoFile(t *testing.T) {
	s, sent, b := fixture(t, "")
	rec, err := s.Send(context.Background(), "k", b.ID, false)
	if err == nil || rec.Error == "" || len(*sent) != 0 {
		t.Fatalf("%v %+v", err, rec)
	}
}

func TestAsciiName(t *testing.T) {
	for in, want := range map[string]string{
		"Cien años de soledad - Gabriel García Márquez": "Cien aos de soledad - Gabriel Garca Mrquez",
		"ノルウェイの森":                                       "book",
		"Dune: Part 1 - Frank Herbert":                  "Dune Part 1 - Frank Herbert",
	} {
		if got := asciiName(in); got != want {
			t.Errorf("asciiName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The test book is an EPUB readers open: mimetype first and stored.
func TestTestBook(t *testing.T) {
	data := TestBook(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC))
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if z.File[0].Name != "mimetype" || z.File[0].Method != zip.Store {
		t.Fatalf("first file %s, method %d", z.File[0].Name, z.File[0].Method)
	}
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
	}
	for _, n := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/page.xhtml"} {
		if !names[n] {
			t.Errorf("no %s", n)
		}
	}
	s, sent, _ := fixture(t, "")
	c, _ := s.Config(context.Background())
	if _, err := s.Test(context.Background(), c, "p"); err != nil || len(*sent) != 1 {
		t.Fatal(err)
	}
	if name, _, _ := attached(t, (*sent)[0].msg); name != "Box Office test.epub" {
		t.Errorf("name %q", name)
	}
}

// With Calibre there, a book the Kindle won't take goes as an EPUB.
func TestConverts(t *testing.T) {
	s, sent, b := fixture(t, "book.mobi")
	tool := filepath.Join(t.TempDir(), "ebook-convert")
	os.WriteFile(tool, []byte("#!/bin/sh\nprintf converted > \"$2\"\n"), 0o755)
	s.Convert = tool
	if _, err := s.Send(context.Background(), "k", b.ID, false); err != nil {
		t.Fatal(err)
	}
	name, _, data := attached(t, (*sent)[0].msg)
	if !strings.HasSuffix(name, ".epub") || string(data) != "converted" {
		t.Errorf("%q %q", name, data)
	}
}
