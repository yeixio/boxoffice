package migrate

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultPorts are where each app listens unless it's been changed.
var defaultPorts = map[string]int{"radarr": 7878, "sonarr": 8989, "lidarr": 8686, "readarr": 8787, "nzbget": 6789}

// candidateURLs are the addresses an app is usually at from Box Office: its
// Kubernetes service or Compose service name, on its usual port, with no
// URL base or with one named after the app (as when it shares a hostname
// behind a reverse proxy, like example.com/radarr).
func candidateURLs(app string) []string {
	port, ok := defaultPorts[app]
	if !ok {
		return nil
	}
	hosts := []string{app}
	if ns := namespace(); ns != "" {
		hosts = append(hosts, app+"."+ns+".svc")
	}
	var out []string
	for _, h := range hosts {
		base := "http://" + h + ":" + strconv.Itoa(port)
		out = append(out, base, base+"/"+app)
	}
	return out
}

// namespace is the Kubernetes namespace Box Office runs in, if it does.
func namespace() string {
	b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Find looks for app at the addresses it's usually at and returns the first
// one that answers like it, or "" when none does. It sends no API key or
// password: an app that asks for one is enough to know it's there.
func (m *Migrator) Find(ctx context.Context, app string) string {
	app = strings.ToLower(strings.TrimSpace(app))
	urls := m.Candidates
	if urls == nil {
		urls = candidateURLs
	}
	cands := urls(app)
	if len(cands) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	found := make([]bool, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = answersAs(ctx, client, app, c)
		}()
	}
	wg.Wait()
	// An app under a URL base can also answer without it, before pointing
	// a request with a key at the base; so the one with the longest path
	// wins, and otherwise the first, the shortest host name.
	best, bestPath := "", -1
	for i, ok := range found {
		if !ok {
			continue
		}
		u, err := url.Parse(cands[i])
		if err != nil {
			continue
		}
		if len(u.Path) > bestPath {
			best, bestPath = cands[i], len(u.Path)
		}
	}
	return best
}

// answersAs says whether base looks like app's address. Radarr and Sonarr
// turn away their API without a key; NZBGet asks for its own sign-in.
func answersAs(ctx context.Context, client *http.Client, app, base string) bool {
	path := "/api/v3/system/status"
	if app == "nzbget" {
		path = "/jsonrpc/version"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if app == "nzbget" {
		// NZBGet names itself in its Server header, and in the sign-in it
		// asks for unless it uses its own sign-in page. With no password
		// set, it just answers.
		named := strings.Contains(strings.ToLower(strings.Join(append(res.Header.Values("Server"), res.Header.Values("WWW-Authenticate")...), " ")), "nzbget")
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return named
		case http.StatusOK:
			var r struct {
				Result any `json:"result"`
			}
			return json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&r) == nil && r.Result != nil
		}
		return false
	}
	return res.StatusCode == http.StatusUnauthorized
}

// mediaDirs are the folders Box Office could see media in: the volumes
// mounted into it, and the folders just inside them. Its own data folder,
// scratch space and the system's mounts are left out.
func mediaDirs(exclude []string) []string {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	skip := func(p string) bool {
		if p == "/" {
			return true
		}
		for _, x := range append([]string{"/proc", "/sys", "/dev", "/etc", "/run", "/var/run", "/tmp"}, exclude...) {
			if x != "" && (p == x || strings.HasPrefix(p, strings.TrimRight(x, "/")+"/")) {
				return true
			}
		}
		return false
	}
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		p := unescapeMount(fields[4])
		if skip(p) || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return withChildren(out)
}

// withChildren adds the folders just inside each of dirs, for a volume
// mounted a level above the library, such as /media holding movies and tv.
func withChildren(dirs []string) []string {
	out := slices.Clone(dirs)
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if p := filepath.Join(d, e.Name()); e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// unescapeMount undoes mountinfo's octal escapes, such as \040 for a space.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// findPathMaps works out where Box Office sees each of the old app's root
// folders it can't see at the same path. For each one, it looks for its
// titles' folders inside each of dirs, and picks the folder holding most of
// them. A root folder with no titles yet matches a folder of the same name.
// Titles maps each root folder to its titles' folders.
func findPathMaps(roots []string, titles map[string][]string, dirs []string) []PathMap {
	var maps []PathMap
	for _, root := range roots {
		root = filepath.Clean(root)
		if isDir(root) {
			continue
		}
		var rels []string
		for _, t := range titles[root] {
			if rel, err := filepath.Rel(root, filepath.Clean(t)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				rels = append(rels, rel)
			}
			if len(rels) == 20 {
				break
			}
		}
		best, bestScore := "", 0
		for _, d := range dirs {
			score := 0
			if len(rels) == 0 {
				if filepath.Base(d) == filepath.Base(root) && isDir(d) {
					score = 1
				}
			} else {
				for _, rel := range rels {
					if isDir(filepath.Join(d, rel)) {
						score++
					}
				}
				// Most of them, so a folder that happens to share a title or
				// two isn't taken for the library.
				if score*2 < len(rels) {
					score = 0
				}
			}
			if score > bestScore || (score == bestScore && score > 0 && filepath.Base(d) == filepath.Base(root) && filepath.Base(best) != filepath.Base(root)) {
				best, bestScore = d, score
			}
		}
		if best != "" {
			maps = append(maps, PathMap{From: root, To: best})
		}
	}
	return maps
}
