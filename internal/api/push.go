package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeixio/boxoffice/internal/push"
	"github.com/yeixio/boxoffice/internal/store"
)

// Installing Box Office and the request site as a phone app, and push
// notifications to the devices that ask for them (internal/push).

func (s *Server) pushRoutes() []route {
	return []route{
		{"GET /manifest.webmanifest", false, s.manifest},
		{"GET /api/v1/push", true, s.pushInfo},
		{"POST /api/v1/push/devices", true, s.savePushDevice},
		{"DELETE /api/v1/push/devices/{id}", true, s.deletePushDevice},
		{"POST /api/v1/push/devices/{id}/test", true, s.testPushDevice},
		{"GET /api/portal/push", false, s.asMember(s.memberPush)},
		{"POST /api/portal/push", false, s.asMember(s.saveMemberPush)},
		{"POST /api/portal/push/remove", false, s.asMember(s.removeMemberPush)},
		{"POST /api/portal/push/test", false, s.asMember(s.testMemberPush)},
	}
}

// pusher is the push sender; one is made when the server wasn't given one.
func (s *Server) pusher() *push.Service {
	s.pushOnce.Do(func() {
		if s.Push == nil {
			s.Push = &push.Service{Store: s.Store, Log: s.Log}
		}
	})
	return s.Push
}

func pushErr(err error) error {
	var pe *push.Error
	switch {
	case errors.As(err, &pe):
		return errorf(http.StatusBadRequest, pe.Message)
	case errors.Is(err, push.ErrGone):
		return errorf(http.StatusGone, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return err
	}
	return err
}

// manifest tells a phone or browser how to install the site as an app:
// its name, colours and icons. The request site has its own, under the
// name it was given.
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) error {
	name, start := "Box Office", "/"
	onHost := s.portalHost(r)
	if onHost || r.URL.Query().Get("for") == "request" {
		name = "Requests"
		if s.Portal != nil {
			if st, err := s.Portal.Settings(r.Context()); err == nil && st.Name != "" {
				name = st.Name
			}
		}
		if !onHost {
			start = "/request"
		}
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	return json.NewEncoder(w).Encode(map[string]any{
		"id": start, "name": name, "short_name": name, "start_url": start, "scope": start,
		"display": "standalone", "background_color": "#15120f", "theme_color": "#15120f",
		"icons": []map[string]string{
			{"src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png"},
			{"src": "/icons/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	})
}

type pushDeviceIn struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Topics []string `json:"topics"`
}

func (in pushDeviceIn) device(r *http.Request, memberID int64) store.PushDevice {
	return store.PushDevice{Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth, MemberID: memberID, Topics: in.Topics, Name: deviceName(r.UserAgent())}
}

// deviceName says which device a browser is on, for the list of devices:
// "Chrome on Android".
func deviceName(ua string) string {
	has := func(s string) bool { return strings.Contains(ua, s) }
	browser := ""
	switch {
	case has("Edg/") || has("EdgA/") || has("EdgiOS/"):
		browser = "Edge"
	case has("OPR/"):
		browser = "Opera"
	case has("SamsungBrowser/"):
		browser = "Samsung Internet"
	case has("Firefox/") || has("FxiOS/"):
		browser = "Firefox"
	case has("Chrome/") || has("CriOS/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	}
	system := ""
	switch {
	case has("iPhone"):
		system = "iPhone"
	case has("iPad"):
		system = "iPad"
	case has("Android"):
		system = "Android"
	case has("Windows"):
		system = "Windows"
	case has("Mac OS X") || has("Macintosh"):
		system = "Mac"
	case has("CrOS"):
		system = "Chromebook"
	case has("Linux"):
		system = "Linux"
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	}
	return system
}

func (s *Server) pushInfo(w http.ResponseWriter, r *http.Request) error {
	key, err := s.pusher().PublicKey(r.Context())
	if err != nil {
		return err
	}
	devices, err := s.Store.PushDevices(r.Context(), 0)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"publicKey": key, "topics": push.Topics, "devices": devices})
}

func (s *Server) savePushDevice(w http.ResponseWriter, r *http.Request) error {
	var in pushDeviceIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	d, err := s.pusher().Save(r.Context(), in.device(r, 0))
	if err != nil {
		return pushErr(err)
	}
	return writeJSON(w, http.StatusOK, d)
}

// adminDevice finds one of the admins' devices, never a member's.
func (s *Server) adminDevice(r *http.Request) (store.PushDevice, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.PushDevice{}, err
	}
	d, err := s.Store.PushDevice(r.Context(), id)
	if err == nil && d.MemberID != 0 {
		err = store.ErrNotFound
	}
	return d, err
}

func (s *Server) deletePushDevice(w http.ResponseWriter, r *http.Request) error {
	d, err := s.adminDevice(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeletePushDevice(r.Context(), d.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) testPushDevice(w http.ResponseWriter, r *http.Request) error {
	d, err := s.adminDevice(r)
	if err != nil {
		return err
	}
	return s.pushTest(w, r, d)
}

// pushTest sends a device a test, and says what the push service said
// when it didn't take it.
func (s *Server) pushTest(w http.ResponseWriter, r *http.Request, d store.PushDevice) error {
	if err := s.pusher().Test(r.Context(), d); errors.Is(err, push.ErrGone) {
		return pushErr(err)
	} else if err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- the request site ----

// memberPush gives the key to subscribe with, and the endpoints of the
// member's devices, so a browser can tell whether it's one of them.
func (s *Server) memberPush(w http.ResponseWriter, r *http.Request, m store.Member) error {
	key, err := s.pusher().PublicKey(r.Context())
	if err != nil {
		return err
	}
	devices, err := s.Store.PushDevices(r.Context(), m.ID)
	if err != nil {
		return err
	}
	endpoints := []string{}
	for _, d := range devices {
		endpoints = append(endpoints, d.Endpoint)
	}
	return writeJSON(w, http.StatusOK, map[string]any{"publicKey": key, "endpoints": endpoints})
}

func (s *Server) saveMemberPush(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in pushDeviceIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if _, err := s.pusher().Save(r.Context(), in.device(r, m.ID)); err != nil {
		return pushErr(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) removeMemberPush(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in pushDeviceIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Store.DeletePushEndpoint(r.Context(), m.ID, in.Endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) testMemberPush(w http.ResponseWriter, r *http.Request, m store.Member) error {
	var in pushDeviceIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	devices, err := s.Store.PushDevices(r.Context(), m.ID)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.Endpoint == in.Endpoint {
			return s.pushTest(w, r, d)
		}
	}
	return store.ErrNotFound
}
