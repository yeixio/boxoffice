package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// dumpTables are the tables a backup holds, parents before children.
// Sign-in sessions aren't kept: a restored server signs everyone in again.
var dumpTables = []string{
	"settings", "users", "quality_profiles", "root_folders", "movies", "series", "seasons", "episodes",
	"artists", "albums", "authors", "books",
	"media_files", "title_wants", "legacy_ids", "migrations", "jobs",
	"indexers", "download_clients", "usenet_servers", "blocklist", "imported_settings", "data_usage",
	"members", "requests", "issues", "problem_fixes", "problem_fix_files",
}

// DumpFormat names the backup format, so a restore can tell it's one.
const DumpFormat = "boxoffice-backup"

type dumpHeader struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type dumpTable struct {
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Dump writes every table as JSON. The same dump restores into SQLite or
// PostgreSQL, so a backup also moves a server from one to the other.
// skipSettings are settings left out, such as secrets a backup shouldn't hold.
func (s *Store) Dump(ctx context.Context, w io.Writer, skipSettings ...string) error {
	enc := json.NewEncoder(w)
	if err := enc.Encode(dumpHeader{Format: DumpFormat, Version: 1, CreatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	// One read transaction, so the tables agree with each other.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !s.postgres})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range dumpTables {
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+t)
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		d := dumpTable{Table: t, Columns: cols, Rows: [][]any{}}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return err
			}
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					vals[i] = string(b)
				}
			}
			if t == "settings" && len(vals) > 0 && contains(skipSettings, fmt.Sprint(vals[0])) {
				continue
			}
			d.Rows = append(d.Rows, vals)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ErrNotBackup means the file isn't a Box Office backup.
var ErrNotBackup = errors.New("that isn't a Box Office backup")

// Restore replaces everything in the database with a dump, in one
// transaction: if any of it fails, nothing changes. keepSettings are
// settings kept as they are, such as where backups go.
func (s *Store) Restore(ctx context.Context, r io.Reader, keepSettings ...string) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var h dumpHeader
	if err := dec.Decode(&h); err != nil || h.Format != DumpFormat {
		return ErrNotBackup
	}
	if h.Version > 1 {
		return fmt.Errorf("this backup is from a newer Box Office (format %d); update Box Office first", h.Version)
	}
	var tables []dumpTable
	for {
		var t dumpTable
		err := dec.Decode(&t)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the backup is damaged: %w", err)
		}
		if !contains(dumpTables, t.Table) {
			return fmt.Errorf("the backup has an unknown table %q", t.Table)
		}
		tables = append(tables, t)
	}
	kept := map[string]string{}
	for _, k := range keepSettings {
		if v, err := s.Setting(ctx, k); err == nil && v != "" {
			kept[k] = v
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM member_sessions"); err != nil {
		return err
	}
	for i := len(dumpTables) - 1; i >= 0; i-- {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+dumpTables[i]); err != nil {
			return err
		}
	}
	for _, t := range tables {
		cols := make([]string, len(t.Columns))
		marks := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			if strings.ContainsFunc(c, func(r rune) bool { return !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
				return fmt.Errorf("the backup has an odd column name %q", c)
			}
			cols[i], marks[i] = c, "?"
		}
		stmt := s.q("INSERT INTO " + t.Table + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")")
		for _, row := range t.Rows {
			if len(row) != len(cols) {
				return fmt.Errorf("the backup is damaged: a row in %s has %d values for %d columns", t.Table, len(row), len(cols))
			}
			for i, v := range row {
				if n, ok := v.(json.Number); ok {
					if iv, err := n.Int64(); err == nil {
						row[i] = iv
					} else if fv, err := n.Float64(); err == nil {
						row[i] = fv
					}
				}
			}
			if _, err := tx.ExecContext(ctx, stmt, row...); err != nil {
				return fmt.Errorf("restoring %s: %w", t.Table, err)
			}
		}
		if s.postgres && contains(t.Columns, "id") {
			if _, err := tx.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('"+t.Table+"', 'id'), COALESCE((SELECT MAX(id) FROM "+t.Table+"), 0) + 1, false)"); err != nil {
				return err
			}
		}
	}
	for k, v := range kept {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value"), k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
