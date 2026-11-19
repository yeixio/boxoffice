package api

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/yeixio/boxoffice/internal/zone"
)

// timeZoneView is the clock Box Office keeps, as Settings shows it.
type timeZoneView struct {
	// Zone is the zone in use, such as Europe/Berlin, and Saved the one
	// picked in Settings ("" follows the server's own).
	Zone  string `json:"zone"`
	Saved string `json:"saved"`
	// Server is the server's own zone, and FromEnv whether TZ set it.
	Server  string `json:"server"`
	FromEnv bool   `json:"fromEnv"`
	// Now is the time on Box Office's clock, with its offset from UTC.
	Now time.Time `json:"now"`
}

func (s *Server) timeZoneState(ctx context.Context) timeZoneView {
	saved, _ := s.Store.Setting(ctx, zone.Setting)
	server := time.Local.String()
	if server == "Local" || server == "" {
		server, _ = time.Now().Zone()
	}
	return timeZoneView{Zone: zone.Name(), Saved: saved, Server: server, FromEnv: os.Getenv("TZ") != "", Now: zone.Now().Truncate(time.Second)}
}

func (s *Server) timeZone(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.timeZoneState(r.Context()))
}

// setTimeZone picks the zone Box Office's clock is in, from now on; "" goes
// back to the server's own.
func (s *Server) setTimeZone(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Zone string `json:"zone"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Zone != "" {
		if _, err := zone.Load(in.Zone); err != nil {
			return errorf(http.StatusBadRequest, err.Error())
		}
	}
	if err := s.Store.SetSetting(r.Context(), zone.Setting, in.Zone); err != nil {
		return err
	}
	if err := zone.Set(in.Zone); err != nil {
		return err
	}
	s.Log.Info("the time zone was changed", "zone", zone.Name())
	return s.timeZone(w, r)
}

// adoptTimeZone takes the zone of the browser Box Office was set up in,
// when nothing says which zone the server is in: no zone picked, no TZ, and
// a system clock on UTC, as a container's is.
func (s *Server) adoptTimeZone(ctx context.Context, name string) {
	if name == "" || os.Getenv("TZ") != "" {
		return
	}
	if _, offset := time.Now().In(time.Local).Zone(); offset != 0 || (time.Local.String() != "UTC" && time.Local.String() != "Local") {
		return
	}
	if saved, _ := s.Store.Setting(ctx, zone.Setting); saved != "" {
		return
	}
	if _, err := zone.Load(name); err != nil {
		return
	}
	if s.Store.SetSetting(ctx, zone.Setting, name) == nil && zone.Set(name) == nil {
		s.Log.Info("using the time zone of the browser Box Office was set up in", "zone", name)
	}
}
