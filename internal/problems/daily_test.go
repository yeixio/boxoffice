package problems

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sent struct{ title, message string }

// lookAndWait runs a look, daily or the admin's own, and waits for it.
func lookAndWait(t *testing.T, s *Service, daily bool) {
	t.Helper()
	if !s.start(context.Background(), daily) {
		t.Fatal("a look was already running")
	}
	for range 500 {
		if st, _ := s.State(); !st.Looking {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the look didn't finish")
}

func alerted(ch chan sent) *sent {
	select {
	case a := <-ch:
		return &a
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func TestDailyAlertsOnlyNew(t *testing.T) {
	l := newLibrary(t)
	ch := make(chan sent, 4)
	newService := func() *Service {
		return &Service{Store: l.st, Log: slog.New(slog.DiscardHandler), Tools: noTools,
			Alert: func(_ context.Context, title, message string) { ch <- sent{title, message} }}
	}
	s := newService()

	lookAndWait(t, s, true)
	a := alerted(ch)
	if a == nil || a.title != "15 new problem files" {
		t.Fatalf("first daily look: %+v", a)
	}
	if !strings.Contains(a.message, "1 disc image") || !strings.Contains(a.message, "3 names or folders to tidy") || !strings.Contains(a.message, "Settings → Problem files") {
		t.Fatalf("message %q", a.message)
	}
	if st, _ := s.State(); st.Next == nil || st.Next.Sub(*st.LookedAt) != Every {
		t.Fatalf("next look %+v", st.Next)
	}

	// Nothing new, and a restart doesn't make old news new.
	s = newService()
	lookAndWait(t, s, true)
	if a := alerted(ch); a != nil {
		t.Fatalf("alerted again: %+v", a)
	}

	// What the admin's own look found has been seen: no alert, then or later.
	touch(t, filepath.Join(l.movies, "Another Film 2004.mkv"), 3)
	lookAndWait(t, s, false)
	lookAndWait(t, s, true)
	if a := alerted(ch); a != nil {
		t.Fatalf("alerted about what the admin saw: %+v", a)
	}

	touch(t, filepath.Join(l.movies, "Brazil (1985)", "BRAZIL 2.iso"), 4)
	lookAndWait(t, s, true)
	a = alerted(ch)
	if a == nil || a.title != "1 new problem file" || !strings.HasPrefix(a.message, "Box Office's daily look through the library found 1 disc image.") || !strings.Contains(a.message, "17 in all") {
		t.Fatalf("new disc image: %+v", a)
	}
}

func TestRunWaitsForTheDay(t *testing.T) {
	l := newLibrary(t)
	ctx := context.Background()
	at := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	l.st.SetSetting(ctx, settingLookedAt, at.Format(time.RFC3339))
	s := &Service{Store: l.st, Log: slog.New(slog.DiscardHandler), Tools: noTools}
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Run(run); close(done) }()
	defer func() { stop(); <-done }()
	for range 100 {
		if st, _ := s.State(); st.Next != nil {
			if !st.Next.Equal(at.Add(Every)) {
				t.Fatalf("next look at %v, want %v", st.Next, at.Add(Every))
			}
			if st.Looking || st.LookedAt != nil {
				t.Fatal("looked before its time")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Run didn't set the next look")
}
