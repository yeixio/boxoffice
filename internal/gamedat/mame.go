package gamedat

import (
	"io"
	"maps"
	"slices"
	"sort"
)

// Set is a MAME set on disk: the archive named for it, and the CHDs in the
// folder named for it.
type Set struct {
	Name  string
	Path  string
	Size  int64
	Files []File
	// CHDs are the set's CHD files by disk name (without .chd).
	CHDs map[string]string
	// Err says why the archive couldn't be read.
	Err error
}

// Problem is one thing wrong with a set.
type Problem struct {
	// Kind is one of the Problem kinds below.
	Kind string `json:"kind"`
	// Name is the file, disk, or set it's about.
	Name string `json:"name"`
}

// Problem kinds.
const (
	// A file the set needs isn't in it.
	ProblemMissing = "missing"
	// A file of the set has the right name but different contents: it's
	// from another version of MAME, or damaged.
	ProblemWrong = "wrong"
	// The set needs a BIOS set that isn't there.
	ProblemBIOS = "bios"
	// The set needs a device's set (such as qsound) that isn't there.
	ProblemDevice = "device"
	// A clone needs files from its parent set, which isn't there.
	ProblemParent = "parent"
	// The set needs a CHD that isn't there, or one that's different.
	ProblemDisk      = "disk"
	ProblemDiskWrong = "diskWrong"
	// The archive can't be opened.
	ProblemUnreadable = "unreadable"
)

// Statuses of a game.
const (
	Complete = "complete"
	// Missing files, a missing parent, or missing CHDs.
	Missing = "missing"
	// Files with the wrong contents: another MAME version's.
	Wrong = "wrong"
	// Everything but a BIOS or device set.
	NeedsBIOS = "bios"
	// Not in the list at all, such as a set renamed since.
	Unknown = "unknown"
	// The file can't be read.
	Unreadable = "unreadable"
)

// Set formats, as ROM managers call them.
const (
	NonMerged = "non-merged"
	Split     = "split"
	Merged    = "merged"
)

// Game is a MAME game found on disk and how its set compares with the list.
type Game struct {
	Machine *Machine
	// Set is the archive the game is in: its own, or for a clone in a
	// merged set, its parent's.
	Set      *Set
	InParent bool
	Status   string
	Problems []Problem
}

// Parent is the set the game is grouped under: its parent, or itself.
func (g Game) Parent() string {
	if g.Machine.CloneOf != "" {
		return g.Machine.CloneOf
	}
	return g.Machine.Name
}

// Check is what CheckMAME found.
type Check struct {
	Games []Game
	// Format is how the sets are put together, or "" if there's nothing
	// to tell by.
	Format string
	// Support are the BIOS and device sets on disk, which other games
	// need but aren't games.
	Support []string
}

// LoadMAME reads MAME's list in two passes: the first for every set's
// name and links, the second for the files of only the sets on disk and
// those they lean on. MAME's whole list is too big to keep.
func LoadMAME(open func() (io.ReadCloser, error), onDisk []string) (*List, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	first, err := ParseXML(rc, func(string) bool { return false })
	rc.Close()
	if err != nil {
		return nil, err
	}
	need := Needed(first, onDisk)
	first = nil
	rc, err = open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return ParseXML(rc, func(n string) bool { return need[n] })
}

// Needed lists the sets whose files checking onDisk takes: those sets,
// their parents, BIOS and devices, and the clones of parents on disk,
// which a merged set holds.
func Needed(l *List, onDisk []string) map[string]bool {
	need := map[string]bool{}
	var add func(string)
	add = func(n string) {
		m, ok := l.Machines[n]
		if !ok || need[n] {
			return
		}
		need[n] = true
		add(m.CloneOf)
		add(m.RomOf)
		for _, d := range m.Devices {
			if dm, ok := l.Machines[d]; ok && dm.HasRoms {
				add(d)
			}
		}
	}
	present := map[string]bool{}
	for _, n := range onDisk {
		present[n] = true
		add(n)
	}
	for _, m := range l.Machines {
		if m.CloneOf != "" && present[m.CloneOf] {
			add(m.Name)
		}
	}
	return need
}

