// Package health looks Box Office over for problems an admin can fix, and
// says how to fix each: missing keys, folders it can't see or write to,
// files that aren't where it thinks, connections missing a password,
// backups that stopped, and tasks that keep failing.
package health

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/backup"
	"github.com/yeixio/boxoffice/internal/store"
)

// An Issue is one problem, and how to fix it.
type Issue struct {
	ID     string `json:"id"`
	Level  string `json:"level"` // error (something doesn't work), warning (something will go wrong), notice
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Steps say how to fix it, in order.
	Steps []string `json:"steps"`
	// Link is the page where it's fixed.
	Link *Link `json:"link,omitempty"`
}

type Link struct {
	To    string `json:"to"`
	Label string `json:"label"`
}

// Checker runs the checks.
type Checker struct {
	Store   *store.Store
	Backups *backup.Manager
	// TMDBKey is the key in use, "" for none.
	TMDBKey func(ctx context.Context) string
	now     func() time.Time
}

// Thresholds.
const (
	lowSpace    = 10 << 30 // bytes free on a root folder's disk
	fileSample  = 200      // files checked on disk each time
	failedSince = 24 * time.Hour
)

func (c *Checker) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Check runs every check, worst first. A check that can't run is an issue
// itself.
func (c *Checker) Check(ctx context.Context) []Issue {
	var out []Issue
	for _, check := range []func(context.Context) ([]Issue, error){c.tmdb, c.rootFolders, c.files, c.connections, c.backups, c.jobs} {
		issues, err := check(ctx)
		if err != nil {
			out = append(out, Issue{ID: "check-failed", Level: "warning", Title: "A health check couldn't run", Detail: err.Error()})
			continue
		}
		out = append(out, issues...)
	}
	rank := map[string]int{"error": 0, "warning": 1, "notice": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Level] < rank[out[j].Level] })
	return out
}

var settingsLink = &Link{To: "/settings", Label: "Open Settings"}

func (c *Checker) tmdb(ctx context.Context) ([]Issue, error) {
	if c.TMDBKey == nil || c.TMDBKey(ctx) != "" {
		return nil, nil
	}
	return []Issue{{
		ID: "tmdb-key", Level: "warning", Title: "No TMDB key",
		Detail: "Box Office looks movies and shows up on TMDB. Without a key it can't add new titles or keep details, posters and episode lists up to date.",
		Steps: []string{
			"Sign up for a free account at themoviedb.org and confirm your email.",
			"On themoviedb.org, open Settings, then API, click Create and answer Yes to personal use.",
			"Copy the API Key (the longer API Read Access Token works too).",
			"In Box Office, open Settings, paste it under TMDB and click Check and save.",
		},
		Link: settingsLink,
	}}, nil
}

var kindName = map[string]string{"movie": "movie", "series": "TV"}

func (c *Checker) rootFolders(ctx context.Context) ([]Issue, error) {
	var out []Issue
	for _, kind := range []string{"movie", "series"} {
		fs, err := c.Store.RootFolders(ctx, kind)
		if err != nil {
			return nil, err
		}
		if len(fs) == 0 {
			out = append(out, Issue{
				ID: "root-none-" + kind, Level: "warning", Title: fmt.Sprintf("No %s root folder", kindName[kind]),
				Detail: fmt.Sprintf("Every %s goes in its own folder inside a root folder, so Box Office can't add any until there is one.", map[string]string{"movie": "movie", "series": "show"}[kind]),
				Steps: []string{
					fmt.Sprintf("Make sure your %s library is mounted into Box Office (in Kubernetes, the media list in its Helm values; in Docker, a -v volume).", kindName[kind]),
					fmt.Sprintf("In Settings, under %s root folders, click Add and enter the folder's path as Box Office sees it, such as %s.", titleCase(kindName[kind]), map[string]string{"movie": "/media/movies", "series": "/media/tv"}[kind]),
				},
				Link: settingsLink,
			})
		}
		for _, f := range fs {
			id := fmt.Sprintf("root-%d", f.ID)
			st, err := os.Stat(f.Path)
			switch {
			case err != nil || !st.IsDir():
				out = append(out, Issue{
					ID: id + "-missing", Level: "error", Title: "Box Office can't see " + f.Path,
					Detail: fmt.Sprintf("It's a %s root folder, but there's no folder there, so Box Office can't find or add files in it.", kindName[f.Kind]),
					Steps: []string{
						"Check the volume holding it is mounted into Box Office at that path: in Kubernetes, the media list in its Helm values (then sync); in Docker, the -v volume.",
						"If the library is at another path now, add that path as a root folder in Settings and remove this one.",
					},
					Link: settingsLink,
				})
				continue
			case !writable(f.Path):
				out = append(out, Issue{
					ID: id + "-readonly", Level: "notice", Title: f.Path + " is read-only",
					Detail: "Box Office can read your library there but can't put new downloads in it, upgrade files or rename them. That's expected while you try Box Office out next to your old apps.",
					Steps: []string{
						"When you're ready for Box Office to manage the library, mount the volume read-write: in Kubernetes, remove readOnly: true from its entry in the media list of the Helm values and sync; in Docker, drop :ro from the -v volume.",
						"Shut down Radarr or Sonarr first, so two apps don't change the same files.",
					},
				})
			}
			if free := freeSpace(f.Path); free >= 0 && free < lowSpace {
				out = append(out, Issue{
					ID: id + "-space", Level: "warning", Title: "Little space left on " + f.Path,
					Detail: fmt.Sprintf("Only %s is free, so downloads may not fit.", gib(free)),
					Steps:  []string{"Free some space on that disk, or add a root folder on another disk in Settings."},
					Link:   settingsLink,
				})
			}
		}
	}
	return out, nil
}

