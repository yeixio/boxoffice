package games

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/gamedat"
)

// DefaultTitleDB is where the Switch's list of games comes from: the
// eShop's games, gathered by the titledb project. No account is needed.
const DefaultTitleDB = "https://raw.githubusercontent.com/blawar/titledb/master"

// listNames are the lists' names as people know them.
var listNames = map[string]string{"no-intro": "No-Intro", "redump": "Redump", "tosec": "TOSEC", "titledb": "Nintendo eShop"}

// systemLists reads a system's lists: for a cartridge system No-Intro's,
// for a disc system Redump's (and No-Intro's of its downloads), for the
// Switch the eShop's. A system with none gives none and no error; games
// for it are known by name. label names the lists and their dates, as
// "Redump 2026.10.07".
func (s *Service) systemLists(ctx context.Context, sys gamedat.System) (dats []*gamedat.DAT, label string, err error) {
	var labels []string
	for _, l := range sys.Lists {
		var dat *gamedat.DAT
		kind, name, _ := strings.Cut(l, "/")
		if kind == "titledb" {
			dat, err = s.titleDB(ctx)
		} else {
			var p string
			p, err = s.libretroDAT(ctx, kind, name)
			if err == nil && p == "" {
				err = fmt.Errorf("libretro's database has no %s list called %s", listNames[kind], name)
			}
			if err == nil {
				dat, err = readDAT(p)
			}
		}
		if err != nil {
			// A system's first list is the one it can't do without.
			if len(dats) == 0 {
				return nil, "", fmt.Errorf("couldn't get %s's list of %s games: %w", listNames[kind], sys.Name, err)
			}
			s.log().Warn("couldn't get a game list", "list", l, "err", err)
			err = nil
			continue
		}
		dats = append(dats, dat)
		lbl := listNames[kind]
		if v := dat.Header["version"]; v != "" && kind != "titledb" {
			lbl += " " + v
		}
		if !contains(labels, lbl) {
			labels = append(labels, lbl)
		}
	}
	return dats, strings.Join(labels, ", "), nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// listIndex is a system's lists indexed: by CRC, name, title and serial.
func (s *Service) listIndex(ctx context.Context, sys gamedat.System) (*gamedat.Index, string, error) {
	dats, label, err := s.systemLists(ctx, sys)
	if err != nil {
		return nil, "", err
	}
	ix := gamedat.NewIndex(&gamedat.DAT{})
	for _, d := range dats {
		ix.Add(d)
	}
	return ix, label, nil
}

// noIntro is No-Intro's list for a console, from libretro's database.
func (s *Service) noIntro(ctx context.Context, sys gamedat.System) (*gamedat.Index, string, error) {
	ix, label, err := s.listIndex(ctx, sys)
	if err != nil {
		return nil, "", err
	}
	_, version, _ := strings.Cut(label, " ")
	version, _, _ = strings.Cut(version, ",")
	return ix, version, nil
}

// entryFacts are a game's facts: libretro's, by its first file's CRC,
// and those its list gives itself (the eShop's do).
func entryFacts(fi gamedat.FactIndex, e *gamedat.Entry) gamedat.Facts {
	var f gamedat.Facts
	if len(e.Roms) > 0 && e.Roms[0].HasCRC {
		if x := fi[e.Roms[0].CRC]; x != nil {
			f = *x
		}
	}
	f.Year = cmp.Or(f.Year, e.Fields["releaseyear"])
	f.Publisher = cmp.Or(f.Publisher, e.Fields["publisher"])
	f.Developer = cmp.Or(f.Developer, e.Fields["developer"])
	f.Genre = cmp.Or(f.Genre, e.Fields["genre"])
	if f.Players == 0 {
		f.Players, _ = strconv.Atoi(e.Fields["users"])
	}
	return f
}

// The Switch's list.

// titleDBEntry is one game in titledb's list of the eShop.
type titleDBEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Publisher   string   `json:"publisher"`
	Developer   string   `json:"developer"`
	ReleaseDate int      `json:"releaseDate"`
	Players     int      `json:"numberOfPlayers"`
	Category    []string `json:"category"`
	IconURL     string   `json:"iconUrl"`
	BoxArt      string   `json:"frontBoxArt"`
	Size        int64    `json:"size"`
	IsDemo      bool     `json:"isDemo"`
}

