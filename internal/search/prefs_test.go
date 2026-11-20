package search

import (
	"context"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/quality"
)

// A profile's release preferences order releases of the same quality:
// preferred words first, avoided ones last, and a never-take word turns a
// release down. Quality still comes before any of it.
func TestMovieReleasePreferences(t *testing.T) {
	srv, _ := indexer(t,
		item("The.Matrix.1999.1080p.BluRay.x264-GRP", "plain", 8e9),
		item("The.Matrix.1999.1080p.BluRay.x264-YIFY", "avoided", 8e9),
		item("The.Matrix.1999.1080p.BluRay.x265-NTb", "two", 8e9),
		item("The.Matrix.1999.1080p.BluRay.x265-GRP", "one", 8e9),
		item("The.Matrix.1999.1080p.BluRay.KORSUB.x265-NTb", "never", 8e9),
		item("The.Matrix.1999.1080p.WEB-DL.x265-NTb", "lower", 4e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	order := func() string {
		t.Helper()
		res, err := s.Movie(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, c := range res.Releases {
			if c.Accepted {
				ids = append(ids, c.GUID)
			}
		}
		return strings.Join(ids, " ")
	}
	p, _ := st.Profile(ctx, m.ProfileID)
	p.Prefs = quality.Prefs{Presets: []string{"poor"}, Prefer: []string{"x265", "NTb"}, Never: []string{"korsub"}}
	if err := st.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Profile(ctx, p.ID); strings.Join(got.Prefs.Prefer, ",") != "x265,NTb" || len(got.Prefs.Presets) != 1 {
		t.Fatalf("kept %+v", got.Prefs)
	}
	if got := order(); got != "two one plain avoided lower" {
		t.Fatalf("order: %s", got)
	}
	res, _ := s.Movie(ctx, m.ID)
	for _, c := range res.Releases {
		switch c.GUID {
		case "two":
			if strings.Join(c.Preferred, ",") != "x265,NTb" || len(c.Avoided) != 0 {
				t.Errorf("two: %v %v", c.Preferred, c.Avoided)
			}
		case "avoided":
			if strings.Join(c.Avoided, ",") != "YIFY" {
				t.Errorf("avoided: %v", c.Avoided)
			}
		case "never":
			if c.Accepted || !strings.Contains(strings.Join(c.Rejections, "; "), `its name has "korsub", which the `+p.Name+` profile never takes`) {
				t.Errorf("never: %v %v", c.Accepted, c.Rejections)
			}
		}
	}
	// A search takes the best; the avoided one only when it's all there is.
	if r, err := s.pick(ctx, res, "movie", m.ID, 0); err != nil || !strings.Contains(r.Message, "x265-NTb") {
		t.Fatalf("picked: %+v %v", r, err)
	}
}
