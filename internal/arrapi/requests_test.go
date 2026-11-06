package arrapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/portal"
	"github.com/yeixio/boxoffice/internal/store"
)

// withRequests serves the arr API with requests from Matinee taken.
func withRequests(t *testing.T, e env) (string, *portal.Service) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := &portal.Service{Store: e.cat.Store, Catalog: e.cat, Log: log}
	keyFn := func(context.Context) string { return key }
	mux := http.NewServeMux()
	for _, app := range []arrapi.App{arrapi.Radarr, arrapi.Sonarr} {
		h := arrapi.New(app, e.cat, keyFn, "test", log)
		h.Requests = p
		mux.Handle("/"+string(app)+"/", http.StripPrefix("/"+string(app), h))
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, p
}

func send(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// What people ask for in Matinee lands on the Requests page under their
// Matinee account, whether or not the request site is turned on.
func TestRequestsFromMatinee(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if f := features(t, e.url+"/radarr"); slices.Contains(f, "requests") {
		t.Fatalf("without the request desk: %v", f)
	}
	url, p := withRequests(t, e)
	if f := features(t, url+"/radarr"); !slices.Contains(f, "requests") {
		t.Fatalf("features: %v", f)
	}

	var v portal.View
	if c := send(t, "POST", url+"/radarr/api/v3/boxoffice/requests", map[string]any{"user": "Ellen", "tmdbId": 603}, &v); c != 201 {
		t.Fatalf("ask: %d", c)
	}
	if v.State != portal.StatePending || v.Title != "The Matrix" || v.Kind != "movie" || v.ID == 0 {
		t.Fatalf("asked: %+v", v)
	}
	m, err := e.cat.Store.MemberByName(ctx, "ellen")
	if err != nil || m.Source != "matinee" || m.DisplayName != "Ellen" || v.MemberID != m.ID {
		t.Fatalf("member: %+v, %v", m, err)
	}
	// Asking again is the same request.
	var again portal.View
	if c := send(t, "POST", url+"/radarr/api/v3/boxoffice/requests", map[string]any{"user": "ellen", "tmdbId": 603}, &again); c != 201 || again.ID != v.ID {
		t.Fatalf("again %d: %+v", c, again)
	}
	var states []portal.View
	if c := getJSON(t, url+"/radarr/api/v3/boxoffice/requests?ids="+itoa(v.ID)+",999", &states); c != 200 || len(states) != 1 || states[0].State != portal.StatePending {
		t.Fatalf("states %d: %+v", c, states)
	}

	// An admin approves it in Matinee: it's added.
	var ok portal.View
	if c := send(t, "POST", url+"/radarr/api/v3/boxoffice/requests/"+itoa(v.ID)+"/approve", nil, &ok); c != 200 || ok.Status != store.RequestApproved || ok.TitleID == 0 {
		t.Fatalf("approve %d: %+v", c, ok)
	}

	// A weekly limit is said in words Matinee can show.
	if _, err := p.SaveSettings(ctx, portal.Settings{Name: "Requests", LocalSignIn: true, QuotaShows: 1}); err != nil {
		t.Fatal(err)
	}
	var sv portal.View
	if c := send(t, "POST", url+"/sonarr/api/v3/boxoffice/requests", map[string]any{"user": "ellen", "tmdbId": 95396}, &sv); c != 201 || sv.Kind != "series" {
		t.Fatalf("show %d: %+v", c, sv)
	}
	var msg map[string]string
	if c := send(t, "POST", url+"/sonarr/api/v3/boxoffice/requests", map[string]any{"user": "ellen", "tmdbId": 1001}, &msg); c != 422 || msg["message"] == "" {
		t.Fatalf("over the limit %d: %v", c, msg)
	}
	// Declined, with a reason; and one taken back while it waits is gone.
	var no portal.View
	if c := send(t, "POST", url+"/sonarr/api/v3/boxoffice/requests/"+itoa(sv.ID)+"/decline", map[string]string{"reply": "Not this one"}, &no); c != 200 || no.State != portal.StateDeclined || no.Reply != "Not this one" {
		t.Fatalf("decline %d: %+v", c, no)
	}

	// Approved by Matinee (an admin there): added straight away, no limit.
	var added portal.View
	if c := send(t, "POST", url+"/radarr/api/v3/boxoffice/requests", map[string]any{"user": "mike", "tmdbId": 1001, "approved": true}, &added); c != 201 || added.Status != store.RequestApproved {
		t.Fatalf("approved %d: %+v", c, added)
	}

	var gone portal.View
	send(t, "POST", url+"/sonarr/api/v3/boxoffice/requests", map[string]any{"user": "sam", "tmdbId": 95396}, &gone)
	if gone.State != portal.StatePending {
		t.Fatalf("sam: %+v", gone)
	}
	if c := send(t, "DELETE", url+"/sonarr/api/v3/boxoffice/requests/"+itoa(gone.ID), nil, nil); c != 204 {
		t.Fatalf("withdraw: %d", c)
	}
	if _, err := e.cat.Store.Request(ctx, gone.ID); err != store.ErrNotFound {
		t.Fatalf("still there: %v", err)
	}

	// A turned-off account can't ask.
	sam, _ := e.cat.Store.MemberByName(ctx, "sam")
	sam.Disabled = true
	e.cat.Store.UpdateMember(ctx, sam)
	if c := send(t, "POST", url+"/radarr/api/v3/boxoffice/requests", map[string]any{"user": "sam", "tmdbId": 603}, &msg); c != 422 {
		t.Fatalf("disabled: %d", c)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
