package api

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/importer"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nzb"
)

// Adding an NZB by hand takes two steps: the NZB (a file, or a link to
// one) is checked first, which says what's in it and what Box Office
// makes of it, and then added, for a title or to be filed once it's
// downloaded. The NZB is kept between the two, so a link is fetched once.

// maxNZB is the biggest NZB taken: a big season pack's is a few MB.
const maxNZB = 64 << 20

// heldNZB is a checked NZB waiting to be added.
type heldNZB struct {
	data    []byte
	name    string
	expires time.Time
}

// nzbHold keeps checked NZBs for an hour, at most a few dozen.
type nzbHold struct {
	mu   sync.Mutex
	held map[string]heldNZB
}

func (h *nzbHold) put(data []byte, name string) string {
	b := make([]byte, 12)
	rand.Read(b)
	token := hex.EncodeToString(b)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil {
		h.held = map[string]heldNZB{}
	}
	now := time.Now()
	for k, v := range h.held {
		if now.After(v.expires) || len(h.held) > 40 {
			delete(h.held, k)
		}
	}
	h.held[token] = heldNZB{data: data, name: name, expires: now.Add(time.Hour)}
	return token
}

func (h *nzbHold) get(token string) (heldNZB, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.held[token]
	if !ok || time.Now().After(v.expires) {
		return heldNZB{}, false
	}
	return v, true
}

func (h *nzbHold) drop(token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.held, token)
}

// NZBCheck is what's in an NZB and what Box Office makes of it.
type NZBCheck struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Files int    `json:"files"`
	// Password says the NZB gives the password its archives need.
	Password bool `json:"password,omitempty"`
	// Guess is what Box Office thinks it is, from its name and files.
	Guess importer.Item `json:"guess"`
	// Kinds are what it can be filed as: the kinds the library takes.
	Kinds []string `json:"kinds"`
}

// NZBLink is a link to an NZB, to fetch.
type NZBLink struct {
	URL string `json:"url"`
}

// checkNZB reads an NZB sent as a file (its name in ?name=) or, as JSON,
// a link to fetch it from, and says what's in it.
func (s *Server) checkNZB(w http.ResponseWriter, r *http.Request) error {
	var data []byte
	name := r.URL.Query().Get("name")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var l NZBLink
		if err := readJSON(r, &l); err != nil {
			return err
		}
		var err error
		data, name, err = s.fetchNZB(r.Context(), l.URL)
		if err != nil {
			return errorf(http.StatusBadRequest, err.Error())
		}
	} else {
		var err error
		data, err = io.ReadAll(io.LimitReader(r.Body, maxNZB+1))
		if err != nil {
			return err
		}
		if len(data) > maxNZB {
			return errorf(http.StatusRequestEntityTooLarge, "that's too big to be an NZB")
		}
	}
	// Some indexers send NZBs gzipped (.nzb.gz).
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(zr, maxNZB+1))
		}
		if err != nil || len(data) > maxNZB {
			return errorf(http.StatusBadRequest, "that gzipped NZB can't be unpacked")
		}
		name = strings.TrimSuffix(name, ".gz")
	}
	n, err := nzb.Parse(bytes.NewReader(data))
	if err != nil {
		return errorf(http.StatusBadRequest, "that isn't an NZB Box Office can read: "+err.Error())
	}
	if len(n.Files) == 0 {
		return errorf(http.StatusBadRequest, "that NZB lists no files to download")
	}
	name = cleanNZBName(name)
	if t := strings.TrimSpace(n.Meta["title"]); t != "" {
		name = t
	}
	if name == "" {
		name = "download"
	}
	c := NZBCheck{Name: name, Size: n.Bytes(), Files: len(n.Files), Password: n.Meta["password"] != "" || strings.Contains(name, "{{")}
	var files []importer.ReleaseFile
	for _, f := range n.Files {
		if ok, _ := nzb.IsPar2(f.Name()); ok {
			continue
		}
		files = append(files, importer.ReleaseFile{Name: f.Name(), Size: f.Bytes()})
	}
	if s.Imports != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		c.Guess = s.Imports.GuessRelease(ctx, name, files, n.Meta["category"])
		cancel()
		c.Kinds = s.Imports.Kinds(r.Context())
	}
	c.Guess.Name = name
	c.Token = s.nzbs.put(data, name)
	return writeJSON(w, http.StatusOK, c)
}

