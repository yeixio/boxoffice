package jobs

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// A job that stopping Box Office cut short isn't failed: it runs again at
// the next start.
func TestStoppedJobRunsAgain(t *testing.T) {
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(st, log)
	ctx, stop := context.WithCancel(context.Background())
	r.Handle("refresh", func(ctx context.Context, _ string) (Result, error) {
		stop()
		return Result{}, ctx.Err()
	})
	j, err := r.Queue(context.Background(), "refresh", "everything", "")
	if err != nil {
		t.Fatal(err)
	}
	r.RunAll(ctx)
	if got := job(t, r, j.ID); got != "running" {
		t.Fatalf("after the stop it's %s", got)
	}
	// The next start.
	ran := false
	r = New(st, log)
	r.Handle("refresh", func(context.Context, string) (Result, error) { ran = true; return Done("ok"), nil })
	if err := st.RequeueRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.RunAll(context.Background())
	if got := job(t, r, j.ID); !ran || got != "done" {
		t.Fatalf("ran %v, %s", ran, got)
	}
}

func job(t *testing.T, r *Runner, id int64) string {
	t.Helper()
	list, err := r.Store.Jobs(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range list {
		if j.ID == id {
			return j.Status
		}
	}
	t.Fatalf("job %d is gone", id)
	return ""
}
