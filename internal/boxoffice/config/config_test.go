package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	env := map[string]string{"BOXOFFICE_ADDR": ":9000", "BOXOFFICE_DATA_DIR": "/data", "BOXOFFICE_REFRESH_EVERY": "6h"}
	c, err := load([]string{"-addr", ":9100"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9100" || c.DataDir != "/data" || c.Refresh != 6*time.Hour || c.Database() != "/data/boxoffice.db" {
		t.Fatalf("got %+v", c)
	}
	env["BOXOFFICE_DATABASE_URL"] = "postgres://x"
	c, _ = load(nil, func(k string) string { return env[k] })
	if c.Database() != "postgres://x" {
		t.Fatalf("database %q", c.Database())
	}
	if _, err := load([]string{"-refresh-every", "1m"}, func(string) string { return "" }); err == nil {
		t.Fatal("a one-minute refresh was accepted")
	}
}
