package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/yeixio/boxoffice/internal/config"
	"github.com/yeixio/boxoffice/internal/store"
)

// openedDatabase is the database the server runs on.
type openedDatabase struct {
	db *store.Store
	// dsn is where it is; configured is what the server's settings say,
	// which is an older database when it was moved and the settings
	// weren't changed yet.
	dsn, configured string
}

// openDatabase opens the server's database: the one its settings say, or
// the one that database was moved to (see internal/store/move.go). A move
// an admin asked for in Settings happens first, before anything else uses
// the database; if it fails, the server carries on with the database it
// has and Settings says why.
func openDatabase(ctx context.Context, cfg config.Config, log *slog.Logger) (openedDatabase, error) {
	configured := cfg.Database()
	for range 5 {
		dsn, err := store.Follow(ctx, configured)
		if err != nil {
			return openedDatabase{}, err
		}
		if dsn != configured {
			log.Warn("the database was moved; using the new one. Change Box Office's database setting to it (BOXOFFICE_DATABASE_URL, or the Helm chart's database.url or database.existingSecret), then the old database can go",
				"configured", store.Describe(configured), "using", store.Describe(dsn))
		}
		db, err := store.Open(dsn)
		if err != nil {
			return openedDatabase{}, err
		}
		to, err := db.Setting(ctx, store.SettingMovePending)
		if err != nil || to == "" {
			return openedDatabase{db: db, dsn: dsn, configured: configured}, err
		}
		if err := moveAtStart(ctx, db, dsn, to, cfg.Addr, log); err != nil {
			return openedDatabase{db: db, dsn: dsn, configured: configured}, nil
		}
		// Opened again: Follow now finds the new database.
		db.Close()
	}
	return openedDatabase{}, errors.New("it was moved too many times in a row")
}

// moveAtStart makes the move an admin asked for. Either way, it's no
// longer asked for after this; a failure is kept for Settings to show.
func moveAtStart(ctx context.Context, db *store.Store, from, to, addr string, log *slog.Logger) error {
	db.SetSetting(ctx, store.SettingMovePending, "")
	start := "Starting."
	moving.Store(&start)
	stop := serveWhileMoving(addr, log)
	defer stop()
	log.Info("moving the database", "from", store.Describe(from), "to", store.Describe(to))
	var last time.Time
	report, err := db.MoveTo(ctx, to, func(table string, done, total int64) {
		p := fmt.Sprintf("Copied %d of %d rows.", done, total)
		moving.Store(&p)
		if time.Since(last) > 5*time.Second {
			last = time.Now()
			log.Info("moving the database", "table", table, "rows", done, "of", total)
		}
	})
	if err == nil {
		err = db.FinishMove(ctx, from, to, report)
	}
	if err != nil {
		log.Error("the database move failed; Box Office carries on with the database it has, which wasn't changed", "to", store.Describe(to), "err", err)
		db.SetSetting(ctx, store.SettingMoveError, err.Error())
		return err
	}
	db.SetSetting(ctx, store.SettingMoveError, "")
	log.Info("moved the database; every table has the same number of rows in both",
		"to", store.Describe(to), "tables", len(report.Tables), "rows", report.Rows, "took", report.Took.Round(time.Millisecond))
	return nil
}

// moving is how far a database move is, while one runs.
var moving atomic.Pointer[string]

// serveWhileMoving answers on Box Office's address while it moves its
// database, so Kubernetes doesn't take it for dead and restart it halfway
// (/healthz answers, /readyz doesn't), and a browser that gets here sees a
// page that says what's going on. The API answers 503, which the
// Restarting screen in Settings keeps waiting through.
func serveWhileMoving(addr string, log *slog.Logger) (stop func()) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Warn("can't answer while moving the database", "addr", addr, "err", err)
		return func() {}
	}
	srv := &http.Server{Handler: http.HandlerFunc(movingHandler), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("moving page stopped", "addr", addr, "err", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		ln.Close()
	}
}

func movingHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.Write([]byte("ok\n"))
		return
	case "/readyz":
		http.Error(w, "moving the database", http.StatusServiceUnavailable)
		return
	}
	progress := ""
	if p := moving.Load(); p != nil {
		progress = *p
	}
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/") || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "Box Office is moving its database. " + progress, "code": "moving_the_database"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(strings.Replace(movingPage, "{progress}", html.EscapeString(progress), 1)))
}

const movingPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="5">
<title>Box Office</title>
<style>
:root { color-scheme: light dark; --bg: #f7f6f3; --fg: #1c1b19; --muted: #6b6862; --accent: #d9a441; }
@media (prefers-color-scheme: dark) { :root { --bg: #141312; --fg: #f1efea; --muted: #a29e96; } }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--fg);
  font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 28rem; padding: 0 16px; text-align: center; }
h1 { font-size: 1.4rem; margin: 0 0 .5rem; }
p { color: var(--muted); margin: 0 0 .75rem; }
.dot { display: inline-block; width: .6rem; height: .6rem; border-radius: 50%; background: var(--accent);
  margin-right: .4rem; animation: pulse 1.4s ease-in-out infinite; }
@keyframes pulse { 50% { opacity: .25; } }
</style>
</head>
<body>
<main>
<h1><span class="dot"></span>Box Office is moving its database</h1>
<p>An admin asked to move Box Office's database. It opens again as soon as everything is copied and checked. {progress}</p>
<p>This page checks again every few seconds.</p>
</main>
</body>
</html>
`

// databaseCommand is "boxoffice database": moving the database by hand,
// with the server stopped.
func databaseCommand() error {
	args := os.Args[2:]
	usage := func() error {
		fmt.Fprint(os.Stderr, `Usage:
  boxoffice database where                Say which database Box Office uses
  boxoffice database move --to <database> Copy the database to another one and switch over to it
  boxoffice database undo-move            Use the database from before a move again

<database> is a PostgreSQL URL (postgres://user:password@host:5432/boxoffice)
or a SQLite file's full path (/data/boxoffice.db). Stop the Box Office server
before a move. Other flags, such as --data and --database-url, say which
database Box Office uses now, the same as for the server.
`)
		return errors.New("say what to do")
	}
	if len(args) == 0 {
		return usage()
	}
	ctx := context.Background()
	switch args[0] {
	case "where":
		cfg, err := config.Load(args[1:])
		if err != nil {
			return err
		}
		dsn, err := store.Follow(ctx, cfg.Database())
		if err != nil {
			return err
		}
		fmt.Println("Box Office uses", store.Describe(dsn))
		if dsn != cfg.Database() {
			fmt.Println("It was moved from", store.Describe(cfg.Database())+", which its settings still say. Change the settings to the new database.")
		}
		return nil
	case "move":
		fs := flag.NewFlagSet("move", flag.ContinueOnError)
		to := fs.String("to", "", "the database to move to: a PostgreSQL URL or a SQLite file's full path")
		noSwitch := fs.Bool("copy-only", false, "copy and check, but keep using the old database")
		rest := splitFlags(args[1:], "to", "copy-only")
		if err := fs.Parse(rest.own); err != nil {
			return err
		}
		if *to == "" {
			return usage()
		}
		cfg, err := config.Load(rest.others)
		if err != nil {
			return err
		}
		return moveByHand(ctx, cfg, *to, !*noSwitch)
	case "undo-move":
		cfg, err := config.Load(args[1:])
		if err != nil {
			return err
		}
		to, err := store.MovedTo(ctx, cfg.Database())
		if err != nil {
			return err
		}
		if to == "" {
			fmt.Println(store.Describe(cfg.Database()), "wasn't moved; nothing to undo.")
			return nil
		}
		if err := store.UndoMove(ctx, cfg.Database()); err != nil {
			return err
		}
		fmt.Println("Box Office uses", store.Describe(cfg.Database()), "again. Anything that changed in", store.Describe(to), "since the move isn't in it; that database is still there.")
		return nil
	}
	return usage()
}

// moveByHand is "boxoffice database move".
func moveByHand(ctx context.Context, cfg config.Config, to string, switchOver bool) error {
	from, err := store.Follow(ctx, cfg.Database())
	if err != nil {
		return err
	}
	if !store.IsPostgres(from) {
		if _, err := os.Stat(from); err != nil {
			return fmt.Errorf("there's no database at %s: %w", from, err)
		}
	}
	fmt.Println("Moving", store.Describe(from), "to", store.Describe(to))
	fmt.Println("Make sure the Box Office server is stopped: anything it writes during the move isn't copied.")
	db, err := store.Open(from)
	if err != nil {
		return err
	}
	defer db.Close()
	var last time.Time
	report, err := db.MoveTo(ctx, to, func(table string, done, total int64) {
		if time.Since(last) > time.Second || done == total {
			last = time.Now()
			fmt.Printf("\r  copied %d of %d rows", done, total)
		}
	})
	fmt.Println()
	if err != nil {
		return fmt.Errorf("move: %w (the old database wasn't changed)", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "table\trows\t")
	for _, t := range report.Tables {
		if t.Rows > 0 {
			fmt.Fprintf(w, "%s\t%d\t\n", t.Table, t.Rows)
		}
	}
	w.Flush()
	fmt.Printf("Copied %d rows in %d tables in %s; every table has the same number of rows in both.\n", report.Rows, len(report.Tables), report.Took.Round(time.Millisecond))
	if !switchOver {
		fmt.Println("Box Office still uses", store.Describe(from)+".")
		return nil
	}
	if err := db.FinishMove(ctx, from, to, report); err != nil {
		return fmt.Errorf("switch over: %w", err)
	}
	fmt.Println("Box Office uses the new database from its next start. The old one is kept as it was, with a note that sends Box Office to the new one.")
	if store.IsPostgres(to) {
		fmt.Println("Now set BOXOFFICE_DATABASE_URL (or the Helm chart's database.url or database.existingSecret) to the new database, so the old one can go.")
	} else if filepath.Base(to) == "boxoffice.db" {
		fmt.Println("Now take BOXOFFICE_DATABASE_URL out of Box Office's settings and set its data folder (BOXOFFICE_DATA_DIR or --data) to " + filepath.Dir(to) + ", so the old database can go.")
	} else {
		fmt.Println("Box Office's settings name a SQLite database as boxoffice.db in the data folder. Until the new file is that, keep the old database: its note sends Box Office to the new one.")
	}
	return nil
}

type splitArgs struct{ own, others []string }

// splitFlags separates a subcommand's own flags (names) from the server's,
// which say where the database is now.
func splitFlags(args []string, names ...string) splitArgs {
	var out splitArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		name, _, hasValue := strings.Cut(name, "=")
		own := false
		for _, n := range names {
			if strings.HasPrefix(a, "-") && name == n {
				own = true
			}
		}
		if !own {
			out.others = append(out.others, a)
			continue
		}
		out.own = append(out.own, a)
		if !hasValue && name != "copy-only" && i+1 < len(args) {
			i++
			out.own = append(out.own, args[i])
		}
	}
	return out
}
