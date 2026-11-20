package quality

import (
	"strings"
	"testing"
)

func TestPrefsJudge(t *testing.T) {
	list := func(m PrefMatch) string {
		return strings.Join(m.Preferred, ",") + "|" + strings.Join(m.Avoided, ",") + "|" + m.Never
	}
	own := Prefs{Prefer: []string{"NTb", "x265", "DTS-HD"}, Avoid: []string{"upscaled"}, Never: []string{"KORSUB", "dubbed"}}
	for _, c := range []struct {
		prefs Prefs
		name  string
		want  string
	}{
		{own, "Show.S01E02.1080p.WEB-DL.x265-NTb", "NTb,x265||"},
		{own, "Show S01E02 1080p WEB-DL DTS-HD MA 5.1 H.264-GRP", "DTS-HD||"},
		{own, "Movie.2019.2160p.UpScaled.BluRay.x265-GRP", "x265|upscaled|"},
		{own, "Movie.2019.1080p.HDRip.KORSUB.x264-GRP", "||KORSUB"},
		// Whole words only, and not in the title itself.
		{own, "Movie.2019.1080p.WEB-DL.x2650-NTbX", "||"},
		{own, "Dubbed.2019.1080p.WEB-DL.H.264-GRP", "||"},
		{own, "Upscaled.S01E01.Dubbed.1080p.WEB-DL-GRP", "||dubbed"},
		// A name with no year or episode is taken whole.
		{own, "Some.Thing.x265-NTb", "NTb,x265||"},
		// The year in a title isn't where the title ends.
		{Prefs{Prefer: []string{"Remux"}}, "2001.A.Space.Odyssey.1968.Remux.2160p-GRP", "Remux||"},
		{Prefs{Prefer: []string{"Odyssey"}}, "2001.A.Space.Odyssey.1968.Remux.2160p-GRP", "||"},
		// Presets.
		{Prefs{Presets: []string{"best"}}, "Movie.2019.2160p.UHD.BluRay.Remux.DV.HDR10+.TrueHD.Atmos-GRP", "Remux,DV,HDR10+,Atmos,TrueHD||"},
		{Prefs{Presets: []string{"best"}}, "Movie.2019.2160p.WEB-DL.DDP5.1.HDR.H.265-GRP", "HDR||"},
		{Prefs{Presets: []string{"small"}}, "Movie.2019.1080p.BluRay.Remux.AVC-GRP", "|Remux|"},
		{Prefs{Presets: []string{"small"}}, "Movie.2019.1080p.WEB-DL.H.265-GRP", "H.265||"},
		{Prefs{Presets: []string{"poor"}}, "Movie.2019.1080p.BluRay.x264-YIFY", "|YIFY|"},
		{Prefs{Presets: []string{"poor"}}, "Movie.2019.1080p.BluRay.x264.YTS.MX", "|YTS|"},
		{Prefs{Presets: []string{"poor"}}, "Movie.2019.1080p.3D.HSBS.BluRay.x264-GRP", "|3D,HSBS|"},
		// The profile's own lists win over a preset's.
		{Prefs{Presets: []string{"small"}, Prefer: []string{"remux"}}, "Movie.2019.1080p.BluRay.Remux.HEVC-GRP", "remux,HEVC||"},
		{Prefs{Presets: []string{"best"}, Avoid: []string{"atmos"}}, "Movie.2019.2160p.Remux.Atmos-GRP", "Remux|atmos|"},
		// Two presets that disagree: avoided wins.
		{Prefs{Presets: []string{"best", "small"}}, "Movie.2019.2160p.Remux.HEVC.Atmos-GRP", "Atmos,HEVC|Remux|"},
		{Prefs{}, "Movie.2019.2160p.Remux.HEVC.Atmos-GRP", "||"},
	} {
		if got := list(c.prefs.Judge(c.name)); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// Any avoided word puts a release below one with none.
	a, b := PrefMatch{Preferred: []string{"x", "y", "z"}, Avoided: []string{"u"}}, PrefMatch{}
	if a.Score() >= b.Score() || (PrefMatch{Preferred: []string{"x"}}).Score() <= b.Score() {
		t.Fatalf("scores: %d %d", a.Score(), b.Score())
	}
}

func TestPrefsCheckAndClean(t *testing.T) {
	p := Profile{Name: "HD", Allowed: []int{9}, Cutoff: 9}
	for _, bad := range []Prefs{
		{Presets: []string{"nope"}},
		{Avoid: []string{"--"}},
		{Prefer: []string{strings.Repeat("a", 61)}},
		{Never: make([]string, 101)},
	} {
		p.Prefs = bad
		if p.Check() == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
	p.Prefs = Prefs{Presets: []string{"best", "best"}, Prefer: []string{" NTb ", "ntb", "", "x265"}}.Clean()
	if err := p.Check(); err != nil || len(p.Prefs.Presets) != 1 || strings.Join(p.Prefs.Prefer, ",") != "NTb,x265" {
		t.Fatalf("%+v %v", p.Prefs, err)
	}
}