// titleDB is the Switch's list of games: titledb's list of the US eShop,
// downloaded the first time and again after a month, and kept as just
// the games (no updates or DLC) in a list of its own, which is far
// smaller.
func (s *Service) titleDB(ctx context.Context) (*gamedat.DAT, error) {
	small := filepath.Join(s.Dir, "titledb", "switch.json")
	if st, err := os.Stat(small); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		return readTitleDB(small)
	}
	s.listMu.Lock()
	defer s.listMu.Unlock()
	if st, err := os.Stat(small); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		return readTitleDB(small)
	}
	big := filepath.Join(s.Dir, "titledb", "US.en.json")
	err := s.download(ctx, cmp.Or(s.TitleDB, DefaultTitleDB)+"/US.en.json", big)
	if err == nil {
		err = shrinkTitleDB(big, small)
		os.Remove(big)
	}
	if err != nil {
		if _, serr := os.Stat(small); serr == nil {
			s.log().Warn("couldn't update the Switch's game list; using the one downloaded before", "err", err)
			return readTitleDB(small)
		}
		return nil, err
	}
	return readTitleDB(small)
}

// shrinkTitleDB keeps titledb's games, not their updates and DLC, and
// only what Box Office uses of each.
func shrinkTitleDB(from, to string) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("titledb's list isn't what Box Office expects")
	}
	out := []titleDBEntry{}
	seen := map[string]bool{}
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return err
		}
		var e titleDBEntry
		if err := dec.Decode(&e); err != nil {
			return err
		}
		e.ID = strings.ToUpper(e.ID)
		if len(e.ID) != 16 || !strings.HasSuffix(e.ID, "000") || e.Name == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		e.Category = e.Category[:min(len(e.Category), 1)]
		out = append(out, e)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
		return err
	}
	tmp := to + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

// cleanTM takes the trademark signs out of an eShop name.
var cleanTM = strings.NewReplacer("™", "", "®", "", "©", "", " ", " ")

func readTitleDB(p string) (*gamedat.DAT, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var es []titleDBEntry
	if err := json.NewDecoder(io.LimitReader(f, 256<<20)).Decode(&es); err != nil {
		return nil, fmt.Errorf("couldn't read the Switch's game list: %w", err)
	}
	st, _ := f.Stat()
	dat := &gamedat.DAT{Header: map[string]string{"name": "Nintendo - Switch"}, Games: make([]gamedat.Entry, 0, len(es))}
	if st != nil {
		dat.Header["version"] = st.ModTime().Format("2006.01.02")
	}
	for _, e := range es {
		name := strings.Join(strings.Fields(cleanTM.Replace(e.Name)), " ")
		if e.IsDemo {
			name += " (Demo)"
		}
		fields := map[string]string{"name": name, "serial": e.ID, "publisher": e.Publisher, "developer": e.Developer,
			"art": cmp.Or(e.IconURL, e.BoxArt)}
		if e.ReleaseDate > 19000000 {
			fields["releaseyear"] = strconv.Itoa(e.ReleaseDate / 10000)
		}
		if e.Players > 0 {
			fields["users"] = strconv.Itoa(e.Players)
		}
		if len(e.Category) > 0 {
			fields["genre"] = e.Category[0]
		}
		if e.Size > 0 {
			fields["size"] = strconv.FormatInt(e.Size, 10)
		}
		dat.Games = append(dat.Games, gamedat.Entry{Fields: fields})
	}
	return dat, nil
}
