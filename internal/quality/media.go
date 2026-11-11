package quality

import "slices"

// Media is what a look inside a video file found: what the picture and
// sound really are, whatever its name says.
type Media struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"videoCodec,omitempty"` // h264, hevc, av1, mpeg2video…
	BitDepth   int     `json:"bitDepth,omitempty"`
	HDR        string  `json:"hdr,omitempty"` // HDR10, HDR10+, Dolby Vision, HLG or ""
	AudioCodec string  `json:"audioCodec,omitempty"`
	Channels   int     `json:"channels,omitempty"`
	Bitrate    int64   `json:"bitrate,omitempty"`  // bits a second, the whole file
	Duration   float64 `json:"duration,omitempty"` // seconds
	// Title is the title the file gives itself, which a disc's copy
	// often keeps with its cut in it ("Blade Runner: The Final Cut").
	Title string `json:"title,omitempty"`
	// Error is why the file couldn't be looked inside, when it couldn't.
	Error string `json:"error,omitempty"`
	// Retry says the file couldn't be read for now, through no fault of
	// its own (its share stopped answering), so it's looked inside again
	// later.
	Retry bool `json:"retry,omitempty"`
}

// Resolution is the picture's size as a quality names it (2160, 1080,
// 720 or 480), or 0 when there's no picture. Films are often cropped to
// a wide strip (1920×800), and old ones stretched from narrow pixels
// (1440×1080), so either the width or the height can make the size.
func (m Media) Resolution() int {
	w, h := m.Width, m.Height
	switch {
	case w <= 0 || h <= 0:
		return 0
	case w >= 3200 || h >= 1600:
		return 2160
	case w >= 1700 || h >= 1000:
		return 1080
	case w >= 1200 || h >= 700:
		return 720
	default:
		return 480
	}
}

// Nearest is the listed quality for a source at a resolution: Parse's
// rules, so a file and a release with the same facts get the same
// quality.
func Nearest(source string, res int) Quality {
	if source == "" {
		source = "web"
	}
	if source == "dvd" || source == "remux" && res < 1080 {
		return All[2] // a DVD, or a DVD's own copy
	}
	for _, q := range All {
		if q.Source == source && q.Resolution == res {
			return q
		}
	}
	for _, q := range slices.Backward(All) {
		if q.Resolution == res {
			return q
		}
	}
	return All[0]
}

// Correct says what quality a file really is, given what Box Office has
// recorded for it (current), its name, what a look
// inside found, and its title's profile. It returns current when there's
// nothing to correct.
//
// Only the picture's size is taken as fact: where a copy came from (web,
// Blu-ray…) can't be seen inside the file, so it comes from the record or
// the name. A recorded quality whose size matches is never changed, so
// only a picture that really is smaller can make a copy rank lower. A
// quality is picked from the ones the profile takes, closest to what the
// file is; when where it came from isn't known, the lowest one that meets
// the profile's cutoff, so a guess never sets off an upgrade.
func Correct(current int, name string, m Media, p Profile) int {
	res := m.Resolution()
	if res == 0 {
		return current
	}
	cur, ok := ByID(current)
	if ok && current != 0 && cur.Resolution == res {
		return current
	}
	source := ""
	switch {
	case ok && current != 0:
		source = cur.Source
	default:
		source = SourceOf(name)
	}
	// A DVD isn't bigger than 480 lines, so a bigger picture says the
	// source was wrong too.
	if source == "dvd" && res > 480 {
		source = ""
	}
	// The qualities the profile takes at this size.
	var fits []Quality
	for _, id := range p.Allowed {
		if q, ok := ByID(id); ok && q.ID != 0 && q.Resolution == res {
			fits = append(fits, q)
		}
	}
	slices.SortFunc(fits, func(a, b Quality) int { return Rank(a.ID) - Rank(b.ID) })
	if len(fits) == 0 {
		// The profile takes nothing this size: say what it is.
		return Nearest(source, res).ID
	}
	if source != "" {
		want := Rank(Nearest(source, res).ID)
		best := fits[0]
		for _, q := range fits[1:] {
			if abs(Rank(q.ID)-want) < abs(Rank(best.ID)-want) {
				best = q
			}
		}
		return best.ID
	}
	// Not known where it came from: the lowest that meets the cutoff, or
	// the best the profile takes at this size.
	for _, q := range fits {
		if Rank(q.ID) >= Rank(p.Cutoff) {
			return q.ID
		}
	}
	return fits[len(fits)-1].ID
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
