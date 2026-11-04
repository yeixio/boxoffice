// Package nntptest is a fake news server for tests.
package nntptest

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// Server serves articles from memory.
type Server struct {
	Username, Password string

	ln       net.Listener
	mu       sync.Mutex
	articles map[string][]byte
	Conns    atomic.Int32 // open now
	MaxConns atomic.Int32 // the most open at once
	Fetches  atomic.Int32
}

// New starts a server on a free local port.
func New() *Server {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s := &Server{ln: ln, articles: map[string][]byte{}}
	go s.serve()
	return s
}

// Add stores an article body (as the poster sent it, before dot-stuffing).
func (s *Server) Add(id string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.articles[id] = body
}

// Remove drops an article, as if it expired.
func (s *Server) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.articles, id)
}

func (s *Server) Host() string { return s.ln.Addr().(*net.TCPAddr).IP.String() }
func (s *Server) Port() int    { return s.ln.Addr().(*net.TCPAddr).Port }
func (s *Server) Close()       { s.ln.Close() }

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	n := s.Conns.Add(1)
	defer s.Conns.Add(-1)
	for {
		m := s.MaxConns.Load()
		if n <= m || s.MaxConns.CompareAndSwap(m, n) {
			break
		}
	}
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	fmt.Fprint(w, "200 fake news server ready\r\n")
	w.Flush()
	authed := s.Username == ""
	user := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(cmd) {
		case "AUTHINFO":
			k, v, _ := strings.Cut(arg, " ")
			if strings.EqualFold(k, "USER") {
				user = v
				fmt.Fprint(w, "381 password please\r\n")
			} else if user == s.Username && v == s.Password {
				authed = true
				fmt.Fprint(w, "281 welcome\r\n")
			} else {
				fmt.Fprint(w, "481 no\r\n")
			}
		case "DATE":
			fmt.Fprint(w, "111 20260101000000\r\n")
		case "QUIT":
			fmt.Fprint(w, "205 bye\r\n")
			w.Flush()
			return
		case "BODY":
			if !authed {
				fmt.Fprint(w, "480 sign in first\r\n")
				break
			}
			s.Fetches.Add(1)
			id := strings.Trim(arg, "<>")
			s.mu.Lock()
			body, ok := s.articles[id]
			s.mu.Unlock()
			if !ok {
				fmt.Fprint(w, "430 no such article\r\n")
				break
			}
			fmt.Fprintf(w, "222 0 <%s>\r\n", id)
			for _, l := range strings.SplitAfter(string(body), "\n") {
				if l == "" {
					continue
				}
				if l[0] == '.' {
					w.WriteByte('.')
				}
				w.WriteString(l)
			}
			fmt.Fprint(w, ".\r\n")
		default:
			fmt.Fprint(w, "500 what?\r\n")
		}
		w.Flush()
	}
}
