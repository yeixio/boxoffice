package ereader

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"fmt"
	"time"
)

// TestTitle is the title of the test book.
const TestTitle = "Box Office test"

// TestBook is a one-page EPUB that Kindle, PocketBook and other readers
// all open: what a test sends, so the admin sees it arrive.
func TestBook(now time.Time) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	// The EPUB rules: "mimetype" first, and stored, not compressed.
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/epub+zip"))
	id := make([]byte, 16)
	rand.Read(id)
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	files := []struct{ name, body string }{
		{"META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`},
		{"OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="id">urn:uuid:` + uuid + `</dc:identifier>
    <dc:title>` + TestTitle + `</dc:title>
    <dc:creator>Box Office</dc:creator>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">` + now.UTC().Format("2006-01-02T15:04:05Z") + `</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="page" href="page.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="page"/></spine>
</package>
`},
		{"OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en">
<head><title>` + TestTitle + `</title></head>
<body><nav epub:type="toc"><ol><li><a href="page.xhtml">` + TestTitle + `</a></li></ol></nav></body>
</html>
`},
		{"OEBPS/page.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en">
<head><title>` + TestTitle + `</title></head>
<body>
<h1>It works</h1>
<p>Box Office can send books to this e-reader. Ebooks you send from Box Office, or that it downloads for you, will show up here like this one.</p>
<p>Sent ` + now.UTC().Format("2 January 2006, 15:04 MST") + `.</p>
</body>
</html>
`},
	}
	for _, f := range files {
		w, _ := z.Create(f.name)
		w.Write([]byte(f.body))
	}
	z.Close()
	return b.Bytes()
}
