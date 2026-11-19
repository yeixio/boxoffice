package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/lists"
	"github.com/yeixio/boxoffice/internal/store"
)

// Followed lists (Settings → Library → Lists): a TMDB list, a Trakt
// watchlist or list, or a Plex watchlist, whose titles are added by
// themselves or wait for a yes (see internal/lists).
func (s *Server) listRoutes() []route {
	return []route{
		{"GET /api/v1/lists", true, s.getLists},
		{"POST /api/v1/lists/look", true, s.lookAtList},
		{"POST /api/v1/lists", true, s.followList},
		{"PUT /api/v1/lists/trakt", true, s.putTraktClient},
		{"PUT /api/v1/lists/{id}", true, s.saveList},
		{"DELETE /api/v1/lists/{id}", true, s.deleteList},
		{"POST /api/v1/lists/{id}/check", true, s.checkList},
		{"GET /api/v1/lists/{id}/items", true, s.listItems},
		{"POST /api/v1/lists/{id}/items", true, s.decideListItem},
	}
}

// lister is the list follower: the one main.go made, or one made here (it
// then only answers; nothing runs it).
func (s *Server) lister() *lists.Service {
	s.listOnce.Do(func() {
		if s.Lists == nil {
			s.Lists = &lists.Service{Store: s.Store, Catalog: s.Catalog, Log: s.Log, UserAgent: "BoxOffice/" + s.Version}
		}
	})
	return s.Lists
}

// listErr turns the follower's refusals into answers.
func listErr(err error) error {
	var le *lists.Error
	if errors.As(err, &le) {
		return errorf(http.StatusBadRequest, le.Message)
	}
	return err
}

type listView struct {
	store.List
	// Counts is how many of its titles are in each state.
	Counts map[string]int `json:"counts"`
}

type listsView struct {
	Lists []listView `json:"lists"`
	// TraktClient says whether Trakt's client ID is saved; it never leaves
	// the server.
	TraktClient bool `json:"traktClient"`
	// EveryHours is how often lists are read, and PerCheck the most one
	// adds each time.
	EveryHours int `json:"everyHours"`
	PerCheck   int `json:"perCheck"`
}

func (s *Server) getLists(w http.ResponseWriter, r *http.Request) error {
	all, err := s.Store.Lists(r.Context())
	if err != nil {
		return err
	}
	counts, err := s.Store.ListCounts(r.Context())
	if err != nil {
		return err
	}
	key, err := s.Store.Setting(r.Context(), lists.SettingTraktClient)
	if err != nil {
		return err
	}
	out := listsView{Lists: []listView{}, TraktClient: key != "", EveryHours: int(lists.Every.Hours()), PerCheck: lists.PerCheck}
	for _, l := range all {
		c := counts[l.ID]
		if c == nil {
			c = map[string]int{}
		}
		out.Lists = append(out.Lists, listView{l, c})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) lookAtList(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Address string `json:"address"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	pre, err := s.lister().Look(r.Context(), in.Address)
	if errors.Is(err, lists.ErrNoTraktKey) {
		// Not a mistake: the page asks for the client ID next.
		return writeJSON(w, http.StatusOK, map[string]any{"source": lists.SourceTrakt, "needsTraktClient": true})
	}
	if err != nil {
		return listErr(err)
	}
	return writeJSON(w, http.StatusOK, pre)
}

func (s *Server) followList(w http.ResponseWriter, r *http.Request) error {
	var in store.List
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.Enabled = true
	l, err := s.lister().Follow(r.Context(), in)
	if err != nil {
		return listErr(err)
	}
	// Read it for the first time now, without making the page wait.
	go s.lister().Check(context.WithoutCancel(r.Context()), l.ID)
	return writeJSON(w, http.StatusCreated, listView{l, map[string]int{}})
}

func (s *Server) saveList(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in store.List
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.ID = id
	l, err := s.lister().Save(r.Context(), in)
	if err != nil {
		return listErr(err)
	}
	return writeJSON(w, http.StatusOK, l)
}

func (s *Server) deleteList(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.Store.List(r.Context(), id); err != nil {
		return err
	}
	if err := s.Store.DeleteList(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) checkList(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.lister().Check(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err != nil {
		var le *lists.Error
		if errors.As(err, &le) {
			return errorf(http.StatusBadRequest, le.Message)
		}
		return errorf(http.StatusBadGateway, "The list couldn't be read: "+err.Error())
	}
	return writeJSON(w, http.StatusOK, res)
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.Store.List(r.Context(), id); err != nil {
		return err
	}
	items, err := s.Store.ListItems(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, items)
}

func (s *Server) decideListItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Kind   string `json:"kind"`
		TMDBID int64  `json:"tmdbId"`
		Add    bool   `json:"add"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	it, err := s.lister().Decide(r.Context(), id, in.Kind, in.TMDBID, in.Add)
	if err != nil {
		return listErr(err)
	}
	return writeJSON(w, http.StatusOK, it)
}

// putTraktClient saves (or with "", forgets) the client ID of the admin's
// Trakt API app.
func (s *Server) putTraktClient(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ClientID string `json:"clientId"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.ClientID = strings.TrimSpace(in.ClientID)
	if in.ClientID != "" && (len(in.ClientID) < 32 || len(in.ClientID) > 128 || strings.Trim(strings.ToLower(in.ClientID), "0123456789abcdefghijklmnopqrstuvwxyz") != "") {
		return errorf(http.StatusBadRequest, "A Trakt client ID is a long run of letters and digits (64 of them). Copy the Client ID from your Trakt app's page, with nothing before or after it.")
	}
	if err := s.Store.SetSetting(r.Context(), lists.SettingTraktClient, in.ClientID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
