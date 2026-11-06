package portal

import (
	"context"
	"errors"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
)

// Matinee passes on what people ask for in its apps, through the Radarr
// or Sonarr connection it has to Box Office (see arrapi), so their
// requests land on the Requests page with everyone else's. Each Matinee
// account is a member, the same one as when they sign in to the request
// site with Matinee. The request site needn't be turned on for this:
// it's the Requests page, its rules and its people that Matinee uses.

// MatineeAsk is a request passed on from Matinee.
type MatineeAsk struct {
	// User is the Matinee account that asked, by username.
	User   string
	Kind   string // movie or series
	TMDBID int64
	// Approved says Matinee lets this person add titles without an admin
	// (one of its admins, or everyone there): it's added straight away.
	Approved bool
	// ProfileID is the quality profile they picked, or 0.
	ProfileID int
}

// AskFromMatinee files a request from Matinee. Asking again for the same
// title answers with the request there is.
func (s *Service) AskFromMatinee(ctx context.Context, in MatineeAsk) (View, error) {
	m, err := s.matineeMember(ctx, in.User)
	if err != nil {
		return View{}, err
	}
	if m.Disabled {
		return View{}, badf("%s's account on the request site is turned off; ask an admin", m.Name())
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return View{}, err
	}
	return s.ask(ctx, st, m, in.Kind, in.TMDBID, "", asking{approved: in.Approved, profileID: in.ProfileID, again: true})
}

// RequestViews is where requests stand, by id; ids there's no request for
// any more are left out.
func (s *Service) RequestViews(ctx context.Context, ids []int64) ([]View, error) {
	var rs []store.Request
	for _, id := range ids {
		r, err := s.Store.Request(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return s.Views(ctx, rs)
}

// View is where one request stands.
func (s *Service) View(ctx context.Context, id int64) (View, error) {
	r, err := s.Store.Request(ctx, id)
	if err != nil {
		return View{}, err
	}
	vs, err := s.Views(ctx, []store.Request{r})
	if err != nil {
		return View{}, err
	}
	return vs[0], nil
}

// Withdraw takes back a request that's still waiting, for Matinee when
// someone deletes it there. One that's decided stays, as on the site.
func (s *Service) Withdraw(ctx context.Context, id int64) error {
	r, err := s.Store.Request(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil || r.Status != store.RequestPending {
		return err
	}
	return s.Store.DeleteRequest(ctx, id)
}

// ApproveWith approves a request with a quality profile, or the request
// site's when 0.
func (s *Service) ApproveWith(ctx context.Context, id int64, profileID int) (View, error) {
	if _, err := s.Approve(ctx, id, catalog.AddOptions{ProfileID: profileID}); err != nil {
		return View{}, err
	}
	return s.View(ctx, id)
}
