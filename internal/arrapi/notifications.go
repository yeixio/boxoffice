package arrapi

import (
	"net/http"
	"strconv"
)

// notifications is GET /api/v3/boxoffice/notifications, Box Office's own
// addition to the Radarr and Sonarr API: what Box Office has to tell
// people (see internal/notify), for Matinee to pass on. Both /radarr and
// /sonarr answer with every notification, movies and shows alike, so
// Matinee reads one of them per Box Office (by boxOfficeId in
// system/status).
//
// after is the last id the caller has; it gets what came since, oldest
// first, up to limit (100 by default, at most 500). after=-1 asks for
// nothing but where the feed is now, for a caller starting fresh that
// doesn't want the backlog.
func (h *Handler) notifications(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after, err = 0, nil
	}
	if err != nil {
		return invalid("after", "after must be a notification id")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	id, err := h.Catalog.Store.InstanceID(ctx)
	if err != nil {
		return err
	}
	last, err := h.Catalog.Store.LastNotification(ctx)
	if err != nil {
		return err
	}
	out := map[string]any{"boxOfficeId": id, "last": last, "notifications": []any{}}
	if after >= 0 {
		// A caller ahead of the feed (Box Office's database was restored
		// from an older backup) starts again from the beginning.
		if after > last {
			after = 0
		}
		list, err := h.Catalog.Store.Notifications(ctx, after, limit)
		if err != nil {
			return err
		}
		out["notifications"] = list
	}
	return write(w, http.StatusOK, out)
}
