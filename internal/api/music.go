package api

import (
	"errors"
	"net/http"

	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/store"
)

// musicErr turns the music list's own errors into answers.
func musicErr(err error) error {
	switch {
	case errors.Is(err, music.ErrOff):
		return errorf(http.StatusConflict, err.Error())
	case errors.Is(err, musicbrainz.ErrNotFound):
		return errorf(http.StatusNotFound, err.Error())
	}
	return err
}

func (s *Server) lookupArtists(w http.ResponseWriter, r *http.Request) error {
	found, err := s.Music.Lookup(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		return musicErr(err)
	}
	return writeJSON(w, http.StatusOK, found)
}

func (s *Server) artists(w http.ResponseWriter, r *http.Request) error {
	as, err := s.Store.Artists(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, as)
}

func (s *Server) addArtist(w http.ResponseWriter, r *http.Request) error {
	var in music.AddOptions
	if err := readJSON(r, &in); err != nil {
		return err
	}
	a, err := s.Music.Add(r.Context(), in)
	if err != nil {
		if a.ID != 0 {
			// On the list, but its albums couldn't be fetched yet; a
			// refresh tries again.
			s.Log.Warn("fetching an artist's albums", "artist", a.Name, "err", err)
			return writeJSON(w, http.StatusCreated, a)
		}
		return musicErr(err)
	}
	return writeJSON(w, http.StatusCreated, a)
}

// artistView is an artist with its albums.
type artistView struct {
	store.Artist
	AlbumList []albumView `json:"albumList"`
}

type albumView struct {
	store.Album
	Cover string `json:"cover"`
}

func (s *Server) artist(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.Store.Artist(r.Context(), id)
	if err != nil {
		return err
	}
	return s.writeArtist(w, r, a, http.StatusOK)
}

func (s *Server) writeArtist(w http.ResponseWriter, r *http.Request, a store.Artist, status int) error {
	albums, err := s.Store.Albums(r.Context(), a.ID)
	if err != nil {
		return err
	}
	v := artistView{Artist: a, AlbumList: make([]albumView, 0, len(albums))}
	for _, b := range albums {
		v.AlbumList = append(v.AlbumList, albumView{b, musicbrainz.CoverURL(b.MBID)})
	}
	return writeJSON(w, status, v)
}

func (s *Server) editArtist(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in music.Edit
	if err := readJSON(r, &in); err != nil {
		return err
	}
	a, err := s.Music.Update(r.Context(), id, in)
	if err != nil {
		return musicErr(err)
	}
	return s.writeArtist(w, r, a, http.StatusOK)
}

func (s *Server) refreshArtist(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.Music.Refresh(r.Context(), id)
	if err != nil {
		return musicErr(err)
	}
	return s.writeArtist(w, r, a, http.StatusOK)
}

// deleteArtist takes an artist off the list; its files stay where they are.
func (s *Server) deleteArtist(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteArtist(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) editAlbum(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Monitored *bool `json:"monitored"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Monitored == nil {
		return errorf(http.StatusBadRequest, "say whether to watch for it")
	}
	if err := s.Store.SetAlbumMonitored(r.Context(), id, *in.Monitored); err != nil {
		return err
	}
	b, err := s.Store.Album(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, albumView{b, musicbrainz.CoverURL(b.MBID)})
}

func (s *Server) albumTypes(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, map[string]any{"types": music.AlbumTypes, "defaults": music.DefaultAlbumTypes})
}
