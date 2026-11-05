package arrapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yeixio/boxoffice/internal/catalog"
)

// Discover, for Matinee: the same rows as Box Office's home page (see
// catalog.Home), through the Radarr or Sonarr connection Matinee already
// has, so Matinee's Discover tab works with Box Office in place of
// Overseerr. Asking for a title is adding it, through the usual movie and
// series calls. Status says "discover" is here.
//
//	GET /api/v3/boxoffice/discover              the home rows, and genres and networks
//	GET /api/v3/boxoffice/discover/rows/{id}    a page (?page=) of one row
//	GET /api/v3/boxoffice/discover/search?q=    movies and shows by name

func (h *Handler) discoverRoutes() {
	h.route("GET /api/v3/boxoffice/discover", h.discover)
	h.route("GET /api/v3/boxoffice/discover/rows/{id}", h.discoverRow)
	h.route("GET /api/v3/boxoffice/discover/search", h.discoverSearch)
}

func (h *Handler) discover(w http.ResponseWriter, r *http.Request) error {
	home, err := h.Catalog.Home(r.Context())
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, home)
}

func (h *Handler) discoverRow(w http.ResponseWriter, r *http.Request) error {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	p, err := h.Catalog.Row(r.Context(), r.PathValue("id"), page)
	if errors.Is(err, catalog.ErrNoRow) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, p)
}

func (h *Handler) discoverSearch(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return write(w, http.StatusOK, []any{})
	}
	res, err := h.Catalog.Search(r.Context(), q)
	if err != nil {
		return err
	}
	return write(w, http.StatusOK, res)
}
