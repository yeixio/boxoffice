package api

import (
	"net/http"
	"os"
)

// Restarting Box Office from Settings. It stops the way it does when
// Kubernetes or Docker stops it: downloads stop where they are and carry on
// after the start, and the stop is noted as planned, so no "restarted
// unexpectedly" alert goes out. Then it starts again: in Kubernetes by
// exiting for the kubelet to start the container again, elsewhere by
// starting itself again in place.

// RestartHow says how Box Office comes back from a restart.
func RestartHow() string {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "kubernetes"
	}
	return "self"
}

type restartInfo struct {
	// Downloads is how many downloads are running, which the restart
	// stops and picks up again.
	Downloads int    `json:"downloads"`
	How       string `json:"how"`
	// Available is false where nothing was given to restart with, as in
	// tests.
	Available bool `json:"available"`
}

func (s *Server) getRestart(w http.ResponseWriter, r *http.Request) error {
	out := restartInfo{How: RestartHow(), Available: s.Restart != nil}
	if s.Downloads != nil {
		out.Downloads = len(s.Downloads.Live().Progress)
	}
	return writeJSON(w, http.StatusOK, out)
}

// postRestart answers first, then restarts, so the page hears back. It
// gives when this run started, for the page to tell the next one apart.
func (s *Server) postRestart(w http.ResponseWriter, r *http.Request) error {
	if s.Restart == nil {
		return errorf(http.StatusConflict, "this server can't restart itself; restart it where it runs")
	}
	s.Log.Info("restart asked for", "by", currentUser(r))
	writeJSON(w, http.StatusAccepted, map[string]any{"startedAt": s.started.UTC(), "how": RestartHow()})
	http.NewResponseController(w).Flush()
	// The web server waits for this answer to finish before it stops.
	go s.Restart()
	return nil
}