// CheckMAME matches the sets on disk to MAME's list and checks each one.
// sha1 reads a CHD's SHA-1; it may be nil, and then CHDs are only looked
// for.
func CheckMAME(l *List, sets map[string]*Set, sha1 func(path string) (string, error)) Check {
	var c Check
	clonesOf := map[string][]*Machine{}
	for _, m := range l.Machines {
		if m.CloneOf != "" {
			clonesOf[m.CloneOf] = append(clonesOf[m.CloneOf], m)
		}
	}
	crcsOf := map[string]map[uint32]bool{}
	crcs := func(s *Set) map[uint32]bool {
		if s == nil {
			return nil
		}
		if c, ok := crcsOf[s.Name]; ok {
			return c
		}
		c := make(map[uint32]bool, len(s.Files))
		for _, f := range s.Files {
			c[f.CRC] = true
		}
		crcsOf[s.Name] = c
		return c
	}
	merged, split, nonMerged := 0, 0, 0
	for _, name := range slices.Sorted(maps.Keys(sets)) {
		s := sets[name]
		m, ok := l.Machines[name]
		if !ok {
			g := Game{Machine: &Machine{Name: name, Description: name}, Set: s, Status: Unknown}
			if s.Err != nil {
				g.Status, g.Problems = Unreadable, []Problem{{ProblemUnreadable, s.Err.Error()}}
			}
			c.Games = append(c.Games, g)
			continue
		}
		if m.IsBIOS || m.IsDevice {
			c.Support = append(c.Support, name)
			continue
		}
		c.Games = append(c.Games, Game{Machine: m, Set: s})
		if m.CloneOf != "" {
			// A clone's own archive holding its parent's files is
			// non-merged; one without them is split.
			shared := false
			have := crcs(s)
			for _, r := range m.Roms {
				if r.Merge != "" && r.HasCRC && have[r.CRC] {
					shared = true
					break
				}
			}
			if shared {
				nonMerged++
			} else {
				split++
			}
			continue
		}
		// A merged parent holds its clones too.
		have := crcs(s)
		for _, cl := range clonesOf[name] {
			if _, own := sets[cl.Name]; own || cl.IsBIOS || cl.IsDevice {
				continue
			}
			for _, r := range cl.Roms {
				if r.Merge == "" && r.HasCRC && !r.NoDump && have[r.CRC] {
					c.Games = append(c.Games, Game{Machine: cl, Set: s, InParent: true})
					merged++
					break
				}
			}
		}
	}
	switch {
	case merged > 0 && merged >= split && merged >= nonMerged:
		c.Format = Merged
	case nonMerged > 0 && nonMerged >= split:
		c.Format = NonMerged
	case split > 0:
		c.Format = Split
	}
	for i := range c.Games {
		g := &c.Games[i]
		if g.Status != "" {
			continue
		}
		g.Status, g.Problems = checkSet(l, sets, g, crcs, sha1)
	}
	sort.SliceStable(c.Games, func(i, j int) bool { return c.Games[i].Machine.Name < c.Games[j].Machine.Name })
	return c
}

// chain is a set's own name, then the sets it takes files from, nearest
// first: its parent, then the BIOS.
func chain(l *List, m *Machine) []string {
	out := []string{m.Name}
	seen := map[string]bool{m.Name: true}
	for n := m.RomOf; n != "" && !seen[n]; {
		out = append(out, n)
		seen[n] = true
		next, ok := l.Machines[n]
		if !ok {
			break
		}
		n = next.RomOf
	}
	if m.CloneOf != "" && !seen[m.CloneOf] {
		out = append(out, m.CloneOf)
	}
	return out
}

