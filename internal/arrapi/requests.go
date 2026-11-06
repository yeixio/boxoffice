package arrapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/portal"
)

// Requests, for Matinee: what people ask for in Matinee's apps lands on
// Box Office's Requests page, under their Matinee account, and admins
// decide there (or in Matinee, which passes the decision on). Status says
// "requests" is here. Radarr's side takes movies and Sonarr's shows.
//
//	POST   /api/v3/boxoffice/requests                {user, tmdbId, approved, qualityProfileId}
//	GET    /api/v3/boxoffice/requests?ids=1,2        where those requests stand
//	POST   /api/v3/boxoffice/requests/{id}/approve   {qualityProfileId}
//	POST   /api/v3/boxoffice/requests/{id}/decline   {reply}
//	DELETE /api/v3/boxoffice/requests/{id}           taken back, while it waits

func (h *Handler) requestRoutes() {
	h.route("POST /api/v3/boxoffice/requests", h.needRequests(h.askRequest))
	h.route("GET /api/v3/boxoffice/requests", h.needRequests(h.requestStates))
	h.route("POST /api/v3/boxoffice/requests/{id}/approve", h.needRequests(h.approveRequest))
	h.route("POST /api/v3/boxoffice/requests/{id}/decline", h.needRequests(h.declineRequest))
	h.route("DELETE /api/v3/boxoffice/requests/{id}", h.needRequests(h.withdrawRequest))
}

func (h *Handler) needRequests(fn func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if h.Requests == nil {
			return errNotFound
		}
		return fn(w, r)
	}
}

// appProfile is Box Office's id for a quality profile Matinee named by
// the app's id, or 0.
func (h *Handler) appProfile(r *http.Request, id int) (int, error) {
	if id <= 0 {
		return 0, nil
	}
	m, err := h.profileIDs(r.Context())
	if err != nil {
		return 0, err
	}
	return m.fromApp(id), nil
}

func (h *Handler) askRequest(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		User      string `json:"user"`
		TMDBID    int64  `json:"tmdbId"`
		Approved  bool   `json:"approved"`
		ProfileID int    `json:"qualityProfileId"`
	}
	if err := read(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.User) == "" {
		return invalid("user", "say who asked")
	}
	if in.TMDBID <= 0 {
		return invalid("tmdbId", "say which title")
	}
	profile, err := h.appProfile(r, in.ProfileID)
	if err != nil {
		return err
	}
	v, err := h.Requests.AskFromMatinee(r.Context(), portal.MatineeAsk{User: in.User, Kind: h.kind(), TMDBID: in.TMDBID, Approved: in.Approved, ProfileID: profile})
	if err != nil {
		return err
	}
	return write(w, http.StatusCreated, v)
}

func (h *Handler) requestStates(w http.ResponseWriter, r *http.Request) error {
	var ids []int64
	for _, s := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
		if len(ids) == 500 {
			break
		}
	}
	vs, err := h.Requests.RequestViews(r.Context(), ids)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, vs)
}

func (h *Handler) approveRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		ProfileID int `json:"qualityProfileId"`
	}
	if r.ContentLength != 0 {
		if err := read(r, &in); err != nil {
			return err
		}
	}
	profile, err := h.appProfile(r, in.ProfileID)
	if err != nil {
		return err
	}
	v, err := h.Requests.ApproveWith(r.Context(), id, profile)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, v)
}

func (h *Handler) declineRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Reply string `json:"reply"`
	}
	if r.ContentLength != 0 {
		if err := read(r, &in); err != nil {
			return err
		}
	}
	if _, err := h.Requests.Decline(r.Context(), id, in.Reply); err != nil {
		return err
	}
	v, err := h.Requests.View(r.Context(), id)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, v)
}

func (h *Handler) withdrawRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := h.Requests.Withdraw(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