// cleanNZBName is a release's name from its NZB's file name.
func cleanNZBName(name string) string {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, `\`, "/")))
	if strings.EqualFold(path.Ext(name), ".nzb") {
		name = name[:len(name)-4]
	}
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// fetchNZB fetches an NZB from a link, and gives the release's name from
// the file name the server sends, or the link's. A link to an indexer
// Box Office has, without its API key, is given the key.
func (s *Server) fetchNZB(ctx context.Context, link string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("paste a link that starts with http:// or https://")
	}
	if ixs, err := s.Store.Indexers(ctx); err == nil {
		q := u.Query()
		for _, ix := range ixs {
			iu, err := url.Parse(ix.URL)
			if err == nil && strings.EqualFold(iu.Hostname(), u.Hostname()) && ix.APIKey != "" && q.Get("apikey") == "" && q.Get("r") == "" && q.Get("i") == "" {
				q.Set("apikey", ix.APIKey)
				u.RawQuery = q.Encode()
				break
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Box Office/"+s.Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("couldn't get the NZB from %s: %v", u.Host, unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("%s answered %s", u.Host, resp.Status)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msg += "; the link may need your API key, or have run out"
		}
		return nil, "", errors.New(msg)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNZB+1))
	if err != nil {
		return nil, "", fmt.Errorf("couldn't get the NZB from %s: %v", u.Host, err)
	}
	if len(data) > maxNZB {
		return nil, "", errors.New("that link gives something too big to be an NZB")
	}
	name := ""
	if _, p, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = p["filename"]
	}
	if name == "" {
		name = path.Base(u.Path)
		if !strings.EqualFold(path.Ext(name), ".nzb") {
			name = ""
		}
	}
	return data, name, nil
}

// unwrapURL drops the "Get <url>:" from a fetch's error, so an API key in
// the link isn't shown.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// NZBAdd adds a checked NZB.
type NZBAdd struct {
	Token string `json:"token"`
	// Name is the release's name, if it's to be changed.
	Name string `json:"name,omitempty"`
	// For is the title it's for, as the path its releases are listed at:
	// /movies/12/releases, /episodes/34/releases,
	// /series/5/seasons/2/releases, /albums/7/releases,
	// /books/9/releases?format=ebook, /gamewants/3/releases.
	For string `json:"for,omitempty"`
	// FileAs, for none, is whether to file it in the library once it's
	// downloaded: "auto", or what it is; "" leaves it in the downloads
	// folder.
	FileAs string `json:"fileAs,omitempty"`
}

func (s *Server) addCheckedNZB(w http.ResponseWriter, r *http.Request) error {
	var a NZBAdd
	if err := readJSON(r, &a); err != nil {
		return err
	}
	held, ok := s.nzbs.get(a.Token)
	if !ok {
		return errorf(http.StatusBadRequest, "that NZB was checked too long ago; add it again")
	}
	d := store.Download{Name: cmp.Or(strings.TrimSpace(a.Name), held.name), Source: "upload"}
	if a.For != "" {
		var err error
		if d, err = s.downloadFor(r.Context(), d, a.For); err != nil {
			return err
		}
	} else if a.FileAs != "" {
		if !slices.Contains(importer.FileKinds, a.FileAs) {
			return errorf(http.StatusBadRequest, "Box Office can't file something as "+a.FileAs)
		}
		d.FileAs = a.FileAs
	}
	d, err := s.Downloads.Add(r.Context(), d, held.data)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	s.nzbs.drop(a.Token)
	return writeJSON(w, http.StatusCreated, d)
}

var reFor = regexp.MustCompile(`^/(movies|episodes|albums|books|gamewants)/(\d+)/releases$|^/series/(\d+)/seasons/(\d+)/releases$`)

// downloadFor ties a download to the title a releases path is for.
func (s *Server) downloadFor(ctx context.Context, d store.Download, p string) (store.Download, error) {
	u, err := url.Parse(p)
	if err != nil {
		return d, errorf(http.StatusBadRequest, "which title is it for?")
	}
	m := reFor.FindStringSubmatch(u.Path)
	if m == nil {
		return d, errorf(http.StatusBadRequest, "which title is it for?")
	}
	if m[3] != "" {
		id, _ := strconv.ParseInt(m[3], 10, 64)
		season, _ := strconv.Atoi(m[4])
		eps, err := s.Store.Episodes(ctx, id)
		if err != nil {
			return d, err
		}
		d.Kind, d.TitleID = "series", id
		for _, e := range eps {
			if e.Season == season {
				d.EpisodeIDs = append(d.EpisodeIDs, e.ID)
			}
		}
		if len(d.EpisodeIDs) == 0 {
			return d, errorf(http.StatusBadRequest, fmt.Sprintf("the show has no season %d", season))
		}
	} else {
		id, _ := strconv.ParseInt(m[2], 10, 64)
		switch m[1] {
		case "movies":
			if _, err := s.Store.Movie(ctx, id); err != nil {
				return d, err
			}
			d.Kind, d.TitleID = "movie", id
		case "episodes":
			e, err := s.Store.Episode(ctx, id)
			if err != nil {
				return d, err
			}
			d.Kind, d.TitleID, d.EpisodeIDs = "series", e.SeriesID, []int64{e.ID}
		case "albums":
			if _, err := s.Store.Album(ctx, id); err != nil {
				return d, err
			}
			d.Kind, d.TitleID = store.AlbumKind, id
		case "books":
			if _, err := s.Store.Book(ctx, id); err != nil {
				return d, err
			}
			format := u.Query().Get("format")
			if !store.IsBookKind(format) {
				format = store.Ebook
			}
			d.Kind, d.TitleID = format, id
		case "gamewants":
			if _, err := s.Store.GameWant(ctx, id); err != nil {
				return d, err
			}
			d.Kind, d.TitleID = store.GameKind, id
		}
	}
	if err := s.Store.CheckUnlocked(ctx, d.Kind, d.TitleID); err != nil {
		return d, errorf(http.StatusBadRequest, "it's on Do not touch, so Box Office won't download anything for it")
	}
	return d, nil
}
