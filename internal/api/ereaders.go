package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/ereader"
	"github.com/yeixio/boxoffice/internal/store"
)

// Send to e-readers (Settings): Box Office emails ebooks to Kindles,
// PocketBooks and any other address that takes them, by hand from a
// book's page or by itself as each one downloads (see internal/ereader).

// ereaders is the e-reader sender: the one main.go made, or one made here.
func (s *Server) ereaders() *ereader.Service {
	s.ereaderOnce.Do(func() {
		if s.Ereaders == nil {
			s.Ereaders = &ereader.Service{Store: s.Store, Log: s.Log}
		}
		if s.Ereaders.AlertMail == nil {
			s.Ereaders.AlertMail = func(ctx context.Context) (alerts.Email, error) {
				c, err := s.Alerts().Config(ctx)
				return c.Email, err
			}
		}
	})
	return s.Ereaders
}

// ereadersView is GET /api/v1/ereaders. The password never leaves the
// server.
type ereadersView struct {
	Mail    ereadersMailView `json:"mail"`
	Devices []ereader.Device `json:"devices"`
	// AlertMail is the mail server set up under Health → Alerts, which
	// books can go out through too.
	AlertMail ereadersAlertMail `json:"alertMail"`
	// From is the address books come from with these settings: the one
	// to approve with Amazon and PocketBook.
	From string `json:"from"`
	// CanConvert says Calibre's ebook-convert is there, to turn books a
	// reader won't take into EPUB.
	CanConvert bool                `json:"canConvert"`
	History    []store.EreaderSend `json:"history"`
}

type ereadersMailView struct {
	ereader.Mail
	HasPassword bool `json:"hasPassword"`
}

type ereadersAlertMail struct {
	Ready bool   `json:"ready"`
	Host  string `json:"host,omitempty"`
	From  string `json:"from,omitempty"`
}

func (s *Server) getEreaders(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	e := s.ereaders()
	c, err := e.Config(ctx)
	if err != nil {
		return err
	}
	am := e.AlertServer(ctx)
	hist, err := s.Store.EreaderSends(ctx, 30)
	if err != nil {
		return err
	}
	v := ereadersView{Devices: c.Devices, History: hist, CanConvert: e.Converter() != "",
		AlertMail: ereadersAlertMail{Ready: am.Host != "", Host: am.Host, From: am.From},
		From:      c.Server(am).From,
	}
	v.Mail.Mail, v.Mail.HasPassword = c.Mail, c.Mail.Password != ""
	v.Mail.Password = ""
	return writeJSON(w, http.StatusOK, v)
}

// mergeEreaders keeps the password while the server and username stay
// the same, and gives new readers their ids.
func mergeEreaders(old, c ereader.Config) ereader.Config {
	c.Normalize()
	if c.Mail.Password == "" && c.Mail.Host == old.Mail.Host && c.Mail.Username == old.Mail.Username {
		c.Mail.Password = old.Mail.Password
	}
	for i := range c.Devices {
		d := &c.Devices[i]
		if d.ID != "" {
			if _, ok := old.Device(d.ID); ok {
				continue
			}
		}
		b := make([]byte, 6)
		rand.Read(b)
		d.ID = hex.EncodeToString(b)
	}
	return c
}

func (s *Server) putEreaders(w http.ResponseWriter, r *http.Request) error {
	var in ereader.Config
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	e := s.ereaders()
	old, err := e.Config(ctx)
	if err != nil {
		return err
	}
	c := mergeEreaders(old, in)
	if err := c.Check(e.AlertServer(ctx)); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	if err := e.SaveConfig(ctx, c); err != nil {
		return err
	}
	s.Log.Info("e-reader settings saved", "by", currentUser(r), "readers", len(c.Devices))
	return s.getEreaders(w, r)
}

// testEreader sends a small test book to one reader, with the settings in
// the request (so they can be tried before saving) or the saved ones. A
// reader not saved yet is named by its place in the list: "new0" and on.
func (s *Server) testEreader(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	e := s.ereaders()
	c, err := e.Config(ctx)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if r.ContentLength != 0 {
		var in ereader.Config
		if err := readJSON(r, &in); err != nil {
			return err
		}
		// New readers keep their form ids here, so the one to try is found.
		ids := make([]string, len(in.Devices))
		for i, d := range in.Devices {
			ids[i] = d.ID
		}
		c = mergeEreaders(c, in)
		for i := range c.Devices {
			if ids[i] == id {
				id = c.Devices[i].ID
			}
		}
	}
	if _, ok := c.Device(id); !ok {
		return errorf(http.StatusBadRequest, "there's no such e-reader")
	}
	if err := c.Check(e.AlertServer(ctx)); err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	sent, _ := e.Test(ctx, c, id)
	return writeJSON(w, http.StatusOK, sent)
}

// sendBook sends a book's ebook to a reader now.
func (s *Server) sendBook(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Device string `json:"device"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.Device = strings.TrimSpace(in.Device)
	ctx := r.Context()
	e := s.ereaders()
	c, err := e.Config(ctx)
	if err != nil {
		return err
	}
	if _, ok := c.Device(in.Device); !ok {
		return errorf(http.StatusBadRequest, "pick an e-reader; add one in Settings")
	}
	if _, err := s.Store.Book(ctx, id); err != nil {
		return err
	}
	sent, _ := e.Send(ctx, in.Device, id, false)
	return writeJSON(w, http.StatusOK, sent)
}
