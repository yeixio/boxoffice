package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestCode(t *testing.T) {
	for in, want := range map[string]string{"en": "en", "eng": "en", "English": "en", "pt-BR": "pt", "ger": "de", "deu": "de",
		"nor": "nb", "en-GB": "en", "und": "", "": "", "klingon": ""} {
		if got := Code(in); got != want {
			t.Errorf("Code(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnDisk(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Film (2001).mkv")
	for _, n := range []string{"Film (2001).mkv", "Film (2001).en.srt", "Film (2001).spa.forced.srt", "Film (2001).French.sdh.ass",
		"Film (2001).srt", "Film (2001) 2.de.srt", "Film (2001).hi.srt", "Film (2001).it.hi.srt", "Other.nl.srt"} {
		os.WriteFile(filepath.Join(dir, n), []byte(srt), 0o644)
	}
	got, err := OnDisk(video)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"en": true, "fr": true, "hi": true, "it": true}
	if len(got) != len(want) {
		t.Fatalf("OnDisk = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("OnDisk = %v, want %v", got, want)
		}
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Film.mkv")
	// Polish in Windows-1250, as older subtitles are.
	pl, _ := charmap.Windows1250.NewEncoder().String("1\r\n00:00:01,000 --> 00:00:02,000\r\nZażółć gęślą jaźń\r\n")
	path, err := Save(video, "pl", true, "x.srt", []byte(pl))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "Film.pl.sdh.srt") {
		t.Fatalf("saved at %s", path)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "Zażółć gęślą jaźń\n") || strings.Contains(string(b), "\r") {
		t.Fatalf("saved %q", b)
	}
	if _, err := Save(video, "pl", true, "x.srt", []byte(srt)); err == nil {
		t.Fatal("replaced a file that was there")
	}
	if _, err := Save(video, "en", false, "x.srt", []byte("<html>nope</html>")); err == nil {
		t.Fatal("saved a page as a subtitle")
	}
	// UTF-16 with a byte order mark.
	u16 := []byte{0xFF, 0xFE}
	for _, r := range srt {
		u16 = append(u16, byte(r), 0)
	}
	if _, err := Save(video, "en", false, "x.srt", u16); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Film.en.srt")); string(b) != srt {
		t.Fatalf("UTF-16 saved as %q", b)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".boxoffice-subtitle-") {
			t.Fatal("left a temporary file")
		}
	}
}

func TestRank(t *testing.T) {
	found := []Found{
		{ID: "popular", Language: "en", Release: "Film.2001.1080p.WEB-DL-OTHER", Downloads: 900},
		{ID: "group", Language: "en", Release: "Film.2001.720p.BluRay.x264-GRP", Downloads: 10},
		{ID: "forced", Language: "en", Forced: true, MatchesFile: true},
		{ID: "french", Language: "fr", MatchesFile: true},
		{ID: "sdh", Language: "en", SDH: true, Release: "Film.2001.720p.BluRay.x264-GRP", Downloads: 10},
	}
	ids := func(list []Found) string {
		var out []string
		for _, f := range list {
			out = append(out, f.ID)
		}
		return strings.Join(out, " ")
	}
	if got := ids(Rank(found, "en", "Film.2001.1080p.BluRay.x264-GRP", false)); got != "group sdh popular" {
		t.Fatalf("Rank = %s", got)
	}
	if got := ids(Rank(found, "en", "Film.2001.1080p.BluRay.x264-GRP", true)); got != "sdh group popular" {
		t.Fatalf("Rank preferring SDH = %s", got)
	}
}

