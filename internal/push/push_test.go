package push_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/push"
	"github.com/yeixio/boxoffice/internal/push/pushtest"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

var b64 = base64.RawURLEncoding

func un(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A message sealed by the web-push library for Node, so the browser's side
// the other tests lean on is checked against something Box Office didn't
// write.
func TestOpensAnotherSendersMessage(t *testing.T) {
	key, err := ecdh.P256().NewPrivateKey(un(t, "xnGTWQ7QzP_0quW3Md0iBJakWAbOQSEcjbEYwbPF1X8"))
	if err != nil {
		t.Fatal(err)
	}
	body := un(t, "NKLrbRYPa19Hq8W8xzDmdgAAEABBBIlPxTQjOVVtpP1ZuB3PSS8UA9in-NOa1VDYDC4vyLPLJziS4_iGwuhGndn0voMpJjFdOaIl_yT_l223s2X5xcYE6ylrTxqRqsj8LvTXnYiNObgq95-U5h-BSsEE_mfZx5ByYFn-uTlMOREzRmeyPzKFDz6rybNrwVnB")
	text, err := pushtest.Open(body, key, un(t, "5tPVQaoYDf_FW0ebr19QUA"))
	if err != nil || string(text) != "When I grow up, I want to be a watermelon" {
		t.Fatalf("%q %v", text, err)
	}
}

func service(t *testing.T) (*push.Service, *store.Store) {
	db := storetest.Open(t)
	return &push.Service{Store: db, AnyAddress: true}, db
}

func save(t *testing.T, s *push.Service, d *pushtest.Device, memberID int64, topics ...string) store.PushDevice {
	t.Helper()
	saved, err := s.Save(context.Background(), store.PushDevice{Endpoint: d.Endpoint, P256dh: d.P256dh, Auth: d.Auth, MemberID: memberID, Topics: topics})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestSendIsSealedAndSigned(t *testing.T) {
	s, _ := service(t)
	ctx := context.Background()
	phone := pushtest.New(t)
	d := save(t, s, phone, 0, push.TopicReady)
	if err := s.Send(ctx, d, push.Message{Title: "Dune (2021) is ready", Body: "It's downloaded and in the library.", URL: "/movies/3"}); err != nil {
		t.Fatal(err)
	}
	got := phone.Got()
	if len(got) != 1 || got[0]["title"] != "Dune (2021) is ready" || got[0]["url"] != "/movies/3" {
		t.Fatalf("got %v, problems %v", got, phone.Problems())
	}
	// It says who sent it, signed with the key browsers subscribe to.
	h := phone.Headers()[0]
	if h.Get("TTL") == "" {
		t.Error("no TTL, which push services insist on")
	}
	auth := h.Get("Authorization")
	token, k, ok := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ", k=")
	pub, _ := s.PublicKey(ctx)
	if !ok || k != pub {
		t.Fatalf("authorization %q, key %q", auth, pub)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", token)
	}
	var claims struct {
		Aud, Sub string
		Exp      int64
	}
	json.Unmarshal(un(t, parts[1]), &claims)
	if !strings.HasPrefix(phone.Endpoint, claims.Aud+"/") || claims.Sub == "" || claims.Exp < time.Now().Unix() || claims.Exp > time.Now().Add(24*time.Hour).Unix() {
		t.Errorf("claims %+v", claims)
	}
	point := un(t, k)
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		t.Fatal(err)
	}
	sig := un(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the signature doesn't match the key")
	}
	// The key stays the same, or every device would be cut off.
	if again, _ := (&push.Service{Store: s.Store}).PublicKey(ctx); again != pub {
		t.Error("the key changed")
	}
}

func TestAdminsHearAboutWhatTheyPicked(t *testing.T) {
	s, _ := service(t)
	ctx := context.Background()
	ready, problems := pushtest.New(t), pushtest.New(t)
	save(t, s, ready, 0, push.TopicReady, push.TopicRequests, "made-up")
	save(t, s, problems, 0, push.TopicProblems)

	s.Notify(ctx, store.Notification{Event: store.EventGrabbed, Level: store.LevelInfo, Title: "Downloading Dune (2021)"})
	s.Notify(ctx, store.Notification{Event: store.EventImported, Level: store.LevelInfo, Title: "Dune (2021) is ready", Page: "/movies/3"})
	s.Notify(ctx, store.Notification{Event: "request.created", Level: store.LevelInfo, Title: "Alice asked for Heat (1995)", Page: "/requests"})
	s.Notify(ctx, store.Notification{Event: store.EventFailed, Level: store.LevelWarning, Title: "Heat (1995) didn't download"})
	s.Notify(ctx, store.Notification{Event: "alert.disk", Level: store.LevelCritical, Title: "The library disk is nearly full"})
	s.Notify(ctx, store.Notification{Event: "alert.disk", Level: store.LevelInfo, Title: "The library disk has room again"})

	if got := titles(ready); got != "Dune (2021) is ready|Alice asked for Heat (1995)" {
		t.Errorf("ready and requests: %s", got)
	}
	if got := titles(problems); got != "Heat (1995) didn't download|The library disk is nearly full" {
		t.Errorf("problems: %s", got)
	}
	if got := ready.Got()[0]["url"]; got != "/movies/3" {
		t.Errorf("opens %q", got)
	}
}

