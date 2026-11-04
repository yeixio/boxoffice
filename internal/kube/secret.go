// Package kube creates the bundled database's credentials from inside the cluster,
// so a fresh Helm install (or Argo CD sync) needs no hand-made Secret.
package kube

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// Client talks to the Kubernetes API with a bearer token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// InCluster builds a client from the pod's service account.
func InCluster() (*Client, string, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, "", fmt.Errorf("not running in a Kubernetes pod")
	}
	token, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return nil, "", err
	}
	ns, err := os.ReadFile(saDir + "/namespace")
	if err != nil {
		return nil, "", err
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	c := &Client{
		BaseURL: "https://" + host + ":" + port,
		Token:   strings.TrimSpace(string(token)),
		HTTP:    &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}},
	}
	return c, strings.TrimSpace(string(ns)), nil
}

// DatabaseSecret describes the Secret the chart's PostgreSQL and server read.
type DatabaseSecret struct {
	Namespace, Name string
	User, Database  string
	Host            string // PostgreSQL service name
	App             string // the app.kubernetes.io/name label; boxoffice when empty
}

// EnsureDatabaseSecret creates the Secret with a random password unless it exists.
// An existing Secret is never touched: the database was initialised with its password.
// It reports whether it created one.
func (c *Client) EnsureDatabaseSecret(ctx context.Context, s DatabaseSecret) (bool, error) {
	path := "/api/v1/namespaces/" + url.PathEscape(s.Namespace) + "/secrets"
	status, err := c.do(ctx, http.MethodGet, path+"/"+url.PathEscape(s.Name), nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusOK {
		return false, nil
	}
	if status != http.StatusNotFound {
		return false, fmt.Errorf("read secret %s: HTTP %d", s.Name, status)
	}

	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return false, err
	}
	password := hex.EncodeToString(b)
	dbURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(s.User, password),
		Host:     s.Host + ":5432",
		Path:     "/" + s.Database,
		RawQuery: "sslmode=disable",
	}).String()
	body, _ := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":   s.Name,
			"labels": map[string]string{"app.kubernetes.io/name": cmp.Or(s.App, "boxoffice")},
		},
		"type":       "Opaque",
		"stringData": map[string]string{"postgres-password": password, "database-url": dbURL},
	})
	status, err = c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusCreated:
		return true, nil
	case http.StatusConflict: // created by a concurrent run; keep theirs
		return false, nil
	}
	return false, fmt.Errorf("create secret %s: HTTP %d", s.Name, status)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}
