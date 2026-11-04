package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestEnsureDatabaseSecret(t *testing.T) {
	stored := map[string]map[string]any{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/media/secrets/matinee-db":
			if stored["matinee-db"] == nil {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/media/secrets":
			var s map[string]any
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &s)
			stored["matinee-db"] = s
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	c := &Client{BaseURL: ts.URL, Token: "tok", HTTP: ts.Client()}
	spec := DatabaseSecret{Namespace: "media", Name: "matinee-db", User: "matinee", Database: "matinee", Host: "matinee-postgresql"}
	created, err := c.EnsureDatabaseSecret(context.Background(), spec)
	if err != nil || !created {
		t.Fatalf("first run: created=%v err=%v", created, err)
	}
	data := stored["matinee-db"]["stringData"].(map[string]any)
	password := data["postgres-password"].(string)
	if len(password) != 48 {
		t.Fatalf("password %q", password)
	}
	u, err := url.Parse(data["database-url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := u.User.Password(); got != password || u.Host != "matinee-postgresql:5432" || u.Path != "/matinee" {
		t.Fatalf("database-url %s doesn't match the password and service", u)
	}

	// A second run keeps the existing password, which the database was created with.
	created, err = c.EnsureDatabaseSecret(context.Background(), spec)
	if err != nil || created {
		t.Fatalf("second run: created=%v err=%v", created, err)
	}
}
