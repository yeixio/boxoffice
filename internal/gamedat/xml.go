// Package gamedat reads game lists (MAME's own -listxml, and the DAT files
// No-Intro and libretro publish) and checks the game files on disk against
// them: which game a file is, and whether a MAME set is complete.
//
// It only reads. Nothing here writes, moves or renames a game file.
package gamedat

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// Machine is one set in MAME's list, or one game in a Logiqx XML DAT.
type Machine struct {
	Name         string
	Description  string
	Year         string
	Manufacturer string
	CloneOf      string
	RomOf        string
	IsBIOS       bool
	IsDevice     bool
	IsMechanical bool
	// Runnable is false for a set MAME can't start on its own.
	Runnable bool
	Players  int
	// DefaultBIOS is the BIOS set a BIOS machine uses unless told
	// otherwise.
	DefaultBIOS string
	// Roms and Disks are filled in only for the sets asked for; HasRoms
	// says whether a set has any files at all, asked for or not.
	Roms    []Rom
	Disks   []Disk
	Devices []string
	HasRoms bool
}

// Rom is one file of a set.
type Rom struct {
	Name string
	Size int64
	CRC  uint32
	// HasCRC is false for a file nobody has dumped.
	HasCRC bool
	// Merge names the file in the parent (or BIOS) set this one is.
	Merge string
	// BIOS names the BIOS option the file belongs to, if it's one.
	BIOS     string
	NoDump   bool
	Optional bool
}

// Disk is a CHD a set needs.
type Disk struct {
	Name     string
	SHA1     string
	Merge    string
	NoDump   bool
	Optional bool
}

// List is a parsed MAME list or Logiqx DAT.
type List struct {
	// Version is MAME's version, such as "0.289", or the DAT's.
	Version  string
	Machines map[string]*Machine
}

// ParseXML reads MAME's -listxml or a Logiqx XML DAT. Files are kept only
// for the sets want says yes to (all of them when want is nil), since
// MAME's whole list has hundreds of thousands.
func ParseXML(r io.Reader, want func(name string) bool) (*List, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	l := &List{Machines: map[string]*Machine{}}
	var m *Machine
	var text *string
	var keep bool
	var inHeader bool
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "mame":
				if b := attr(t, "build"); b != "" {
					l.Version, _, _ = strings.Cut(b, " ")
				}
			case "header":
				inHeader = true
			case "version":
				if inHeader && l.Version == "" {
					text = &l.Version
				}
			case "machine", "game":
				m = &Machine{
					Name:         attr(t, "name"),
					CloneOf:      attr(t, "cloneof"),
					RomOf:        attr(t, "romof"),
					IsBIOS:       attr(t, "isbios") == "yes",
					IsDevice:     attr(t, "isdevice") == "yes",
					IsMechanical: attr(t, "ismechanical") == "yes",
					Runnable:     attr(t, "runnable") != "no",
				}
				keep = want == nil || want(m.Name)
			case "description":
				if m != nil {
					text = &m.Description
				}
			case "year":
				if m != nil {
					text = &m.Year
				}
			case "manufacturer":
				if m != nil {
					text = &m.Manufacturer
				}
			case "biosset":
				// MAME starts with the one marked default, else the first.
				if m != nil && (m.DefaultBIOS == "" || attr(t, "default") == "yes") {
					m.DefaultBIOS = attr(t, "name")
				}
			case "rom":
				if m == nil {
					break
				}
				m.HasRoms = true
				if !keep {
					break
				}
				rom := Rom{Name: attr(t, "name"), Merge: attr(t, "merge"), BIOS: attr(t, "bios"),
					NoDump: attr(t, "status") == "nodump", Optional: attr(t, "optional") == "yes"}
				rom.Size, _ = strconv.ParseInt(attr(t, "size"), 10, 64)
				if c, err := strconv.ParseUint(attr(t, "crc"), 16, 32); err == nil {
					rom.CRC, rom.HasCRC = uint32(c), true
				}
				m.Roms = append(m.Roms, rom)
			case "disk":
				if m == nil {
					break
				}
				m.HasRoms = true
				if keep {
					m.Disks = append(m.Disks, Disk{Name: attr(t, "name"), SHA1: strings.ToLower(attr(t, "sha1")), Merge: attr(t, "merge"),
						NoDump: attr(t, "status") == "nodump", Optional: attr(t, "optional") == "yes"})
				}
			case "device_ref":
				if m != nil {
					if n := attr(t, "name"); n != "" && n != m.Name {
						m.Devices = append(m.Devices, n)
					}
				}
			case "input":
				if m != nil {
					m.Players, _ = strconv.Atoi(attr(t, "players"))
				}
			}
		case xml.CharData:
			if text != nil {
				*text += string(t)
			}
		case xml.EndElement:
			text = nil
			switch t.Name.Local {
			case "header":
				inHeader = false
			case "machine", "game":
				if m != nil && m.Name != "" {
					m.Description = strings.TrimSpace(m.Description)
					m.Manufacturer = strings.TrimSpace(m.Manufacturer)
					m.Year = strings.TrimSpace(m.Year)
					l.Machines[m.Name] = m
				}
				m = nil
			}
		}
	}
	l.Version = strings.TrimSpace(l.Version)
	return l, nil
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
