package downloader

import (
	"context"
	"errors"
	"slices"
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
}

func newPool(s store.UsenetServer) *pool {
	n := max(s.Connections, 1)
	return &pool{srv: s, slots: make(chan struct{}, n), cfg: nntp.Server{Host: s.Host, Port: s.Port, TLS: s.UseTLS, Username: s.Username, Password: s.Password}}
}

func (p *pool) down() bool { return time.Now().Unix() < p.downUntil.Load() }

func (p *pool) rest(err error, d time.Duration) {
	p.downUntil.Store(time.Now().Add(d).Unix())
	p.lastErr.Store(err.Error())
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
	c, err := nntp.Dial(ctx, p.cfg)
	if err != nil {
		<-p.slots
		return nil, err
	}
	p.active.Add(1)
	return c, nil
}

// put returns a connection; a broken one (err set) is closed instead.
func (p *pool) put(c *nntp.Conn, broken bool) {
	p.active.Add(-1)
	if broken {
		c.Close()
	} else {
		p.mu.Lock()
		p.idle = append(p.idle, c)
		p.mu.Unlock()
	}
	<-p.slots
}

func (p *pool) closeIdle() {
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
}

func newServers(list []store.UsenetServer) *servers {
	s := &servers{}
	for _, u := range list {
		if u.Enabled && u.Host != "" {
			s.pools = append(s.pools, newPool(u))
		}
	}
	slices.SortStableFunc(s.pools, func(a, b *pool) int { return a.srv.Level - b.srv.Level })
	return s
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
