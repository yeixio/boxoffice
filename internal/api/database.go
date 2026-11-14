package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Settings → General → Database: which database Box Office uses, and moving
// it to another one (see internal/store/move.go). The move itself happens
// the next time Box Office starts, before anything else uses the database
// (cmd/boxoffice/database.go), so nothing written meanwhile is left behind.

// DatabaseInfo is where the server's database is.
type DatabaseInfo struct {
	// DSN is the database in use; Configured is what the server's settings
	// say, an older database when it was moved and they weren't changed.
	DSN, Configured string
}

type databaseView struct {
	Kind  string `json:"kind"` // sqlite or postgres
	Where string `json:"where"`
	// Configured is set when the server's settings still name the database
	// it was moved from.
	Configured string           `json:"configured,omitempty"`
	MovedFrom  *store.MovedFrom `json:"movedFrom,omitempty"`
	// Pending is a move waiting for the next start.
	Pending    string `json:"pending,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	CanRestart bool   `json:"canRestart"`
	// SuggestedFile is where a SQLite file could go; "" when the usual
	// place is taken.
	SuggestedFile string `json:"suggestedFile,omitempty"`
	// Downloads is how many downloads are running, which the move's
	// restart stops and picks up again.
	Downloads int `json:"downloads"`
}

func (s *Server) databaseView(ctx context.Context) databaseView {
	d := s.DatabaseInfo
	v := databaseView{Kind: "sqlite", Where: store.Describe(d.DSN), CanRestart: s.Restart != nil}
	if store.IsPostgres(d.DSN) {
		v.Kind = "postgres"
	}
	if d.Configured != "" && d.Configured != d.DSN {
		v.Configured = store.Describe(d.Configured)
	}
	if b, _ := s.Store.Setting(ctx, store.SettingMovedFrom); b != "" {
		var m store.MovedFrom
		if json.Unmarshal([]byte(b), &m) == nil {
			v.MovedFrom = &m
		}
	}
	if p, _ := s.Store.Setting(ctx, store.SettingMovePending); p != "" {
		v.Pending = store.Describe(p)
	}
	v.LastError, _ = s.Store.Setting(ctx, store.SettingMoveError)
	if s.DataDir != "" {
		if dir, err := filepath.Abs(s.DataDir); err == nil {
			f := filepath.Join(dir, "boxoffice.db")
			if _, err := os.Stat(f); os.IsNotExist(err) {
				v.SuggestedFile = f
			}
		}
	}
	if s.Downloads != nil {
		v.Downloads = len(s.Downloads.Live().Progress)
	}
	return v
}

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.databaseView(r.Context()))
}

type moveRequest struct {
	To string `json:"to"`
}

// moveTarget reads and checks where an admin wants the database moved.
func (s *Server) moveTarget(r *http.Request) (string, error) {
	var in moveRequest
	if err := readJSON(r, &in); err != nil {
		return "", errorf(http.StatusBadRequest, "invalid request")
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		return "", errorf(http.StatusBadRequest, "say where to move the database: a PostgreSQL URL or a SQLite file's full path")
	}
	if err := s.Store.CheckMoveTarget(r.Context(), to); err != nil {
		return "", errorf(http.StatusBadRequest, "can't move the database there: "+err.Error())
	}
	return to, nil
}

// checkDatabaseMove says whether a move to a database would work, without
// moving anything.
func (s *Server) checkDatabaseMove(w http.ResponseWriter, r *http.Request) error {
	to, err := s.moveTarget(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"where": store.Describe(to)})
}

type moveAnswer struct {
	databaseView
	// Restarting: Box Office is restarting to move the database now;
	// StartedAt is when this run started, to tell the next one apart.
	Restarting bool       `json:"restarting"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
}

// moveDatabase asks for the database to be moved at the next start, and
// restarts Box Office to do it when it can.
func (s *Server) moveDatabase(w http.ResponseWriter, r *http.Request) error {
	to, err := s.moveTarget(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.Store.SetSetting(ctx, store.SettingMoveError, "")
	if err := s.Store.SetSetting(ctx, store.SettingMovePending, to); err != nil {
		return err
	}
	s.Log.Warn("a database move is waiting for the next start", "to", store.Describe(to), "by", currentUser(r))
	out := moveAnswer{databaseView: s.databaseView(ctx)}
	if s.Restart != nil {
		out.Restarting = true
		started := s.started.UTC()
		out.StartedAt = &started
	}
	writeJSON(w, http.StatusAccepted, out)
	if out.Restarting {
		s.Log.Info("restarting to move the database", "by", currentUser(r))
		http.NewResponseController(w).Flush()
		// The web server waits for this answer to finish before it stops.
		go s.Restart()
	}
	return nil
}

func (s *Server) cancelDatabaseMove(w http.ResponseWriter, r *http.Request) error {
	if err := s.Store.SetSetting(r.Context(), store.SettingMovePending, ""); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.databaseView(r.Context()))
}