func TestParseInside(t *testing.T) {
	out := []byte(`{"streams":[{"index":2,"tags":{"language":"eng"},"disposition":{"forced":0}},
		{"index":3,"tags":{"language":"spa"},"disposition":{"forced":1}},
		{"index":4,"tags":{"language":"fre","title":"Forced"},"disposition":{"forced":0}},
		{"index":5,"tags":{"LANGUAGE":"ger"}},{"index":6,"tags":{"language":"und"}}]}`)
	got, err := ParseInside(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "en,de" {
		t.Fatalf("ParseInside = %v", got)
	}
}

func zipped(t *testing.T, name, body string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("readme.txt")
	io.WriteString(w, "made with love")
	w, _ = zw.Create(name)
	io.WriteString(w, body)
	zw.Close()
	return buf.Bytes()
}

func TestPodnapisi(t *testing.T) {
	var asked []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/subtitles/search/old":
			asked = append(asked, r.URL.RawQuery)
			w.Write([]byte(`<?xml version="1.0"?><results><pagination><current>1</current><count>1</count></pagination>
<subtitle><pid>aB1</pid><title>Paper Comets</title><year>2001</year><tvSeason>0</tvSeason><tvEpisode>0</tvEpisode><language>en</language><flags>n</flags><release>Paper.Comets.2001.1080p.BluRay-GRP Paper.Comets.2001.720p.BluRay-GRP</release><downloads>40</downloads></subtitle>
<subtitle><pid>cD2</pid><title>Paper Comets Two</title><year>2001</year><tvSeason>0</tvSeason><tvEpisode>0</tvEpisode><language>en</language><flags></flags><release></release></subtitle>
<subtitle><pid>eF3</pid><title>Paper Comets</title><year>1970</year><tvSeason>0</tvSeason><tvEpisode>0</tvEpisode><language>en</language><flags></flags><release></release></subtitle>
</results>`))
		case r.URL.Path == "/subtitles/aB1/download" && r.URL.Query().Get("container") == "zip":
			w.Write(zipped(t, "Paper.Comets.srt", srt))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	p := &Podnapisi{Base: ts.URL}
	found, err := p.Search(context.Background(), Query{Movie: true, Title: "Paper Comets", Year: 2001, Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "aB1" || !found[0].SDH || found[0].Downloads != 40 || !strings.Contains(found[0].Release, "720p") {
		t.Fatalf("found %+v", found)
	}
	if !strings.Contains(asked[0], "sK=Paper+Comets") || !strings.Contains(asked[0], "sY=2001") || !strings.Contains(asked[0], "sL=en") {
		t.Fatalf("asked %s", asked[0])
	}
	data, name, err := p.Download(context.Background(), found[0])
	if err != nil || string(data) != srt || name != "Paper.Comets.srt" {
		t.Fatalf("download: %q %q %v", data, name, err)
	}
}

func TestOpenSubtitles(t *testing.T) {
	var search string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "key" && r.URL.Path != "/file/7" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"token":"tok"}`))
		case "/subtitles":
			search = r.URL.RawQuery
			w.Write([]byte(`{"data":[
				{"attributes":{"language":"pt-BR","download_count":5,"hearing_impaired":true,"moviehash_match":true,"release":"Show.S01E02.WEB-GRP","files":[{"file_id":7,"file_name":"a.srt"}]}},
				{"attributes":{"language":"en","ai_translated":"1","files":[{"file_id":8}]}},
				{"attributes":{"language":"en","files":[{"file_id":9},{"file_id":10}]}}]}`))
		case "/download":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["file_id"] != float64(7) {
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			w.Write([]byte(`{"link":"` + ts.URL + `/file/7","remaining":19}`))
		case "/file/7":
			w.Write([]byte(srt))
		}
	}))
	defer ts.Close()
	o := &OpenSubtitles{APIKey: "key", Username: "me", Password: "pw", Base: ts.URL}
	found, err := o.Search(context.Background(), Query{TMDBID: 55, Season: 1, Episode: 2, Hash: "abc", Languages: []string{"pt", "en"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search, "parent_tmdb_id=55") || !strings.Contains(search, "languages=en%2Cpt-br%2Cpt-pt") || !strings.Contains(search, "moviehash=abc") {
		t.Fatalf("searched %s", search)
	}
	if len(found) != 2 || found[0].Language != "pt" || !found[0].MatchesFile || !found[0].SDH || !found[1].Machine {
		t.Fatalf("found %+v", found)
	}
	data, _, err := o.Download(context.Background(), found[0])
	if err != nil || string(data) != srt {
		t.Fatalf("download: %q %v", data, err)
	}
	if _, _, err := o.Download(context.Background(), found[1]); err != ErrQuota {
		t.Fatalf("used up: %v", err)
	}
	if err := (&OpenSubtitles{APIKey: "nope", Base: ts.URL}).Check(context.Background()); err != ErrBadKey {
		t.Fatalf("bad key: %v", err)
	}
}

func TestInsideWithFFprobe(t *testing.T) {
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "in.srt")
	os.WriteFile(sub, []byte(srt), 0o644)
	video := filepath.Join(dir, "Film.mkv")
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=160x90:rate=5", "-i", sub, "-i", sub,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "mpeg4", "-c:s", "srt",
		"-metadata:s:s:0", "language=swe", "-metadata:s:s:1", "language=eng", "-disposition:s:1", "forced", video).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	got, err := Inside(context.Background(), fp, video)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "sv" {
		t.Fatalf("Inside = %v, want the Swedish and not the forced English", got)
	}
}
