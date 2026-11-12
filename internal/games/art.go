package games

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/store"
)

// ErrNoArt means none of the places art comes from has a picture of the
// game.
var ErrNoArt = errors.New("there's no picture of this game")

// artWidth is how wide the pictures kept are: enough for a game's page on
// a sharp screen, and small enough for a long list.
const artWidth = 480

// artRetry is how long Box Office waits before looking again for a game's
// picture it couldn't find.
const artRetry = 14 * 24 * time.Hour

var artKinds = []string{"Named_Boxarts", "Named_Titles", "Named_Snaps"}

var artLocks sync.Map // cache file -> *sync.Mutex

// ArtFile is the picture of a game, as a JPEG file kept in the data
// folder: its box art, or else its title screen or a screenshot, from
// libretro's thumbnails (or for a Switch game, the eShop's picture); or
// else the icon a PlayStation game carries of itself. The first time,
// it's downloaded and made small.
func (s *Service) ArtFile(ctx context.Context, g store.Game, system string) (string, error) {
	sys, ok := gamedat.LookupSystem(system)
	if !ok || g.Art == "" && g.Path == "" {
		return "", ErrNoArt
	}
	names := []string{g.Art}
	// A clone, or another version, may only have its parent's picture.
	if sys.Arcade && g.ParentTitle != "" && g.ParentTitle != g.Title {
		names = append(names, gamedat.ThumbName(g.ParentTitle))
	}
	key := strings.Join(names, "\x00")
	if g.Art == "" {
		key = "path:" + g.Path
	}
	sum := sha256.Sum256([]byte(key))
	p := filepath.Join(s.Dir, "art", sys.Key, hex.EncodeToString(sum[:12])+".jpg")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// ".none2": pictures behind links weren't found before.
	none := p + ".none2"
	if st, err := os.Stat(none); err == nil && time.Since(st.ModTime()) < artRetry {
		return s.iconArt(g, p)
	}
	l, _ := artLocks.LoadOrStore(p, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	s.artOnce.Do(func() { s.artSem = make(chan struct{}, 4) })
	select {
	case s.artSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.artSem }()
	bases := s.Art
	if len(bases) == 0 {
		bases = DefaultArt
	}
	var urls []string
	for _, name := range names {
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "https://") {
			urls = append(urls, name)
			continue
		}
		for _, kind := range artKinds {
			for _, base := range bases {
				urls = append(urls, strings.NewReplacer(
					"{system}", url.PathEscape(sys.Libretro),
					"{repo}", url.PathEscape(strings.ReplaceAll(sys.Libretro, " ", "_")),
					"{kind}", kind,
					"{name}", url.PathEscape(name),
				).Replace(base))
			}
		}
	}
	var lastErr error
	for _, u := range urls {
		img, err := s.fetchImage(ctx, u)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		if err := writeJPEG(p, shrink(img, artWidth)); err != nil {
			return "", err
		}
		os.Remove(none)
		return p, nil
	}
	if lastErr != nil {
		// Not found for want of an answer: the game's own icon for now,
		// and look again next time.
		if q, err := s.iconArt(g, p+".icon.jpg"); err == nil {
			return q, nil
		}
		return "", lastErr
	}
	os.MkdirAll(filepath.Dir(none), 0o750)
	os.WriteFile(none, nil, 0o640)
	return s.iconArt(g, p)
}

// iconArt is the icon a game carries of itself (PlayStation games do),
// kept at p.
func (s *Service) iconArt(g store.Game, p string) (string, error) {
	if g.Path == "" {
		return "", ErrNoArt
	}
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	b, err := gamedat.Icon(g.Path)
	if err != nil {
		return "", ErrNoArt
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", ErrNoArt
	}
	if err := writeJPEG(p, shrink(img, artWidth)); err != nil {
		return "", err
	}
	return p, nil
}

func (s *Service) fetchImage(ctx context.Context, u string) (image.Image, error) {
	return s.fetchImageOnce(ctx, u, false)
}

func (s *Service) fetchImageOnce(ctx context.Context, u string, followed bool) (image.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		// libretro's thumbnails on GitHub keep another name for a picture
		// as a link, which comes back as the name it links to.
		if to := linkTarget(b); to != "" && !followed {
			if i := strings.LastIndexByte(u, '/'); i >= 0 {
				return s.fetchImageOnce(ctx, u[:i+1]+url.PathEscape(to), true)
			}
		}
		// Not a picture: as good as none.
		return nil, os.ErrNotExist
	}
	return img, nil
}

// linkTarget is the picture a link's body names, if it's one.
func linkTarget(b []byte) string {
	t := strings.TrimSpace(string(b))
	if len(b) > 512 || !strings.HasSuffix(strings.ToLower(t), ".png") || strings.ContainsAny(t, "/\\\n\x00") {
		return ""
	}
	return t
}

func writeJPEG(p string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".art-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: 85}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// shrink makes a picture no wider than w, averaging the pixels each new
// one covers, on white where it's see-through.
func shrink(src image.Image, w int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	if sw <= w {
		w = sw
	}
	h := max(1, sh*w/sw)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					// Over white.
					r += uint64(cr + (0xffff - ca))
					g += uint64(cg + (0xffff - ca))
					bl += uint64(cb + (0xffff - ca))
					n++
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), 0xffff})
		}
	}
	return dst
}
