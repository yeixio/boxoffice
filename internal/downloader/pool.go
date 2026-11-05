package downloader

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp"
)

// pool keeps a news server's connections, up to the number the account
// allows, and reuses them.
type pool struct {
	srv    store.UsenetServer
	cfg    nntp.Server
	slots  chan struct{}
	mu     sync.Mutex
	idle   []*nntp.Conn
	active atomic.Int32
	// Once a server turns down the sign-in, or can't be reached, it rests
	// a while instead of being asked again for every article.
	downUntil atomic.Int64
	lastErr   atomic.Value // string
	log       *slog.Logger
	// used counts the bytes downloaded from the server; left is what's
	// left of a block account, -1 for none.
	used *atomic.Int64
	left atomic.Int64
	// closed: connections handed back are closed, not kept.
	closed atomic.Bool
}

func newPool(s store.UsenetServer) *pool {
	n := max(s.Connections, 1)
	return &pool{srv: s, slots: make(chan struct{}, n), cfg: nntp.Server{Host: s.Host, Port: s.Port, TLS: s.UseTLS, Username: s.Username, Password: s.Password}}
}

func (p *pool) down() bool {
	return time.Now().Unix() < p.downUntil.Load() || p.left.Load() == 0
}

// took counts n bytes downloaded from the server.
func (p *pool) took(n int64) {
	if p.used != nil {
		p.used.Add(n)
	}
	for {
		l := p.left.Load()
		if l < 0 {
			return
		}
		if p.left.CompareAndSwap(l, max(l-n, 0)) {
			if l-n <= 0 {
				p.lastErr.Store("its block is used up")
				if p.log != nil {
					p.log.Warn("news server's block account is used up", "server", p.srv.Host)
				}
			}
			return
		}
	}
}

func (p *pool) rest(err error, d time.Duration) {
	wasDown := p.down()
	p.downUntil.Store(time.Now().Add(d).Unix())
	p.lastErr.Store(err.Error())
	if p.log != nil && !wasDown {
		p.log.Warn("news server isn't working; resting it", "server", p.srv.Host, "port", p.srv.Port, "for", d.String(), "err", err)
	}
}

func (p *pool) get(ctx context.Context) (*nntp.Conn, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		c := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		p.active.Add(1)
		return c, nil
	}
	p.mu.Unlock()
	start := time.Now()
	c, err := nntp.Dial(ctx, p.cfg)
	if err != nil {
		<-p.slots
		if p.log != nil && ctx.Err() == nil {
			p.log.Debug("news server connection failed", "server", p.srv.Host, "port", p.srv.Port, "tls", p.srv.UseTLS, "err", err)
		}
		return nil, err
	}
	if p.log != nil {
		p.log.Debug("news server connected", "server", p.srv.Host, "port", p.srv.Port, "tls", p.srv.UseTLS,
			"took", time.Since(start).Round(time.Millisecond).String(), "open", p.active.Load()+1)
	}
	p.active.Add(1)
	return c, nil
}

// put returns a connection; a broken one (err set) is closed instead.
func (p *pool) put(c *nntp.Conn, broken bool) {
	p.active.Add(-1)
	if broken || p.closed.Load() {
		c.Close()
	} else {
		p.mu.Lock()
		p.idle = append(p.idle, c)
		p.mu.Unlock()
	}
	<-p.slots
}

// closeIdle closes the idle connections, and those still in use as
// they're handed back, so none are left open once the next download has
// its own. Anyone still fetching through it can, but nothing is kept.
func (p *pool) closeIdle() {
	p.closed.Store(true)
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, c := range idle {
		c.Close()
	}
}

// servers are the pools, first-choice servers first.
type servers struct {
	pools []*pool
	// skipped are servers left out because their block account is used up
	// or expired, and why.
	skipped []string
}

// newServers makes pools for the servers that are on and have data left.
// qs and data may be nil.
func newServers(list []store.UsenetServer, log *slog.Logger, qs map[int64]QuotaState, data *dataCounter) *servers {
	s := &servers{}
	for _, u := range list {
		if u.Enabled && u.Host != "" {
			q, ok := qs[u.ID]
			if !ok {
				q.Left = -1
			}
			if q.Problem != "" {
				s.skipped = append(s.skipped, u.Name+": "+q.Problem)
				continue
			}
			p := newPool(u)
			p.log = log
			p.left.Store(q.Left)
			if data != nil {
				p.used = data.of(u.ID)
			}
			s.pools = append(s.pools, p)
		}
	}
	slices.SortStableFunc(s.pools, func(a, b *pool) int { return a.srv.Level - b.srv.Level })
	return s
}

// unusable says why there are no servers to use, when it's their block
// accounts.
func (s *servers) unusable() string {
	if len(s.pools) > 0 || len(s.skipped) == 0 {
		return ""
	}
	return "no news server has data left: " + strings.Join(s.skipped, "; ")
}

// connections is how many articles can be fetched at once: the first-
// choice servers' connections (backups only get asked for what those lack).
func (s *servers) connections() int {
	n := 0
	for _, p := range s.pools {
		if p.srv.Level == s.pools[0].srv.Level {
			n += max(p.srv.Connections, 1)
		}
	}
	return max(n, 1)
}

var errAllDown = errors.New("none of the news servers can be reached")

// fetch gets an article body from the first server that has it.
func (s *servers) fetch(ctx context.Context, id string, buf []byte) ([]byte, error) {
	var missing bool
	var lastErr error
	for _, p := range s.pools {
		if p.down() {
			continue
		}
		for attempt := 0; attempt < 3; attempt++ {
			c, err := p.get(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return buf, ctx.Err()
				}
				lastErr = err
				if errors.Is(err, nntp.ErrAuth) {
					p.rest(err, 10*time.Minute)
					break
				}
				var ne *nntp.Error
				if errors.As(err, &ne) && ne.Temporary() {
					time.Sleep(time.Second)
					continue
				}
				p.rest(err, time.Minute)
				break
			}
			body, err := c.Body(id, buf[:0])
			if err == nil {
				p.put(c, false)
				p.took(int64(len(body)))
				return body, nil
			}
			if errors.Is(err, nntp.ErrNoArticle) {
				p.put(c, false)
				missing = true
				break
			}
			p.put(c, true)
			lastErr = err
			if ctx.Err() != nil {
				return buf, ctx.Err()
			}
		}
	}
	if missing {
		return buf, nntp.ErrNoArticle
	}
	if lastErr == nil {
		lastErr = errAllDown
	}
	return buf, lastErr
}

func (s *servers) close() {
	for _, p := range s.pools {
		p.closeIdle()
	}
}

// ServerState is how one news server is doing, for the Downloads page.
type ServerState struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Active int    `json:"active"` // connections in use
	Max    int    `json:"max"`
	Error  string `json:"error,omitempty"`
}

func (s *servers) states() []ServerState {
	var out []ServerState
	for _, p := range s.pools {
		st := ServerState{ID: p.srv.ID, Name: p.srv.Name, Active: int(p.active.Load()), Max: p.srv.Connections}
		if p.down() {
			st.Error, _ = p.lastErr.Load().(string)
		}
		out = append(out, st)
	}
	return out
}

// TestServer signs in to a news server and asks its time, to check the
// settings work.
func TestServer(ctx context.Context, u store.UsenetServer) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := nntp.Dial(ctx, nntp.Server{Host: u.Host, Port: u.Port, TLS: u.UseTLS, Username: u.Username, Password: u.Password, Timeout: 15 * time.Second})
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Date()
}
