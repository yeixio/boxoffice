// Package nntp is a small NNTP client: enough to sign in to a news server
// and fetch article bodies by message id, which is all a downloader needs.
package nntp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"net"
	"strconv"
	"strings"
	"time"
)

// ErrNoArticle means the server doesn't have the article (430): it expired,
// was taken down, or never reached this server.
var ErrNoArticle = errors.New("the server doesn't have that article")

// ErrAuth means the server turned down the username or password.
var ErrAuth = errors.New("the news server turned down the username or password")

// Error is any other answer the server gave that isn't what was asked for.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("the news server answered %d %s", e.Code, e.Msg) }

// Temporary says whether trying again later, on a new connection, might
// work: the server is busy or has too many connections from this account.
func (e *Error) Temporary() bool { return e.Code == 400 || e.Code == 502 || e.Code == 503 }

// Server is where and how to connect.
type Server struct {
	Host     string
	Port     int
	TLS      bool
	Username string
	Password string
	// InsecureSkipVerify accepts any certificate, for servers whose
	// certificate doesn't name the host they're reached by.
	InsecureSkipVerify bool
	Timeout            time.Duration // per command; 0 is 60 seconds
}

func (s Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Conn is one signed-in connection. It isn't safe for use by more than one
// goroutine at a time.
type Conn struct {
	srv  Server
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

// Dial connects and signs in.
func Dial(ctx context.Context, srv Server) (*Conn, error) {
	if srv.Timeout == 0 {
		srv.Timeout = 60 * time.Second
	}
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	var nc net.Conn
	var err error
	if srv.TLS {
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: srv.Host, InsecureSkipVerify: srv.InsecureSkipVerify, MinVersion: tls.VersionTLS12}}
		nc, err = td.DialContext(ctx, "tcp", srv.Addr())
	} else {
		nc, err = d.DialContext(ctx, "tcp", srv.Addr())
	}
	if err != nil {
		return nil, err
	}
	c := &Conn{srv: srv, conn: nc, r: bufio.NewReaderSize(nc, 256<<10), w: bufio.NewWriter(nc)}
	c.deadline()
	code, msg, err := c.readStatus()
	if err != nil {
		nc.Close()
		return nil, err
	}
	if code != 200 && code != 201 {
		nc.Close()
		return nil, &Error{code, msg}
	}
	if srv.Username != "" {
		if err := c.auth(); err != nil {
			nc.Close()
			return nil, err
		}
	}
	return c, nil
}

func (c *Conn) deadline() { c.conn.SetDeadline(time.Now().Add(c.srv.Timeout)) }

func (c *Conn) auth() error {
	code, msg, err := c.cmd("AUTHINFO USER " + c.srv.Username)
	if err != nil {
		return err
	}
	if code == 381 {
		code, msg, err = c.cmd("AUTHINFO PASS " + c.srv.Password)
		if err != nil {
			return err
		}
	}
	switch code {
	case 281:
		return nil
	case 481, 482, 452:
		return ErrAuth
	}
	return &Error{code, msg}
}

func (c *Conn) cmd(line string) (int, string, error) {
	c.deadline()
	if _, err := c.w.WriteString(line + "\r\n"); err != nil {
		return 0, "", err
	}
	if err := c.w.Flush(); err != nil {
		return 0, "", err
	}
	return c.readStatus()
}

func (c *Conn) readStatus() (int, string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return 0, "", fmt.Errorf("the news server sent %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("the news server sent %q", line)
	}
	return code, strings.TrimSpace(line[3:]), nil
}

// Body fetches an article's body, with the dot-stuffing undone, appending
// it to buf (which may be nil) to save allocations.
func (c *Conn) Body(id string, buf []byte) ([]byte, error) {
	code, msg, err := c.cmd("BODY <" + id + ">")
	if err != nil {
		return buf, err
	}
	if code == 480 { // signed out, as some servers do after a while
		if err := c.auth(); err != nil {
			return buf, err
		}
		if code, msg, err = c.cmd("BODY <" + id + ">"); err != nil {
			return buf, err
		}
	}
	switch code {
	case 222:
	case 430, 423:
		return buf, ErrNoArticle
	default:
		return buf, &Error{code, msg}
	}
	for {
		c.deadline()
		line, err := c.r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			buf = append(buf, line...)
			continue
		}
		if err != nil {
			return buf, err
		}
		if line[0] == '.' {
			if bytes.Equal(line, []byte(".\r\n")) || bytes.Equal(line, []byte(".\n")) {
				return buf, nil
			}
			line = line[1:]
		}
		buf = append(buf, line...)
	}
}

// Date asks the server its time: a cheap way to check a connection works.
func (c *Conn) Date() error {
	code, msg, err := c.cmd("DATE")
	if err != nil {
		return err
	}
	if code != 111 {
		return &Error{code, msg}
	}
	return nil
}

// Close says goodbye and hangs up.
func (c *Conn) Close() error {
	c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	c.w.WriteString("QUIT\r\n")
	c.w.Flush()
	return c.conn.Close()
}