func (c *Checker) files(ctx context.Context) ([]Issue, error) {
	paths, err := c.Store.SampleFilePaths(ctx, fileSample)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	var missing []string
	for _, p := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	is := Issue{
		ID: "files-missing", Level: "warning", Title: "Some files aren't where Box Office expects them",
		Detail: fmt.Sprintf("%d of %d files checked aren't there, such as %s.", len(missing), len(paths), missing[0]),
		Steps: []string{
			"If they were moved or deleted outside Box Office, refresh the title on its page so Box Office looks again.",
			"If a whole library is missing, check its volume is mounted into Box Office at the same path as its root folder.",
		},
	}
	if len(missing)*2 >= len(paths) {
		is.Level, is.Title = "error", "Most files aren't where Box Office expects them"
		is.Steps = []string{
			"Check the library is mounted into Box Office: in Kubernetes, the media list in its Helm values; in Docker, the -v volumes.",
			"If you moved in from Radarr or Sonarr, the old app may see the library at another path. Move in again and set the path under \"Where Box Office sees your library\"; titles already here are kept.",
		}
		is.Link = &Link{To: "/import", Label: "Open Move in"}
	}
	return []Issue{is}, nil
}

func (c *Checker) connections(ctx context.Context) ([]Issue, error) {
	ixs, err := c.Store.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	dcs, err := c.Store.DownloadClients(ctx)
	if err != nil {
		return nil, err
	}
	var names, sources []string
	add := func(name, source string) {
		names = append(names, name)
		if source != "" && !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	for _, ix := range ixs {
		if ix.MissingSecret {
			add(ix.Name, ix.Source)
		}
	}
	for _, dc := range dcs {
		if dc.MissingSecret {
			add(dc.Name, dc.Source)
		}
	}
	from := "the old app"
	if len(sources) > 0 {
		from = strings.Join(sources, " and ")
	}
	if len(names) == 0 {
		return nil, nil
	}
	return []Issue{{
		ID: "connections-secret", Level: "warning", Title: plural(len(names), "connection needs", "connections need") + " a password or API key",
		Detail: from + " hides these from other apps, so they came over blank and Box Office can't use them yet: " + strings.Join(names, ", ") + ".",
		Steps: []string{
			"In Settings, under Brought over from other apps, remove the ones marked needs its key.",
			"Move in from " + from + " again, and leave \"Have it make a backup of itself\" switched on: Box Office reads the hidden keys from that backup. Titles already here are kept as they are.",
		},
		Link: &Link{To: "/import", Label: "Open Move in"},
	}}, nil
}

func (c *Checker) backups(ctx context.Context) ([]Issue, error) {
	if c.Backups == nil {
		return nil, nil
	}
	s, err := c.Backups.Settings(ctx)
	if err != nil {
		return nil, err
	}
	link := &Link{To: "/backups", Label: "Open Backups"}
	if s.EveryHours == 0 {
		return []Issue{{
			ID: "backups-off", Level: "warning", Title: "Scheduled backups are off",
			Detail: "Box Office only backs itself up when you ask, so a lost disk could lose your library's settings and history.",
			Steps:  []string{"In Backups, set how often to back up, such as every 24 hours, and save."},
			Link:   link,
		}}, nil
	}
	var out []Issue
	list, err := c.Backups.List(ctx)
	if err != nil {
		return nil, err
	}
	var newest time.Time
	for _, b := range list {
		if b.Local && b.CreatedAt.After(newest) {
			newest = b.CreatedAt
		}
	}
	// A scheduled backup is checked for every 10 minutes; well past when
	// one was due means they're failing.
	if due := time.Duration(s.EveryHours)*time.Hour + time.Hour; !newest.IsZero() && c.clock().Sub(newest) > due {
		out = append(out, Issue{
			ID: "backups-late", Level: "error", Title: "Backups have stopped",
			Detail: fmt.Sprintf("The newest backup is from %s, though one is due every %d hours.", newest.Local().Format("Jan 2, 15:04"), s.EveryHours),
			Steps: []string{
				"In Backups, click Back up now. If it fails, the message says why, such as a full disk.",
				"Check Box Office's data volume has space free.",
			},
			Link: link,
		})
	}
	if s.Remote.Kind == "" {
		out = append(out, Issue{
			ID: "backups-local", Level: "notice", Title: "Backups are only on this server",
			Detail: "If its disk is lost, so are the backups.",
			Steps:  []string{"In Backups, choose another place for them: a folder on a NAS mounted into Box Office, or S3-compatible cloud storage, and click Test, then Save."},
			Link:   link,
		})
	}
	return out, nil
}

func (c *Checker) jobs(ctx context.Context) ([]Issue, error) {
	jobs, err := c.Store.Jobs(ctx, 200)
	if err != nil {
		return nil, err
	}
	var failed []store.Job
	for _, j := range jobs {
		if j.Status == "failed" && c.clock().Sub(j.CreatedAt) < failedSince {
			failed = append(failed, j)
		}
	}
	if len(failed) == 0 {
		return nil, nil
	}
	last := failed[0]
	detail := fmt.Sprintf("The latest, %s", cmpOr(last.Subject, last.Kind))
	if last.Message != "" {
		detail += ": " + last.Message
	}
	return []Issue{{
		ID: "jobs-failed", Level: "warning", Title: plural(len(failed), "task", "tasks") + " failed in the last day",
		Detail: detail + ".",
		Steps:  []string{"Open Activity to see each one and why it failed. Most say what to fix, such as a TMDB key or a folder Box Office can't see."},
		Link:   &Link{To: "/activity", Label: "Open Activity"},
	}}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func gib(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

func titleCase(s string) string {
	if s == "" || s == "TV" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
