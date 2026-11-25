package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Wrong passwords are counted per client address and username together:
// five in a row lock that address out of that account for a minute, and
// each one after for a minute longer, up to fifteen. Someone guessing from
// elsewhere can't lock the real admin out of their own address, and an
// account's lock is held while a password is checked, so guesses sent at
// the same moment are counted one after the other.

const (
	lockAfter  = 5                // wrong tries before the first lock
	lockMost   = 15 * time.Minute // the longest lock
	lockForget = 30 * time.Minute // a count is dropped this long after its last wrong try
	lockKeep   = 10000            // counts kept at most
)

type failure struct {
	mu    sync.Mutex
	count int
	until time.Time
	last  time.Time
}

// wrong counts a wrong try. The caller holds f.mu.
func (f *failure) wrong(now time.Time) {
	f.count++
	f.last = now
	if f.count >= lockAfter {
		f.until = now.Add(min(time.Duration(f.count-lockAfter+1)*time.Minute, lockMost))
	}
}

type lockouts struct {
	mu sync.Mutex
	m  map[string]*failure
}

// entry gives the count for a key, making one if need be. Counts whose
// lock has run out and that have been quiet are dropped on the way, so a
// guessing run doesn't leave a list that grows without end.
func (l *lockouts) entry(key string, now time.Time) *failure {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*failure{}
	}
	if f, ok := l.m[key]; ok {
		return f
	}
	l.prune(now, len(l.m) >= lockKeep)
	f := &failure{}
	l.m[key] = f
	return f
}

// prune drops the counts that are over with; when full, any that aren't
// locked right now go too. One being checked this moment is left.
func (l *lockouts) prune(now time.Time, full bool) {
	for k, f := range l.m {
		if !f.mu.TryLock() {
			continue
		}
		done := now.After(f.until) && (full || now.Sub(f.last) > lockForget)
		f.mu.Unlock()
		if done {
			delete(l.m, k)
		}
	}
}

func (l *lockouts) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

func (l *lockouts) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// ParseProxies reads a list of the reverse proxies' addresses, as
// addresses or CIDR ranges split by commas or spaces
// ("10.42.0.0/16, 192.168.1.5").
func ParseProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, err
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

func (s *Server) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range s.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the address a request came from. Behind a reverse proxy
// that's the proxy's, for everyone, unless the proxy is one of
// TrustedProxies: then it's the last address in X-Forwarded-For that isn't
// a trusted proxy's, which is the one the outermost trusted proxy saw.
// X-Forwarded-For from anyone else is ignored, since anyone can send it.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	from, err := netip.ParseAddr(host)
	if err != nil || !s.trusted(from) {
		return host
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !s.trusted(a) {
			return a.Unmap().String()
		}
	}
	return host
}

// SessionSweepEvery clears sessions that have run out, at start and once a
// day, so they don't pile up until someone signs out.
func SessionSweepEvery(ctx context.Context, st *store.Store, log interface {
	Info(string, ...any)
	Warn(string, ...any)
}) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := st.DeleteExpiredSessions(ctx); err != nil {
			if ctx.Err() == nil {
				log.Warn("couldn't clear sessions that have run out", "err", err)
			}
		} else if n > 0 {
			log.Info("cleared sessions that had run out", "sessions", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
