// Package ereader sends ebooks to e-readers by email: Send to Kindle,
// Send-to-PocketBook, or any other address that takes ebooks as
// attachments. Admins set it up in Settings → Send to e-readers; a reader
// marked "send new ebooks" gets each ebook that downloads, once, and any
// book can be sent by hand from its page.
package ereader

import (
	"errors"
	"net/mail"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/alerts"
)

// Kinds of e-reader. The kind sets which formats are sent as they are.
const (
	KindKindle     = "kindle"
	KindPocketBook = "pocketbook"
	KindOther      = "other"
)

// Setting keys.
const settingConfig = "ereaders"

// MaxDevices is how many e-readers can be set up.
const MaxDevices = 20

// Config is what admins set up in Settings → Send to e-readers.
type Config struct {
	Mail    Mail     `json:"mail"`
	Devices []Device `json:"devices"`
}

// Mail is the mail server books go out through.
type Mail struct {
	// SameAsAlerts uses the mail server set up under Health → Alerts, so
	// it's typed in once.
	SameAsAlerts bool   `json:"sameAsAlerts"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Security     string `json:"security"` // starttls, tls or none
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	// From is the address books come from: the one to approve in Amazon's
	// and PocketBook's settings.
	From string `json:"from"`
}

// Device is one e-reader, by its email address.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Address string `json:"address"`
	// Auto sends each ebook that downloads to this reader.
	Auto bool `json:"auto"`
}

// KindOf works out an e-reader's kind from its address: Amazon's and
// PocketBook's have their own domains.
func KindOf(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	switch domain {
	case "kindle.com", "free.kindle.com", "kindle.cn":
		return KindKindle
	case "pbsync.com":
		return KindPocketBook
	}
	return KindOther
}

// Normalize fills in defaults and tidies what was typed.
func (c *Config) Normalize() {
	m := &c.Mail
	m.Host, m.Username, m.From = strings.TrimSpace(m.Host), strings.TrimSpace(m.Username), strings.TrimSpace(m.From)
	if m.Security == "" {
		m.Security = "starttls"
	}
	if m.Port == 0 {
		m.Port = map[string]int{"starttls": 587, "tls": 465, "none": 25}[m.Security]
	}
	if c.Devices == nil {
		c.Devices = []Device{}
	}
	for i := range c.Devices {
		d := &c.Devices[i]
		d.Name, d.Address = strings.TrimSpace(d.Name), strings.TrimSpace(d.Address)
		if d.Kind == "" {
			d.Kind = KindOf(d.Address)
		}
	}
}

// Server is the mail server to send through: the alerts' one, or the
// e-readers' own.
func (c Config) Server(alertMail alerts.Email) alerts.Email {
	if c.Mail.SameAsAlerts {
		e := alertMail
		e.From, e.To = c.Mail.From, ""
		if e.From == "" {
			e.From = alertMail.From
		}
		return e
	}
	m := c.Mail
	return alerts.Email{On: true, Host: m.Host, Port: m.Port, Security: m.Security, Username: m.Username, Password: m.Password, From: m.From}
}

// Check says what's wrong with the settings, if anything, in words for
// the admin. alertMail is the mail server set up for alerts.
func (c Config) Check(alertMail alerts.Email) error {
	if len(c.Devices) > MaxDevices {
		return errors.New("that's too many e-readers")
	}
	seen := map[string]bool{}
	for _, d := range c.Devices {
		if _, err := mail.ParseAddress(d.Address); err != nil || !strings.Contains(d.Address, "@") {
			if d.Name != "" {
				return errors.New("enter " + d.Name + "'s email address, such as yourname@kindle.com")
			}
			return errors.New("enter each e-reader's email address, such as yourname@kindle.com")
		}
		if !slices.Contains([]string{KindKindle, KindPocketBook, KindOther}, d.Kind) {
			return errors.New("unknown kind of e-reader")
		}
		a := strings.ToLower(d.Address)
		if seen[a] {
			return errors.New(d.Address + " is on the list twice")
		}
		seen[a] = true
	}
	if len(c.Devices) == 0 {
		return nil
	}
	if c.Mail.SameAsAlerts {
		if alertMail.Host == "" {
			return errors.New("there's no mail server under Health → Alerts yet; set one up there, or enter one here")
		}
	} else {
		if c.Mail.Host == "" {
			return errors.New("enter the mail server, such as smtp.gmail.com")
		}
		if c.Mail.Port <= 0 || c.Mail.Port > 65535 {
			return errors.New("enter the mail server's port: usually 587, or 465 for TLS")
		}
		switch c.Mail.Security {
		case "starttls", "tls", "none":
		default:
			return errors.New("pick how to connect to the mail server")
		}
	}
	if _, err := mail.ParseAddress(c.Server(alertMail).From); err != nil {
		return errors.New("enter the address books come from")
	}
	return nil
}

// Device finds an e-reader by its id.
func (c Config) Device(id string) (Device, bool) {
	i := slices.IndexFunc(c.Devices, func(d Device) bool { return d.ID == id })
	if i < 0 {
		return Device{}, false
	}
	return c.Devices[i], true
}
