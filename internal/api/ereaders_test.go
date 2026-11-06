package api

import (
	"context"
	"testing"

	"github.com/yeixio/boxoffice/internal/alerts"
	"github.com/yeixio/boxoffice/internal/ereader"
)

// E-readers use the alerts' mail server unless told otherwise, keep their
// password like alerts do, and can be tried before they're saved.
func TestEreaderSettings(t *testing.T) {
	s, hs, _ := monitorServer(t)
	var tos []string
	s.Ereaders = &ereader.Service{Store: s.Store, Log: s.Log,
		Deliver: func(_ context.Context, e alerts.Email, from string, to []string, msg []byte) error {
			tos = append(tos, to...)
			return nil
		}}
	type view struct {
		Mail struct {
			SameAsAlerts bool   `json:"sameAsAlerts"`
			Host         string `json:"host"`
			Password     string `json:"password"`
			HasPassword  bool   `json:"hasPassword"`
		} `json:"mail"`
		Devices   []ereader.Device `json:"devices"`
		AlertMail struct {
			Ready bool `json:"ready"`
		} `json:"alertMail"`
		From string `json:"from"`
	}
	var v view
	if code := call(t, hs, "GET", "/api/v1/ereaders", "", &v); code != 200 || !v.Mail.SameAsAlerts || v.AlertMail.Ready || len(v.Devices) != 0 {
		t.Fatalf("fresh: %d %+v", code, v)
	}
	// No alert mail server yet: a reader can't be saved on it.
	if code := call(t, hs, "PUT", "/api/v1/ereaders", `{"mail":{"sameAsAlerts":true},"devices":[{"name":"Kindle","address":"me@kindle.com","auto":true}]}`, nil); code != 400 {
		t.Fatalf("no alert mail: %d", code)
	}
	if code := call(t, hs, "PUT", "/api/v1/admin/alerts", `{"email":{"on":true,"host":"smtp.example.com","port":587,"security":"starttls","username":"u","password":"secret","from":"bo@example.com","to":"me@example.com"},"rules":{},"webhooks":[]}`, nil); code != 200 {
		t.Fatalf("alerts: %d", code)
	}
	body := `{"mail":{"sameAsAlerts":true},"devices":[{"id":"new0","name":"Kindle","address":"me@kindle.com","auto":true}]}`
	var sent struct {
		Error string `json:"error"`
		Title string `json:"title"`
	}
	if code := call(t, hs, "POST", "/api/v1/ereaders/new0/test", body, &sent); code != 200 || sent.Error != "" || sent.Title != ereader.TestTitle || len(tos) != 1 || tos[0] != "me@kindle.com" {
		t.Fatalf("test before saving: %d %+v %v", code, sent, tos)
	}
	if code := call(t, hs, "PUT", "/api/v1/ereaders", body, &v); code != 200 || len(v.Devices) != 1 || v.Devices[0].ID == "new0" || v.Devices[0].Kind != ereader.KindKindle || v.From != "bo@example.com" || !v.AlertMail.Ready {
		t.Fatalf("save: %d %+v", code, v)
	}
	// Its own server: the password stays on the server.
	own := `{"mail":{"host":"smtp.gmail.com","port":587,"security":"starttls","username":"me@gmail.com","password":"app-pass","from":"me@gmail.com"},"devices":[{"id":"` + v.Devices[0].ID + `","address":"me@kindle.com"}]}`
	if code := call(t, hs, "PUT", "/api/v1/ereaders", own, &v); code != 200 || v.Mail.Password != "" || !v.Mail.HasPassword || v.From != "me@gmail.com" {
		t.Fatalf("own server: %d %+v", code, v)
	}
	again := `{"mail":{"host":"smtp.gmail.com","port":587,"security":"starttls","username":"me@gmail.com","from":"me@gmail.com"},"devices":[{"id":"` + v.Devices[0].ID + `","address":"me@kindle.com"}]}`
	call(t, hs, "PUT", "/api/v1/ereaders", again, &v)
	c, _ := s.Ereaders.Config(context.Background())
	if c.Mail.Password != "app-pass" {
		t.Errorf("password = %q, want it kept", c.Mail.Password)
	}
	// A book with no ebook can't go, and says why.
	if code := call(t, hs, "POST", "/api/v1/books/1/send", `{"device":"nope"}`, nil); code != 400 {
		t.Errorf("unknown reader: %d", code)
	}
}
