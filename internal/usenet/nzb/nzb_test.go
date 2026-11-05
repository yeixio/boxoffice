package nzb

import (
	"strings"
	"testing"
)

const sample = `<?xml version="1.0" encoding="iso-8859-1" ?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
 <head><meta type="title">My Movie</meta><meta type="password">secret</meta></head>
 <file poster="a@b" date="1071674882" subject="My.Movie.2020 [01/02] - &quot;my.movie.part01.rar&quot; yEnc (1/2)">
  <groups><group>alt.binaries.test</group></groups>
  <segments>
   <segment bytes="102394" number="2">part2of2@example</segment>
   <segment bytes="102394" number="1">&lt;part1of2@example&gt;</segment>
   <segment bytes="102394" number="1">dup@example</segment>
  </segments>
 </file>
 <file poster="a@b" date="1071674882" subject="[02/02] - &quot;my.movie.vol00+01.par2&quot; yEnc (1/1)">
  <groups><group>alt.binaries.test</group></groups>
  <segments><segment bytes="5000" number="1">par@example</segment></segments>
 </file>
</nzb>`

func TestParse(t *testing.T) {
	n, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if n.Meta["title"] != "My Movie" || n.Meta["password"] != "secret" {
		t.Fatalf("meta %v", n.Meta)
	}
	if len(n.Files) != 2 || len(n.Files[0].Segments) != 2 {
		t.Fatalf("files %+v", n.Files)
	}
	if s := n.Files[0].Segments[0]; s.Number != 1 || s.MessageID != "part1of2@example" {
		t.Fatalf("first segment %+v", s)
	}
	if got := n.Files[0].Name(); got != "my.movie.part01.rar" {
		t.Fatalf("name %q", got)
	}
	if n.Bytes() != 102394*2+5000 {
		t.Fatalf("bytes %d", n.Bytes())
	}
	if ok, b := IsPar2(n.Files[1].Name()); !ok || b != 1 {
		t.Fatal("par2 volume not seen")
	}
	if _, err := Parse(strings.NewReader("<html></html>")); err == nil {
		t.Fatal("parsed html")
	}
}

func TestNameWithoutQuotes(t *testing.T) {
	f := File{Subject: "abc123def.bin yEnc (1/50)"}
	if got := f.Name(); got != "abc123def.bin" {
		t.Fatalf("got %q", got)
	}
}
