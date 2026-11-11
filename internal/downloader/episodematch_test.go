package downloader

import (
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// pantryPack is made up, but shaped like a real case: a season pack whose
// videos are named with random hashes and carry only the store's synopsis
// in a DESCRIPTION tag. The episode overviews (as TMDB has them) are worded
// differently, some are a few words long, and the pack holds a
// double-length special that isn't one of the season's episodes and
// shares a word ("family") with one that is.
var pantryEpisodes = []struct {
	number          int
	title, overview string
}{
	{1, "Bean Counter", "The host looks at dried beans: soaking, simmering and a smoky pot of chili. Recipes: Black Bean Chili"},
	{2, "Knead to Know", "Tune in as the host shows how to make a great loaf of bread. Recipes: Plain White Loaf"},
	{3, "Family Recipes", "The host cooks the dishes his family grew up on."},
	{4, "Shell Shock", "Just how hard is it to cook a lobster at home?"},
	{5, "Sour Power", "Pickles are easier than you think."},
	{6, "Stir Crazy", "A wok, a hot flame and five minutes: the host takes on stir-fry, from slicing to sauce. Recipes: Ginger Beef, Garlic Greens"},
	{7, "Flat Out", "These days we all want more for less. The host's strategy: pound meats thin and pile on the flavor. Recipes: Chicken Paillard, Veal Piccata"},
	{8, "Whisk Taker", "Meringue, mousse and souffle: the host beats egg whites into shape and explains why the bowl matters."},
	{9, "Cold Comfort", "Ice cream without a machine, sorbet without a fuss, and a granita anyone can make."},
	{10, "Need a Sauce?", "Need a tomato sauce?"},
}

var pantryVideos = []struct {
	file        string
	want        int // 0: none of the season's episodes
	minutes     int
	description string
}{
	{"0c5e2a.mkv", 1, 22, "Dried beans get no respect, so the host soaks, simmers and spices them into a smoky chili worth the wait."},
	{"1d77f0.mkv", 2, 22, "If bread really is the staff of life, isn't it time to take it into your own hands? The host wants you to knead your own rustic loaves."},
	{"2a90c4.mkv", 3, 22, "The host returns to the kitchen where he learned to cook and remakes the recipes his family still argues about."},
	{"3be411.mkv", 4, 22, "Trapped in a seaside shack, the host has nothing to eat but lobster. Let him show you the way to cook one without fear."},
	{"4f0019.mkv", 5, 22, "Brine, vinegar and patience: the host fills a shelf of jars and shows that pickles take less work than you'd think."},
	{"5c3d82.mkv", 6, 22, "The host fires up a wok for a stir-fry lesson, from slicing beef across the grain to a ginger sauce in seconds."},
	{"6e81aa.mkv", 7, 22, "Chef and scientist, the host gives his meat recipes maximum flavor through flatness."},
	{"7a2b6c.mkv", 8, 22, "Egg whites can do more than you'd guess. The host whips up a meringue, a chocolate mousse and a souffle that won't fall."},
	{"8d4e3f.mkv", 9, 22, "No machine? No problem. The host churns out ice cream, a quick sorbet and an icy granita."},
	{"9b6c1e.mkv", 10, 22, "Tomato sauce is the chameleon of the kitchen. The host simplifies dinner with a pantry-friendly tomato sauce."},
	{"af03d2.mkv", 0, 44, "Follow the host on his mission to end the dry turkey curse that has haunted his family for ages, before the relatives arrive."},
}

func pantry() ([]store.Episode, map[string]VideoTags, map[string]int) {
	var eps []store.Episode
	for _, e := range pantryEpisodes {
		eps = append(eps, store.Episode{ID: int64(100 + e.number), Season: 3, Number: e.number, Title: e.title, Overview: e.overview})
	}
	// Another season's episodes, to be passed over.
	eps = append(eps, store.Episode{ID: 201, Season: 4, Number: 1, Title: "Bread Winner", Overview: "The host bakes a loaf of rustic bread in a clay pot."})
	tags, want := map[string]VideoTags{}, map[string]int{}
	for _, v := range pantryVideos {
		tags[v.file] = VideoTags{Description: v.description, Duration: time.Duration(v.minutes) * time.Minute}
		want[v.file] = v.want
	}
	return eps, tags, want
}

func TestMatchByTagsSeasonPack(t *testing.T) {
	eps, tags, want := pantry()
	for _, season := range []int{3, 0} {
		got := matchByTags(tags, eps, season, 22*time.Minute)
		for v := range tags {
			e, ok := got[v]
			switch {
			case want[v] == 0 && ok:
				t.Errorf("season %d, %s: matched to S%02dE%02d, should be left alone", season, v, e.Season, e.Number)
			case want[v] != 0 && !ok && season != 0:
				// With every season to choose from, a video may be too
				// close to call (the bread one, with season 4's bread
				// episode): left alone, never matched wrong.
				t.Errorf("season %d, %s: unmatched, is E%02d", season, v, want[v])
			case ok && (e.Season != 3 || e.Number != want[v]):
				t.Errorf("season %d, %s: matched to S%02dE%02d, is E%02d", season, v, e.Season, e.Number, want[v])
			}
		}
	}
}

// Without its length to go by, the special would be taken for the one
// episode it shares a word with, once that episode's own video is missing.
func TestMatchByTagsLength(t *testing.T) {
	eps, all, _ := pantry()
	tags := map[string]VideoTags{"af03d2.mkv": all["af03d2.mkv"]}
	if got := matchByTags(tags, eps, 3, 22*time.Minute); len(got) != 0 {
		t.Fatalf("special matched: %+v", got)
	}
	tags["af03d2.mkv"] = VideoTags{Description: all["af03d2.mkv"].Description, Duration: 22 * time.Minute}
	if got := matchByTags(tags, eps, 3, 22*time.Minute); len(got) != 1 {
		t.Fatalf("at a usual length it should match: %+v", got)
	}
}

func TestMatchByTagsTold(t *testing.T) {
	eps := []store.Episode{{ID: 1, Season: 1, Number: 1, Title: "Pilot"}, {ID: 2, Season: 1, Number: 2, Title: "The Return"}, {ID: 3, Season: 2, Number: 2, Title: "Pilot"}}
	got := matchByTags(map[string]VideoTags{
		"a.mkv": {Season: 1, Episode: 2},
		"b.mkv": {Title: "Show S02E02 1080p"},
		"c.mkv": {Title: "Pilot"},        // two episodes are called that
		"d.mkv": {Title: "the  return!"}, // E02 is taken already
		"e.mkv": {Description: "nothing at all"},
	}, eps, 0, 0)
	if got["a.mkv"].ID != 2 || got["b.mkv"].ID != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, v := range []string{"c.mkv", "d.mkv", "e.mkv"} {
		if _, ok := got[v]; ok {
			t.Errorf("%s matched to %+v", v, got[v])
		}
	}
	if got := matchByTags(map[string]VideoTags{"x.mkv": {Title: "Pilot"}}, eps, 1, 0); got["x.mkv"].ID != 1 {
		t.Errorf("season-only title: %+v", got)
	}
}

func TestReleaseSeason(t *testing.T) {
	for name, want := range map[string]int{
		"Show.S05.576p.WEB-DL.DDP.2.0.H.264-GROUP": 5,
		"Show.Season.2.1080p":                      2,
		"Show S01E02 720p":                         0,
		"Show.2019.1080p":                          0,
		"show.s10.complete.720p.web-dl":            10,
	} {
		if got := releaseSeason(name); got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
}

func TestParseTags(t *testing.T) {
	got, err := parseTags([]byte(`{"format":{"tags":{"MAJOR_BRAND":"dash","DESCRIPTION":" A synopsis. ","title":"Ep","season_number":"3","episode_sort":"7"}}}`))
	if err != nil || got.Description != "A synopsis." || got.Title != "Ep" || got.Season != 3 || got.Episode != 7 {
		t.Fatalf("%+v %v", got, err)
	}
}
