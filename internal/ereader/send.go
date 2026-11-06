package ereader

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/store"
)

// sendTimeout is how long one book gets to go out: attachments are big
// next to alerts.
const sendTimeout = 3 * time.Minute

// convertTimeout is how long Calibre gets to turn a book into an EPUB.
const convertTimeout = 5 * time.Minute

// Formats each kind of reader takes by email, as Amazon and PocketBook
// list them. Any other address gets the book as it is.
var accepts = map[string][]string{
	// Amazon stopped taking MOBI in 2022 and never took AZW3 or KFX by
	// email; EPUB is turned into a Kindle book on its side.
	KindKindle:     {".epub", ".pdf", ".txt", ".rtf", ".doc", ".docx", ".htm", ".html"},
	KindPocketBook: {".epub", ".pdf", ".fb2", ".mobi", ".azw3", ".azw", ".djvu", ".txt", ".rtf", ".doc", ".docx", ".html", ".chm"},
}

// maxSize is the biggest file each kind of reader takes by email.
var maxSize = map[string]int64{KindKindle: 50 << 20}

var contentTypes = map[string]string{
	".epub": "application/epub+zip",
	".pdf":  "application/pdf",
	".mobi": "application/x-mobipocket-ebook",
	".azw":  "application/vnd.amazon.ebook",
	".azw3": "application/vnd.amazon.ebook",
	".fb2":  "application/x-fictionbook+xml",
	".djvu": "image/vnd.djvu",
	".txt":  "text/plain; charset=utf-8",
	".rtf":  "application/rtf",
	".htm":  "text/html; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
}

// Takes says whether a kind of reader takes a format by email.
func Takes(kind, ext string) bool {
	list, ok := accepts[kind]
	return !ok || slices.Contains(list, strings.ToLower(ext))
}

// Service sends books to e-readers.
type Service struct {
	Store *store.Store
	// AlertMail reads the mail server set up under Health → Alerts.
	AlertMail func(ctx context.Context) (alerts.Email, error)
	// Deliver sends a message; nil sends it through the mail server.
	Deliver func(ctx context.Context, e alerts.Email, from string, to []string, msg []byte) error
	// Convert is Calibre's ebook-convert, to turn books a reader doesn't
	// take into EPUB; "" looks for it on the PATH.
	Convert string
	Log     *slog.Logger

	mu sync.Mutex // one book goes out at a time
}

// Config reads the settings, with defaults for what was never set. Until
// they're saved, the alerts' mail server is used if there is one.
func (s *Service) Config(ctx context.Context) (Config, error) {
	var c Config
	v, err := s.Store.Setting(ctx, settingConfig)
	if err != nil {
		return c, err
	}
	if v == "" {
		c.Mail.SameAsAlerts = true
	} else if err := json.Unmarshal([]byte(v), &c); err != nil {
		return c, err
	}
	c.Normalize()
	return c, nil
}

// SaveConfig stores the settings.
func (s *Service) SaveConfig(ctx context.Context, c Config) error {
	c.Normalize()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(ctx, settingConfig, string(b))
}

// AlertServer is the mail server set up for alerts, if any.
func (s *Service) AlertServer(ctx context.Context) alerts.Email {
	if s.AlertMail == nil {
		return alerts.Email{}
	}
	e, err := s.AlertMail(ctx)
	if err != nil {
		return alerts.Email{}
	}
	return e
}

// Converter is the path of Calibre's ebook-convert, or "" when Box
// Office can't find it.
func (s *Service) Converter() string {
	tool := s.Convert
	if tool == "" {
		tool = "ebook-convert"
	}
	p, err := exec.LookPath(tool)
	if err != nil {
		return ""
	}
	return p
}

// Imported sends a book that just went into the library to each reader
// that gets new ebooks, unless it went there already. It's for a
// download's Imported hook.
func (s *Service) Imported(ctx context.Context, d store.Download) {
	if d.Kind != store.Ebook || d.TitleID == 0 {
		return
	}
	c, err := s.Config(ctx)
	if err != nil {
		s.warn("read e-reader settings", err)
		return
	}
	for _, dev := range c.Devices {
		if !dev.Auto {
			continue
		}
		if sent, err := s.Store.SentToEreader(ctx, dev.ID, d.TitleID); err != nil || sent {
			continue
		}
		s.Send(ctx, dev.ID, d.TitleID, true)
	}
}

// Send emails a book's ebook to a reader, notes how it went, and says.
func (s *Service) Send(ctx context.Context, deviceID string, bookID int64, auto bool) (store.EreaderSend, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return store.EreaderSend{}, err
	}
	dev, ok := c.Device(deviceID)
	if !ok {
		return store.EreaderSend{}, errors.New("there's no such e-reader; add it in Settings")
	}
	b, err := s.Store.Book(ctx, bookID)
	if err != nil {
		return store.EreaderSend{}, err
	}
	author := ""
	if a, err := s.Store.Author(ctx, b.AuthorID); err == nil {
		author = a.Name
	}
	rec := store.EreaderSend{DeviceID: dev.ID, DeviceName: dev.Label(), BookID: b.ID, Title: b.Title, Auto: auto}
	err = s.sendBook(ctx, c, dev, b, author)
	return s.note(ctx, rec, err), err
}

// Test sends a small test book to a reader with the settings given, saved
// or not, so an admin knows the address and approved sender are right.
func (s *Service) Test(ctx context.Context, c Config, deviceID string) (store.EreaderSend, error) {
	dev, ok := c.Device(deviceID)
	if !ok {
		return store.EreaderSend{}, errors.New("there's no such e-reader")
	}
	rec := store.EreaderSend{DeviceID: dev.ID, DeviceName: dev.Label(), Title: TestTitle}
	err := s.deliver(ctx, c, dev, attachment{name: "Box Office test.epub", ext: ".epub", data: TestBook(time.Now())}, TestTitle, "")
	return s.note(ctx, rec, err), err
}

