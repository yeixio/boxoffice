package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// Sessions that have run out are cleared; the ones still good stay.
func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	u, err := st.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	good, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.CreateSession(ctx, u.ID, -time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.DeleteExpiredSessions(ctx); err != nil || n != 3 {
		t.Fatalf("cleared %d: %v", n, err)
	}
	if got, err := st.SessionUser(ctx, good); err != nil || got.ID != u.ID {
		t.Fatalf("the good session: %+v %v", got, err)
	}
	if n, _ := st.DeleteExpiredSessions(ctx); n != 0 {
		t.Fatalf("cleared %d more", n)
	}
}
