// Package nzb reads NZB files, which list the Usenet articles that make up
// a release.
package nzb

import (
	"encoding/xml"
	"errors"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// NZB is a release: its files, each made of segments (articles).
type NZB struct {
	Meta  map[string]string // title, password, category…
	Files []File
}

// File is one file of a release.
type File struct {
	Poster   string
	Date     int64
	Subject  string
	Groups   []string
	Segments []Segment
}

// Segment is one article.
type Segment struct {
	Number    int
	Bytes     int64 // the encoded size the poster gave
	MessageID string
}

type xmlNZB struct {
	Head struct {
		Meta []struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"head"`
	Files []struct {
		Poster   string   `xml:"poster,attr"`
		Date     int64    `xml:"date,attr"`
		Subject  string   `xml:"subject,attr"`
		Groups   []string `xml:"groups>group"`
		Segments []struct {
			Bytes  int64  `xml:"bytes,attr"`
			Number int    `xml:"number,attr"`
			ID     string `xml:",chardata"`
		} `xml:"segments>segment"`
	} `xml:"file"`
}

// Parse reads an NZB.
func Parse(r io.Reader) (*NZB, error) {
	var x xmlNZB
	dec := xml.NewDecoder(io.LimitReader(r, 64<<20))
	// Some NZBs say ISO-8859-1; the parts that matter are ASCII.
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	dec.Strict = false
	if err := dec.Decode(&x); err != nil {
		return nil, errors.New("that isn't an NZB file: " + err.Error())
	}
	n := &NZB{Meta: map[string]string{}}
	for _, m := range x.Head.Meta {
		n.Meta[strings.ToLower(m.Type)] = strings.TrimSpace(m.Value)
	}
	for _, f := range x.Files {
		file := File{Poster: f.Poster, Date: f.Date, Subject: f.Subject, Groups: f.Groups}
		seen := map[int]bool{}
		for _, s := range f.Segments {
			id := strings.TrimSpace(s.ID)
			id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
			if id == "" || seen[s.Number] {
				continue
			}
			seen[s.Number] = true
			file.Segments = append(file.Segments, Segment{Number: s.Number, Bytes: s.Bytes, MessageID: id})
		}
		if len(file.Segments) == 0 {
			continue
		}
		slices.SortFunc(file.Segments, func(a, b Segment) int { return a.Number - b.Number })
		n.Files = append(n.Files, file)
	}
	if len(n.Files) == 0 {
		return nil, errors.New("the NZB lists no files")
	}
	return n, nil
}

// Bytes is the release's size as posted (encoded, so a little more than
// the files will be).
func (n *NZB) Bytes() int64 {
	var t int64
	for _, f := range n.Files {
		t += f.Bytes()
	}
	return t
}

func (f File) Bytes() int64 {
	var t int64
	for _, s := range f.Segments {
		t += s.Bytes
	}
	return t
}

var (
	reQuoted = regexp.MustCompile(`"([^"]+)"`)
	reParts  = regexp.MustCompile(`\s*[\[(]?\d+/\d+[\])]?\s*$`)
	reYEnc   = regexp.MustCompile(`(?i)\s*yenc\s*`)
)

// Name guesses a file's name from its subject: the quoted part, as
// posters write it, or failing that the subject without its part count.
// The yEnc header has the real name; this is for before an article is in.
func (f File) Name() string {
	if m := reQuoted.FindStringSubmatch(f.Subject); m != nil {
		return path.Base(strings.TrimSpace(m[1]))
	}
	s := reYEnc.ReplaceAllString(f.Subject, " ")
	s = reParts.ReplaceAllString(s, "")
	s = reParts.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var reVol = regexp.MustCompile(`(?i)\.vol(\d+)\+(\d+)\.par2$`)

// IsPar2 says whether a file is a par2 file; blocks is how many recovery
// blocks a volume file holds (0 for the index file).
func IsPar2(name string) (ok bool, blocks int) {
	if m := reVol.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		return true, n
	}
	return strings.HasSuffix(strings.ToLower(name), ".par2"), 0
}
