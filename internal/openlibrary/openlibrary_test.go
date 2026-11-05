package openlibrary

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthorAndBooks(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "BoxOffice/") {
			t.Errorf("no User-Agent on %s", r.URL)
		}
		switch r.URL.Path {
		case "/authors/OL1394865A.json":
			fmt.Fprint(w, `{"name":"Brandon Sanderson","birth_date":"19 December 1975","bio":{"type":"/type/text","value":"American author."}}`)
		case "/search.json":
			if r.URL.Query().Get("q") != "author_key:OL1394865A" {
				t.Errorf("query %q", r.URL.RawQuery)
			}
			var start int
			fmt.Sscan(r.URL.Query().Get("offset"), &start)
			var docs []string
			for i := start; i < min(start+100, 150); i++ {
				docs = append(docs, fmt.Sprintf(`{"key":"/works/OL%dW","title":"Book %d","first_publish_year":2006,"cover_i":%d,"edition_count":%d}`, i, i, i+1, 200-i))
			}
			fmt.Fprintf(w, `{"numFound":150,"docs":[%s]}`, strings.Join(docs, ","))
		default:
			http.NotFound(w, r)
		}
	}))
	defer hs.Close()
	c := New("test")
	c.Base, c.Spacing = hs.URL, time.Millisecond
	a, err := c.Author(context.Background(), "OL1394865A")
	if err != nil || a.Name != "Brandon Sanderson" || a.Bio != "American author." {
		t.Fatal(a, err)
	}
	if _, err := c.Author(context.Background(), "OL1W"); err != ErrNotFound {
		t.Fatalf("a work key as an author: %v", err)
	}
	bs, err := c.Books(context.Background(), "OL1394865A")
	if err != nil || len(bs) != 150 || bs[0].Key != "OL0W" || bs[0].Editions != 200 || bs[0].Year != 2006 {
		t.Fatalf("%d books, first %+v, %v", len(bs), bs[0], err)
	}
	if CoverURL(bs[1].CoverID) != "https://covers.openlibrary.org/b/id/2-M.jpg" || CoverURL(0) != "" {
		t.Fatal(CoverURL(bs[1].CoverID))
	}
}

func TestBioAsString(t *testing.T) {
	if text([]byte(`"plain"`)) != "plain" || text(nil) != "" {
		t.Fatal("text")
	}
}
