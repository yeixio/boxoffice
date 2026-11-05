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
	if !c.Announce {
		t.Fatal("announcing is off by default")
	}
	env["BOXOFFICE_ANNOUNCE"] = "false"
	if c, _ = load(nil, func(k string) string { return env[k] }); c.Announce {
		t.Fatal("BOXOFFICE_ANNOUNCE=false still announces")
	}
	if _, err := load([]string{"-refresh-every", "1m"}, func(string) string { return "" }); err == nil {
		t.Fatal("a one-minute refresh was accepted")
	}
}

func TestTMDBKey(t *testing.T) {
	defer func(k string) { DefaultTMDBKey = k }(DefaultTMDBKey)
	none := func(string) string { return "" }
	DefaultTMDBKey = ""
	if c, _ := load(nil, none); c.TMDBKey != "" {
		t.Fatalf("a build without a key has %q", c.TMDBKey)
	}
	DefaultTMDBKey = "built-in"
	if c, _ := load(nil, none); c.TMDBKey != "built-in" {
		t.Fatalf("the built-in key isn't used: %q", c.TMDBKey)
	}
	own := func(k string) string {
		if k == "BOXOFFICE_TMDB_KEY" {
			return "mine"
		}
		return ""
	}
	if c, _ := load(nil, own); c.TMDBKey != "mine" {
		t.Fatalf("BOXOFFICE_TMDB_KEY doesn't win over the built-in key: %q", c.TMDBKey)
	}
}
