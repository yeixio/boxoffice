// Package push sends notifications to the browsers and phones that asked
// for them, through the push service of whoever makes the browser (Google,
// Apple, Mozilla, Microsoft), so they arrive while Box Office isn't open.
// Admins' devices hear about what they picked; a request-site member's
// hear about that member's own requests.
package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/yeixio/boxoffice/internal/store"
)

// settingKey holds this server's signing key. Browsers subscribe to it, so
// changing it would cut every device off.
const settingKey = "push_key"

// contact goes with every message, for the push service's people; Apple's
// refuses a message without one.
const contact = "https://github.com/yeixio/boxoffice"

// What an admin's device can hear about.
const (
	TopicReady    = "ready"    // a title arrived in the library
	TopicRequests = "requests" // someone asked for a title or reported a problem
	TopicProblems = "problems" // a download failed or was quarantined, or an alert
)

// Topics lists them, in the order they're shown.
var Topics = []string{TopicReady, TopicRequests, TopicProblems}

// The most devices kept: for all admins, and for one member.
const (
	maxAdminDevices  = 50
	maxMemberDevices = 10
)

// Message is what a device shows.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// URL is the page opened by tapping it.
	URL string `json:"url,omitempty"`
	// Tag makes a newer message about the same thing replace the older one.
	Tag string `json:"tag,omitempty"`
}

// Error is something to tell the person as it is.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func badf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// ErrGone is a device whose browser dropped the subscription. It's
// forgotten when that's found out.
var ErrGone = errors.New("that device no longer takes notifications from Box Office, so it was removed. Turn them on again on the device")

// Service keeps the devices and sends to them.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// HTTP is given by tests; otherwise a client that won't call addresses
	// inside the network is used.
	HTTP *http.Client
	// AnyAddress lets tests use a push service on this machine.
	AnyAddress bool

	mu     sync.Mutex
	client *http.Client
}

// key reads this server's signing key, making it the first time.
func (s *Service) key(ctx context.Context) (*ecdsa.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, err := s.Store.Setting(ctx, settingKey)
	if err != nil {
		return nil, err
	}
	if saved == "" {
		if saved, err = newKey(); err != nil {
			return nil, err
		}
		if err := s.Store.SetSetting(ctx, settingKey, saved); err != nil {
			return nil, err
		}
	}
	return parseKey(saved)
}

// PublicKey is the key a browser subscribes with.
func (s *Service) PublicKey(ctx context.Context) (string, error) {
	k, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	return publicKey(k)
}

// Save adds a device, or updates the one with the same endpoint: an
// admin's when d.MemberID is 0.
func (s *Service) Save(ctx context.Context, d store.PushDevice) (store.PushDevice, error) {
	d.Endpoint = strings.TrimSpace(d.Endpoint)
	u, err := url.Parse(d.Endpoint)
	if err != nil || u.Host == "" || len(d.Endpoint) > 2000 || (u.Scheme != "https" && !s.AnyAddress) {
		return d, badf("that isn't a push address a browser would give")
	}
	key, err := decode(d.P256dh)
	if err != nil || len(key) != 65 {
		return d, badf("the device's key isn't one a browser would give")
	}
	auth, err := decode(d.Auth)
	if err != nil || len(auth) != 16 {
		return d, badf("the device's key isn't one a browser would give")
	}
	// Sealing something shows the key is a real one.
	if _, err := encrypt([]byte("{}"), key, auth, nil, nil); err != nil {
		return d, badf("the device's key isn't one a browser would give")
	}
	d.Topics = slices.DeleteFunc(slices.Clone(Topics), func(t string) bool { return !slices.Contains(d.Topics, t) })
	if d.MemberID != 0 {
		d.Topics = nil
	}
	d.Name = strings.TrimSpace(d.Name)
	if utf8.RuneCountInString(d.Name) > 80 {
		d.Name = string([]rune(d.Name)[:80])
	}
	have, err := s.Store.PushDevices(ctx, d.MemberID)
	if err != nil {
		return d, err
	}
	limit := maxAdminDevices
	if d.MemberID != 0 {
		limit = maxMemberDevices
	}
	if len(have) >= limit && !slices.ContainsFunc(have, func(h store.PushDevice) bool { return h.Endpoint == d.Endpoint }) {
		return d, badf("that's %d devices already. Remove one that isn't used any more first", limit)
	}
	return s.Store.SavePushDevice(ctx, d)
}