// Label is how a reader is named for people.
func (d Device) Label() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Address
}

func (s *Service) note(ctx context.Context, rec store.EreaderSend, err error) store.EreaderSend {
	if err != nil {
		rec.Error = sentence(err.Error())
		if s.Log != nil {
			s.Log.Warn("couldn't send a book to an e-reader", "book", rec.Title, "reader", rec.DeviceName, "err", err)
		}
	} else if s.Log != nil {
		s.Log.Info("sent a book to an e-reader", "book", rec.Title, "reader", rec.DeviceName, "auto", rec.Auto)
	}
	if saved, serr := s.Store.AddEreaderSend(context.WithoutCancel(ctx), rec); serr == nil {
		return saved
	}
	return rec
}

type attachment struct {
	name string // the file's name in the email
	ext  string
	data []byte
}

func (s *Service) sendBook(ctx context.Context, c Config, dev Device, b store.Book, author string) error {
	if b.EbookFile == nil {
		return errors.New("there's no ebook of it in the library yet")
	}
	path := b.EbookFile.Path
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".kepub" || strings.HasSuffix(strings.ToLower(path), ".kepub.epub") {
		// A Kobo EPUB is an EPUB, with marks only Kobo reads.
		ext = ".epub"
	}
	var data []byte
	var err error
	if Takes(dev.Kind, ext) {
		data, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("couldn't read %s: %w", path, err)
		}
	} else {
		conv := s.Converter()
		if conv == "" {
			return fmt.Errorf("%s doesn't take %s files by email, and Box Office can't turn it into an EPUB without Calibre's ebook-convert; get an EPUB copy of it instead", kindName(dev.Kind), strings.TrimPrefix(ext, "."))
		}
		data, err = convert(ctx, conv, path)
		if err != nil {
			return fmt.Errorf("couldn't turn the %s file into an EPUB: %w", strings.TrimPrefix(ext, "."), err)
		}
		ext = ".epub"
	}
	if max := maxSize[dev.Kind]; max > 0 && int64(len(data)) > max {
		return fmt.Errorf("it's %d MB, and %s takes files of %d MB at most", len(data)>>20, kindName(dev.Kind), max>>20)
	}
	name := b.Title
	if author != "" {
		name += " - " + author
	}
	if dev.Kind == KindKindle {
		// Amazon turns down some names with letters outside ASCII; the
		// book's own title comes from inside it anyway.
		name = asciiName(name)
	}
	return s.deliver(ctx, c, dev, attachment{name: name + ext, ext: ext, data: data}, b.Title, author)
}

func kindName(kind string) string {
	switch kind {
	case KindKindle:
		return "Kindle"
	case KindPocketBook:
		return "PocketBook"
	}
	return "This e-reader"
}

func (s *Service) deliver(ctx context.Context, c Config, dev Device, a attachment, title, author string) error {
	alertMail := s.AlertServer(ctx)
	if err := c.Check(alertMail); err != nil {
		return err
	}
	server := c.Server(alertMail)
	from, err := mail.ParseAddress(server.From)
	if err != nil {
		return errors.New("enter the address books come from")
	}
	to, err := mail.ParseAddress(dev.Address)
	if err != nil {
		return errors.New("the e-reader's address isn't right")
	}
	msg, err := message(from, to, title, author, a)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	send := s.Deliver
	if send == nil {
		send = alerts.Deliver
	}
	return send(ctx, server, from.Address, []string{to.Address}, msg)
}

// message is an email with the book attached.
func message(from, to *mail.Address, title, author string, a attachment) ([]byte, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	id := make([]byte, 12)
	rand.Read(id)
	domain := "boxoffice.local"
	if _, host, ok := strings.Cut(from.Address, "@"); ok {
		domain = host
	}
	h := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		// Amazon reads the subject "convert" as an order to turn a PDF into
		// a Kindle book, so a book's title is all it ever says.
		"Subject: " + mime.QEncoding.Encode("utf-8", title),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=" + w.Boundary(),
		"Auto-Submitted: auto-generated",
	}

	text := "Box Office sent you " + title
	if author != "" {
		text += " by " + author
	}
	text += ".\r\n"
	p, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"8bit"}})
	if err != nil {
		return nil, err
	}
	p.Write([]byte(text))

	ct := contentTypes[a.ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	p, err = w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(strings.SplitN(ct, ";", 2)[0], map[string]string{"name": a.name})},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": a.name})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(a.data)
	for len(enc) > 76 {
		p.Write([]byte(enc[:76] + "\r\n"))
		enc = enc[76:]
	}
	p.Write([]byte(enc + "\r\n"))
	if err := w.Close(); err != nil {
		return nil, err
	}
	return append([]byte(strings.Join(h, "\r\n")+"\r\n\r\n"), body.Bytes()...), nil
}

// asciiName keeps a file name's plain letters, digits and punctuation.
func asciiName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" -_.,()'&", r)):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.Trim(out, " -.,")
	if out == "" {
		return "book"
	}
	return out
}

// convert turns a book into an EPUB with Calibre's ebook-convert.
func convert(ctx context.Context, tool, path string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "boxoffice-convert-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "book.epub")
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, path, out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return os.ReadFile(out)
}

func (s *Service) warn(what string, err error) {
	if s.Log != nil {
		s.Log.Warn("couldn't "+what, "err", err)
	}
}

// sentence makes an error read as a sentence.
func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
