package nntp

import (
	"context"
	"errors"
	"testing"

	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

func TestBody(t *testing.T) {
	s := nntptest.New()
	defer s.Close()
	s.Username, s.Password = "me", "pw"
	s.Add("a@b", []byte("line one\r\n.starts with a dot\r\n..two dots\r\nend\r\n"))
	ctx := context.Background()
	if _, err := Dial(ctx, Server{Host: s.Host(), Port: s.Port(), Username: "me", Password: "nope"}); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	c, err := Dial(ctx, Server{Host: s.Host(), Port: s.Port(), Username: "me", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	body, err := c.Body("a@b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "line one\r\n.starts with a dot\r\n..two dots\r\nend\r\n"; string(body) != want {
		t.Fatalf("got %q", body)
	}
	if _, err := c.Body("missing@b", nil); !errors.Is(err, ErrNoArticle) {
		t.Fatalf("missing: %v", err)
	}
	if err := c.Date(); err != nil {
		t.Fatal(err)
	}
}