func checkSet(l *List, sets map[string]*Set, g *Game, crcs func(*Set) map[uint32]bool, sha1 func(string) (string, error)) (string, []Problem) {
	m := g.Machine
	if g.Set.Err != nil && !g.InParent {
		return Unreadable, []Problem{{ProblemUnreadable, g.Set.Err.Error()}}
	}
	links := chain(l, m)
	have := map[uint32]bool{}
	for _, n := range links {
		maps.Copy(have, crcs(sets[n]))
	}
	maps.Copy(have, crcs(g.Set))
	// The BIOS option the game starts with, from the BIOS set it uses.
	defaultBIOS := ""
	for _, n := range links[1:] {
		if b, ok := l.Machines[n]; ok && b.IsBIOS {
			defaultBIOS = b.DefaultBIOS
		}
	}
	if m.DefaultBIOS != "" {
		defaultBIOS = m.DefaultBIOS
	}
	ownNames := map[string]bool{}
	if g.Set != nil {
		for _, f := range g.Set.Files {
			ownNames[f.Base()] = true
		}
	}
	var problems []Problem
	seen := map[Problem]bool{}
	add := func(p Problem) {
		if !seen[p] {
			seen[p] = true
			problems = append(problems, p)
		}
	}
	for _, r := range m.Roms {
		if r.NoDump || r.Optional || !r.HasCRC || have[r.CRC] {
			continue
		}
		if r.BIOS != "" && defaultBIOS != "" && r.BIOS != defaultBIOS {
			continue
		}
		if ownNames[r.Name] {
			add(Problem{ProblemWrong, r.Name})
			continue
		}
		if r.Merge != "" {
			// It comes from the parent or the BIOS: say which is
			// missing, if one is.
			if from := owner(l, links[1:], r); from != "" {
				if _, ok := sets[from]; !ok {
					if b := l.Machines[from]; b != nil && b.IsBIOS {
						add(Problem{ProblemBIOS, from})
					} else {
						add(Problem{ProblemParent, from})
					}
					continue
				}
			}
		}
		add(Problem{ProblemMissing, r.Name})
	}
	for _, d := range m.Devices {
		dm, ok := l.Machines[d]
		if !ok || !dm.HasRoms || len(dm.Roms) == 0 {
			continue
		}
		own := crcs(sets[d])
		for _, r := range dm.Roms {
			if r.NoDump || r.Optional || !r.HasCRC || have[r.CRC] || own[r.CRC] {
				continue
			}
			add(Problem{ProblemDevice, d})
			break
		}
	}
	for _, disk := range m.Disks {
		if disk.NoDump || disk.Optional {
			continue
		}
		path := ""
		for _, n := range append([]string{m.Name}, links[1:]...) {
			if s, ok := sets[n]; ok && s.CHDs != nil {
				if p, ok := s.CHDs[disk.Name]; ok {
					path = p
					break
				}
				if disk.Merge != "" {
					if p, ok := s.CHDs[disk.Merge]; ok {
						path = p
						break
					}
				}
			}
		}
		if path == "" && g.InParent && g.Set.CHDs != nil {
			path = g.Set.CHDs[disk.Name]
		}
		switch {
		case path == "":
			add(Problem{ProblemDisk, disk.Name})
		case sha1 != nil && disk.SHA1 != "":
			if got, err := sha1(path); err == nil && got != disk.SHA1 {
				add(Problem{ProblemDiskWrong, disk.Name})
			}
		}
	}
	return status(problems), problems
}

// owner finds which set in links a merged file comes from.
func owner(l *List, links []string, r Rom) string {
	for _, n := range links {
		m, ok := l.Machines[n]
		if !ok {
			continue
		}
		for _, pr := range m.Roms {
			if pr.Name == r.Merge && (!pr.HasCRC || pr.CRC == r.CRC) {
				// The parent may take it from the BIOS in turn.
				if pr.Merge != "" {
					if deeper := owner(l, links[slices.Index(links, n)+1:], pr); deeper != "" {
						return deeper
					}
				}
				return n
			}
		}
	}
	if len(links) > 0 {
		return links[len(links)-1]
	}
	return ""
}

func status(problems []Problem) string {
	if len(problems) == 0 {
		return Complete
	}
	onlySupport := true
	for _, p := range problems {
		switch p.Kind {
		case ProblemWrong, ProblemDiskWrong:
			return Wrong
		case ProblemBIOS, ProblemDevice:
		default:
			onlySupport = false
		}
	}
	if onlySupport {
		return NeedsBIOS
	}
	return Missing
}