func titles(d *pushtest.Device) string {
	var out []string
	for _, m := range d.Got() {
		out = append(out, m["title"])
	}
	return strings.Join(out, "|")
}

func TestMembersHearWhenWhatTheyAskedForArrives(t *testing.T) {
	s, db := service(t)
	ctx := context.Background()
	member := func(name string) store.Member {
		m, err := db.CreateMember(ctx, store.Member{Username: name}, "x")
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	alice, bob, carol := member("alice"), member("bob"), member("carol")
	ask := func(m store.Member, status string, tmdb int64) {
		if _, err := db.AddRequest(ctx, store.Request{MemberID: m.ID, Kind: "movie", TMDBID: tmdb, Title: "Dune", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	ask(alice, store.RequestApproved, 438631)
	ask(bob, store.RequestDeclined, 438631)
	ask(carol, store.RequestApproved, 949)
	a1, a2, b, c, admin := pushtest.New(t), pushtest.New(t), pushtest.New(t), pushtest.New(t), pushtest.New(t)
	save(t, s, a1, alice.ID)
	save(t, s, a2, alice.ID)
	save(t, s, b, bob.ID)
	save(t, s, c, carol.ID)
	save(t, s, admin, 0, push.TopicProblems)

	s.Notify(ctx, store.Notification{Event: store.EventImported, Title: "Dune (2021) is ready", Page: "/movies/3",
		Media: &store.Media{Kind: "movie", Title: "Dune", TMDBID: 438631}})
	for _, d := range []*pushtest.Device{a1, a2} {
		got := d.Got()
		if len(got) != 1 || got[0]["title"] != "Dune (2021) is ready" || got[0]["url"] != push.MemberPage {
			t.Errorf("alice's device got %v", got)
		}
	}
	if len(b.Got())+len(c.Got())+len(admin.Got()) != 0 {
		t.Errorf("others heard: %v %v %v", b.Got(), c.Got(), admin.Got())
	}
	// A member's device never gets what admins hear about.
	s.Notify(ctx, store.Notification{Event: store.EventFailed, Level: store.LevelWarning, Title: "Dune (2021) didn't download"})
	if len(a1.Got()) != 1 || len(admin.Got()) != 1 {
		t.Errorf("after a problem: alice %v admin %v", a1.Got(), admin.Got())
	}
	// Removing someone removes their devices.
	if err := db.DeleteMember(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.PushDevices(ctx, alice.ID); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestADroppedSubscriptionIsForgotten(t *testing.T) {
	s, db := service(t)
	ctx := context.Background()
	phone := pushtest.New(t)
	d := save(t, s, phone, 0, push.TopicReady)
	phone.Answer(500)
	if err := s.Send(ctx, d, push.Message{Title: "x"}); err == nil || errors.Is(err, push.ErrGone) {
		t.Fatalf("a push service in trouble: %v", err)
	}
	if left, _ := db.PushDevices(ctx, 0); len(left) != 1 {
		t.Fatal("forgotten over a passing problem")
	}
	phone.Answer(410)
	if err := s.Send(ctx, d, push.Message{Title: "x"}); !errors.Is(err, push.ErrGone) {
		t.Fatalf("gone: %v", err)
	}
	if left, _ := db.PushDevices(ctx, 0); len(left) != 0 {
		t.Fatalf("still kept: %v", left)
	}
}

func TestSaveChecksWhatABrowserGave(t *testing.T) {
	s, db := service(t)
	ctx := context.Background()
	phone := pushtest.New(t)
	good := store.PushDevice{Endpoint: phone.Endpoint, P256dh: phone.P256dh, Auth: phone.Auth}
	for name, change := range map[string]func(*store.PushDevice){
		"no address":  func(d *store.PushDevice) { d.Endpoint = "" },
		"not a url":   func(d *store.PushDevice) { d.Endpoint = "fcm" },
		"short key":   func(d *store.PushDevice) { d.P256dh = "AAAA" },
		"not a point": func(d *store.PushDevice) { d.P256dh = b64.EncodeToString(make([]byte, 65)) },
		"short auth":  func(d *store.PushDevice) { d.Auth = "AAAA" },
	} {
		d := good
		change(&d)
		var pe *push.Error
		if _, err := s.Save(ctx, d); !errors.As(err, &pe) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Only https, outside of tests.
	strict := &push.Service{Store: db}
	var pe *push.Error
	if _, err := strict.Save(ctx, good); !errors.As(err, &pe) {
		t.Errorf("plain http: %v", err)
	}
	// The same device again changes what it hears about; it isn't added twice.
	first := save(t, s, phone, 0, push.TopicReady)
	second := save(t, s, phone, 0, push.TopicProblems)
	if first.ID != second.ID || len(second.Topics) != 1 || second.Topics[0] != push.TopicProblems {
		t.Errorf("%+v then %+v", first, second)
	}
	// A member can't pile up devices without end.
	m, _ := db.CreateMember(ctx, store.Member{Username: "dave"}, "x")
	for i := range 11 {
		d := good
		d.Endpoint = phone.Endpoint + "/" + string(rune('a'+i))
		d.MemberID = m.ID
		_, err := s.Save(ctx, d)
		if (i < 10) != (err == nil) {
			t.Errorf("device %d: %v", i+1, err)
		}
	}
}
