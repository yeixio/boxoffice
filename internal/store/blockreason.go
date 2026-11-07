package store

import (
	"context"
	"regexp"
	"strings"
)

// reNZBGetStatus reads the status line Sonarr and Radarr keep for a
// release NZBGet failed: "PAR Status: NONE - Unpack Status: FAILURE - …".
var reNZBGetStatus = regexp.MustCompile(`(PAR|Unpack|Move|Script|Delete|Mark) Status: ([A-Za-z_{}]+)`)

// BlockReason says in Box Office's words why a release was blocked, for a
// reason brought over from Sonarr, Radarr, Lidarr or Readarr. Their own
// sentences are kept; NZBGet's status line (with its unfilled
// {scriptStatus}) becomes what went wrong.
func BlockReason(reason string) string {
	m := reNZBGetStatus.FindAllStringSubmatch(reason, -1)
	if len(m) < 2 {
		return reason
	}
	st := map[string]string{}
	for _, f := range m {
		st[f[1]] = strings.ToUpper(f[2])
	}
	switch {
	case st["Delete"] == "HEALTH":
		return "Missing articles: too much of it was gone from the news servers"
	case st["Unpack"] == "PASSWORD":
		return "Password protected"
	case st["Delete"] == "DUPE" || st["Delete"] == "COPY":
		return "Duplicate of a release already downloaded"
	case st["PAR"] == "FAILURE":
		return "Failed repair: too much was damaged or missing to repair"
	case st["Unpack"] == "SPACE":
		return "Not enough disk space to unpack"
	case st["Unpack"] == "FAILURE":
		return "Failed to unpack"
	case st["Delete"] == "BAD" || st["Mark"] == "BAD":
		return "Marked bad"
	case st["Delete"] == "SCAN":
		return "The NZB couldn't be read"
	case st["Move"] == "FAILURE":
		return "Couldn't be moved after downloading"
	case st["Delete"] == "MANUAL":
		return "Deleted by hand in the download client"
	case st["Script"] == "FAILURE":
		return "A post-processing script failed"
	}
	return "Failed in the download client"
}

// fixBlockReasons rewrites reasons brought over before BlockReason, once:
// a rewritten one no longer matches.
func (s *Store) fixBlockReasons(ctx context.Context) error {
	rows, err := s.query(ctx, "SELECT id, reason FROM blocklist WHERE reason LIKE 'PAR Status:%'")
	if err != nil {
		return err
	}
	fix := map[int64]string{}
	for rows.Next() {
		var id int64
		var reason string
		if err := rows.Scan(&id, &reason); err != nil {
			rows.Close()
			return err
		}
		if r := BlockReason(reason); r != reason {
			fix[id] = r
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, r := range fix {
		if _, err := s.exec(ctx, "UPDATE blocklist SET reason = ? WHERE id = ?", r, id); err != nil {
			return err
		}
	}
	return nil
}