// Send sends m to one device. A device that's gone is forgotten, and the
// error is ErrGone.
func (s *Service) Send(ctx context.Context, d store.PushDevice, m Message) error {
	key, err := decode(d.P256dh)
	if err != nil {
		return err
	}
	auth, err := decode(d.Auth)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(fit(m))
	if err != nil {
		return err
	}
	body, err := encrypt(payload, key, auth, nil, nil)
	if err != nil {
		return err
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil {
		return err
	}
	k, err := s.key(ctx)
	if err != nil {
		return err
	}
	who, err := authorization(k, u.Scheme+"://"+u.Host, contact, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", who)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	// A device that's off for more than a day doesn't need old news.
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "normal")
	res, err := s.http().Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach the push service at %s: %w", u.Host, err)
	}
	defer res.Body.Close()
	said, _ := io.ReadAll(io.LimitReader(res.Body, 300))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		if err := s.Store.DeletePushDevice(ctx, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return ErrGone
	}
	return fmt.Errorf("the push service at %s answered %d %s", u.Host, res.StatusCode, strings.TrimSpace(string(said)))
}

// fit shortens a message that wouldn't fit in one push.
func fit(m Message) Message {
	m.Title = short(m.Title, 200)
	m.Body = short(m.Body, 600)
	return m
}

func short(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Test sends a device a message that says so.
func (s *Service) Test(ctx context.Context, d store.PushDevice) error {
	return s.Send(ctx, d, Message{Title: "Box Office", Body: "Notifications are working on this device.", URL: "/", Tag: "test"})
}

// Notify passes a notification on: to the admins' devices that picked its
// kind, and, when a title arrived, to the members who asked for it.
func (s *Service) Notify(ctx context.Context, n store.Notification) {
	m := Message{Title: n.Title, Body: n.Body, URL: n.Page, Tag: n.Event}
	if n.Media != nil {
		m.Tag = fmt.Sprintf("%s:%s:%d", n.Event, n.Media.Kind, n.Media.TMDBID)
	}
	if topic := topic(n); topic != "" {
		devices, err := s.Store.PushDevices(ctx, 0)
		if err != nil {
			s.warn("couldn't read the devices to notify", err)
		}
		for _, d := range devices {
			if slices.Contains(d.Topics, topic) {
				s.send(ctx, d, m)
			}
		}
	}
	if n.Event != store.EventImported || n.Media == nil || n.Media.TMDBID == 0 {
		return
	}
	asked, err := s.Store.TitleRequests(ctx, n.Media.Kind, n.Media.TMDBID)
	if err != nil {
		s.warn("couldn't read who asked for a title", err)
		return
	}
	m.URL = MemberPage
	told := map[int64]bool{}
	for _, r := range asked {
		if r.Status != store.RequestApproved || told[r.MemberID] {
			continue
		}
		told[r.MemberID] = true
		s.Member(ctx, r.MemberID, m)
	}
}

// MemberPage is the request site's page of someone's own requests. The
// request site sends the address on to wherever it's being served.
const MemberPage = "/request/requests"

// Member sends m to all of one member's devices.
func (s *Service) Member(ctx context.Context, memberID int64, m Message) {
	if memberID == 0 {
		return
	}
	devices, err := s.Store.PushDevices(ctx, memberID)
	if err != nil {
		s.warn("couldn't read the devices to notify", err)
	}
	for _, d := range devices {
		s.send(ctx, d, m)
	}
}

func (s *Service) send(ctx context.Context, d store.PushDevice, m Message) {
	if err := s.Send(ctx, d, m); err != nil && !errors.Is(err, ErrGone) {
		s.warn("couldn't send a push notification", err)
	}
}

func (s *Service) warn(msg string, err error) {
	if s.Log != nil {
		s.Log.Warn(msg, "err", err)
	}
}

// topic is which of an admin's choices a notification comes under, or ""
// for one that isn't pushed: a download starting, an alert clearing up.
func topic(n store.Notification) string {
	switch {
	case n.Event == store.EventImported || n.Event == store.EventDownloaded:
		return TopicReady
	case n.Event == "request.created" || n.Event == "issue.created":
		return TopicRequests
	case n.Level == store.LevelWarning || n.Level == store.LevelCritical:
		return TopicProblems
	}
	return ""
}

// http is the client messages go out with.
func (s *Service) http() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		s.client = newClient(!s.AnyAddress)
	}
	return s.client
}

// newClient makes a client for addresses that came from a browser: it
// follows no redirects, and won't connect to this machine or the home
// network, which no push service is on. When Box Office reaches the
// internet through a proxy, the proxy is what it connects to, and is left
// to decide.
func newClient(guard bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	t := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 10, IdleConnTimeout: time.Minute}
	if u, _ := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: "fcm.googleapis.com"}}); u == nil && guard {
		dialer.Control = publicOnly
	}
	t.DialContext = dialer.DialContext
	return &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// publicOnly refuses to connect to an address that isn't out on the
// internet. It looks at the address being connected to, after the name was
// looked up, so a name that points inside doesn't get through.
func publicOnly(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		ip = nil // shared address space, as some home networks and VPNs use
	}
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("%s isn't a push service's address", host)
	}
	return nil
}
