package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sendTimeout is how long one channel gets to take an alert.
const sendTimeout = 20 * time.Second

func (h Webhook) label() string {
	if h.Name != "" {
		return h.Name
	}
	switch h.Kind {
	case KindDiscord:
		return "Discord"
	case KindNtfy:
		return "ntfy"
	case KindHomeAssistant:
		return "Home Assistant"
	}
	return "Webhook"
}

// CheckWebhook says what's wrong with a webhook's settings, if anything.
func CheckWebhook(h Webhook) error {
	switch h.Kind {
	case KindDiscord, KindNtfy, KindHomeAssistant, KindJSON:
	default:
		return errors.New("unknown kind of webhook")
	}
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("enter the webhook's full address, starting with https:// or http://")
	}
	return nil
}

// CheckEmail says what's wrong with the email settings, if anything.
func CheckEmail(e Email) error {
	if !e.On {
		return nil
	}
	if e.Host == "" {
		return errors.New("enter the mail server")
	}
	if e.Port <= 0 || e.Port > 65535 {
		return errors.New("enter the mail server's port")
	}
	switch e.Security {
	case "starttls", "tls", "none":
	default:
		return errors.New("pick how to connect to the mail server")
	}
	if _, err := mail.ParseAddress(e.From); err != nil {
		return errors.New("enter the address alerts come from")
	}
	if _, err := mail.ParseAddressList(e.To); err != nil || strings.TrimSpace(e.To) == "" {
		return errors.New("enter the addresses alerts go to, separated by commas")
	}
	return nil
}

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: sendTimeout}
}

// dashboard is the address of the Health page, when the server knows its own.
func (m *Manager) dashboard() string {
	if m.URL == "" {
		return ""
	}
	return strings.TrimSuffix(m.URL, "/") + "/health"
}

func (m *Manager) serverName() string {
	if m.Server != "" {
		return m.Server
	}
	return "Box Office"
}

func (m *Manager) webhook(ctx context.Context, h Webhook, a Alert) error {
	if err := CheckWebhook(h); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	var body []byte
	header := http.Header{"Content-Type": {"application/json"}}
	switch h.Kind {
	case KindDiscord:
		body = m.discordBody(a)
	case KindNtfy:
		body = []byte(a.Message)
		header = http.Header{"Content-Type": {"text/plain; charset=utf-8"}}
		// Header values must be plain; ntfy reads RFC 2047 encoded ones.
		header.Set("Title", mime.QEncoding.Encode("utf-8", m.serverName()+": "+a.Title))
		header.Set("Priority", ntfyPriority(a))
		header.Set("Tags", ntfyTag(a))
		if d := m.dashboard(); d != "" {
			header.Set("Click", d)
		}
		if h.Token != "" {
			header.Set("Authorization", "Bearer "+h.Token)
		}
	default:
		body = m.jsonBody(a)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", "BoxOffice")
	resp, err := m.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the address is a secret; leave it out
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		s := strings.TrimSpace(string(msg))
		if s != "" && !strings.HasPrefix(s, "<") {
			return fmt.Errorf("answered %d: %s", resp.StatusCode, s)
		}
		return fmt.Errorf("answered %d", resp.StatusCode)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return nil
}

func ntfyPriority(a Alert) string {
	switch {
	case a.Resolved || a.Level == LevelInfo:
		return "default"
	case a.Level == LevelCritical:
		return "urgent"
	}
	return "high"
}

func ntfyTag(a Alert) string {
	switch {
	case a.Resolved:
		return "white_check_mark"
	case a.Level == LevelCritical:
		return "rotating_light"
	case a.Level == LevelWarning:
		return "warning"
	}
	return "information_source"
}

func (m *Manager) discordBody(a Alert) []byte {
	color := 0xC8352A // Box Office marquee red
	switch {
	case a.Resolved:
		color = 0x22C55E
	case a.Level == LevelCritical:
		color = 0xDC2626
	case a.Level == LevelWarning:
		color = 0xF59E0B
	}
	embed := map[string]any{
		"title":       a.Title,
		"description": a.Message,
		"color":       color,
		"timestamp":   time.UnixMilli(a.Time).UTC().Format(time.RFC3339),
		"footer":      map[string]string{"text": m.serverName()},
	}
	if d := m.dashboard(); d != "" {
		embed["url"] = d
	}
	b, _ := json.Marshal(map[string]any{"username": "Box Office", "embeds": []any{embed}, "allowed_mentions": map[string]any{"parse": []string{}}})
	return b
}

// jsonBody is what Home Assistant and other webhooks get.
func (m *Manager) jsonBody(a Alert) []byte {
	v := map[string]any{
		"server":   m.serverName(),
		"rule":     a.Rule,
		"key":      a.Key,
		"level":    a.Level,
		"title":    a.Title,
		"message":  a.Message,
		"resolved": a.Resolved,
		"time":     time.UnixMilli(a.Time).UTC().Format(time.RFC3339),
	}
	if d := m.dashboard(); d != "" {
		v["url"] = d
	}
	b, _ := json.Marshal(v)
	return b
}

func (m *Manager) email(ctx context.Context, e Email, a Alert) error {
	if err := CheckEmail(e); err != nil {
		return err
	}
	subject := "[" + m.serverName() + "] " + a.Title
	body := a.Message + "\n"
	if d := m.dashboard(); d != "" {
		body += "\nHealth: " + d + "\n"
	}
	body += "\n— " + m.serverName() + ", " + time.UnixMilli(a.Time).Format("2 Jan 2006 15:04 MST") + "\n" +
		"Change which alerts you get in Health → Alerts in Box Office.\n"
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if m.SendMail != nil {
		return m.SendMail(ctx, e, subject, body)
	}
	return sendMail(ctx, e, subject, body)
}

// sendMail sends a plain text email through e's server.
func sendMail(ctx context.Context, e Email, subject, body string) error {
	from, _ := mail.ParseAddress(e.From)
	to, _ := mail.ParseAddressList(e.To)
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	tlsConfig := &tls.Config{ServerName: e.Host}

	d := net.Dialer{}
	var conn net.Conn
	var err error
	if e.Security == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	if e.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server doesn't offer STARTTLS; pick another way to connect")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if e.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("the mail server doesn't take a username and password")
		}
		if err := c.Auth(smtp.PlainAuth("", e.Username, e.Password, e.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	for _, t := range to {
		if err := c.Rcpt(t.Address); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(message(from, to, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func message(from *mail.Address, to []*mail.Address, subject, body string) []byte {
	var b strings.Builder
	var addrs []string
	for _, t := range to {
		addrs = append(addrs, t.String())
	}
	id := make([]byte, 12)
	rand.Read(id)
	domain := "boxoffice.local"
	if _, host, ok := strings.Cut(from.Address, "@"); ok {
		domain = host
	}
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(addrs, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}
