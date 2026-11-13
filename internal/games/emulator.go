package games

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yeixio/boxoffice/internal/gamedat"
)

// Emulator is an emulator's install found in a folder being imported, such
// as an old MAME or AAE folder with its roms folder inside. The games in
// it are that emulator's, whatever their files look like.
type Emulator struct {
	// Dir is the install's folder.
	Dir string
	// System is the system its games go under: mame or aae.
	System string
	// Name is the emulator's name, and Version its version when it says
	// (0.154), or "".
	Name    string
	Version string
	// program is the folder the emulator's program is in.
	program string
}

// Label is the emulator's name with its version.
func (e Emulator) Label() string {
	if e.Version == "" {
		return e.Name
	}
	return e.Name + " " + e.Version
}

var (
	reMAMEExe     = regexp.MustCompile(`^(?:mame|mame64|mameui|mameui64|mame32)(?:0?(\d{3})[a-z]?)?(?:\.exe)?$`)
	reWhatsNewVer = regexp.MustCompile(`^0\.(\d{2,3})\b`)
)

// emulatorOf says which emulator a program in an install's folder is.
func emulatorOf(name string) (system, label string, version string, ok bool) {
	n := strings.ToLower(name)
	switch n {
	case "aae.exe", "aae":
		return "aae", "AAE", "", true
	}
	if m := reMAMEExe.FindStringSubmatch(n); m != nil {
		if m[1] != "" {
			version = "0." + strings.TrimLeft(m[1], "0")
		}
		return "mame", "MAME", version, true
	}
	return "", "", "", false
}

// FindEmulators finds the emulator installs in root or in the folders in
// it, three deep at most.
func FindEmulators(root string) []Emulator {
	var out []Emulator
	seen := map[string]int{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || emulatorFolder(d.Name()) || depth >= 3) {
				return filepath.SkipDir
			}
			return nil
		}
		system, name, version, ok := emulatorOf(d.Name())
		if !ok {
			return nil
		}
		dir := installDir(root, filepath.Dir(p))
		if i, ok := seen[dir]; ok {
			if out[i].Version == "" {
				out[i].Version = version
			}
			return nil
		}
		seen[dir] = len(out)
		out = append(out, Emulator{Dir: dir, System: system, Name: name, Version: version, program: filepath.Dir(p)})
		return nil
	})
	for i, e := range out {
		if e.System == "mame" && e.Version == "" {
			out[i].Version = whatsNewVersion(e.program)
		}
	}
	return out
}

// installDir is the folder an emulator's games are in: the program's own
// folder, or the one above it when that's where the roms folder is (AAE
// comes as a roms folder beside a folder with the program in).
func installDir(root, dir string) string {
	if hasRoms(dir) || dir == root {
		return dir
	}
	if up := filepath.Dir(dir); strings.HasPrefix(up+string(filepath.Separator), root+string(filepath.Separator)) && hasRoms(up) {
		return up
	}
	return dir
}

func hasRoms(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "roms"))
	return err == nil && st.IsDir()
}

// whatsNewVersion reads MAME's version from the whatsnew.txt it comes
// with, whose first line is the version.
func whatsNewVersion(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "[wW][hH][aA][tT][sS][nN][eE][wW]*.txt"))
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			continue
		}
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		f.Close()
		if v := reWhatsNewVer.FindStringSubmatch(strings.TrimSpace(string(buf[:n]))); v != nil {
			return "0." + v[1]
		}
	}
	return ""
}

// emulatorFor is the emulator whose install p is in, if any.
func emulatorFor(emus []Emulator, p string) (Emulator, bool) {
	best := -1
	for i, e := range emus {
		if strings.HasPrefix(p, e.Dir+string(filepath.Separator)) && (best < 0 || len(e.Dir) > len(emus[best].Dir)) {
			best = i
		}
	}
	if best < 0 {
		return Emulator{}, false
	}
	return emus[best], true
}

// emulatorGame says whether a file in an emulator's install is one of its
// games: an archive or a CHD, not one of its programs or notes.
func emulatorGame(name string) bool {
	return gamedat.IsArchive(name) || strings.EqualFold(filepath.Ext(name), ".chd")
}
