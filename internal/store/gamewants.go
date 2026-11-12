package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GameKind is the kind of a wanted game's downloads: their title is the
// game want.
const GameKind = "game"

// GameWant is a game wanted: a version picked from its system's list, to
// be searched for, downloaded and put in the system's folder.
type GameWant struct {
	ID     int64  `json:"id"`
	System string `json:"system"`
	// Name is the version wanted: its name in No-Intro's list, such as
	// "Super Metroid (Japan, USA) (En,Ja)", or MAME's set name.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Group is a console game's title as letters and digits: any version
	// of the game will do.
	Group string `json:"-"`
	Year  string `json:"year,omitempty"`
	Maker string `json:"maker,omitempty"`
	CRC   string `json:"crc,omitempty"`
	// Serial is a disc's serial or a download's title ID, where its list
	// gives one.
	Serial  string    `json:"serial,omitempty"`
	Size    int64     `json:"size,omitempty"`
	Art     string    `json:"-"`
	GotAt   time.Time `json:"gotAt,omitzero"`
	AddedAt time.Time `json:"addedAt"`
}

const gameWantCols = "id, system, name, title, grp, year, maker, crc, serial, size, art, got_at, added_at"

func scanGameWant(r interface{ Scan(...any) error }) (GameWant, error) {
	var w GameWant
	var got, added int64
	err := r.Scan(&w.ID, &w.System, &w.Name, &w.Title, &w.Group, &w.Year, &w.Maker, &w.CRC, &w.Serial, &w.Size, &w.Art, &got, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if got > 0 {
		w.GotAt = time.Unix(got, 0).UTC()
	}
	w.AddedAt = time.Unix(added, 0).UTC()
	return w, nil
}

// AddGameWant notes a game wanted; ErrExists if that version is wanted
// already.
func (s *Store) AddGameWant(ctx context.Context, w GameWant) (GameWant, error) {
	id, err := s.insert(ctx, "INSERT INTO game_wants (system, name, title, grp, year, maker, crc, serial, size, art, added_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		w.System, w.Name, w.Title, w.Group, w.Year, w.Maker, w.CRC, w.Serial, w.Size, w.Art, now())
	if err != nil {
		return GameWant{}, err
	}
	return s.GameWant(ctx, id)
}

// GameWant finds one game wanted.
func (s *Store) GameWant(ctx context.Context, id int64) (GameWant, error) {
	return scanGameWant(s.row(ctx, "SELECT "+gameWantCols+" FROM game_wants WHERE id = ?", id))
}

// GameWants lists the games wanted, newest first.
func (s *Store) GameWants(ctx context.Context) ([]GameWant, error) {
	rows, err := s.query(ctx, "SELECT "+gameWantCols+" FROM game_wants ORDER BY added_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GameWant{}
	for rows.Next() {
		w, err := scanGameWant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteGameWant forgets a game wanted.
func (s *Store) DeleteGameWant(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "DELETE FROM game_wants WHERE id = ?", id)
	return err
}

// GameWantGot notes that a download of a game wanted was moved into its
// system's folder, so it isn't looked for again before a check sees it.
func (s *Store) GameWantGot(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE game_wants SET got_at = ? WHERE id = ?", now(), id)
	return err
}

// GameOnDisk says whether a system's folders hold a game: that version
// (by name), or for a console any complete version of the same title
// (group, which may be "").
func (s *Store) GameOnDisk(ctx context.Context, system, name, group string) (bool, error) {
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM games g JOIN game_systems s ON s.id = g.system_id
		WHERE s.system = ? AND g.status IN ('complete', 'bios', 'identified') AND g.problems NOT LIKE '%"kind":"update"%' AND g.problems NOT LIKE '%"kind":"dlc"%' AND (g.name = ? OR (? <> '' AND g.parent = ?))`,
		system, name, group, group).Scan(&n)
	return n > 0, err
}

// ForgetGamesOnDisk forgets the games wanted for a system that its
// folders now hold, and says how many.
func (s *Store) ForgetGamesOnDisk(ctx context.Context, system string) (int64, error) {
	res, err := s.exec(ctx, `DELETE FROM game_wants WHERE system = ? AND EXISTS (
		SELECT 1 FROM games g JOIN game_systems s ON s.id = g.system_id
		WHERE s.system = game_wants.system AND g.status IN ('complete', 'bios', 'identified') AND g.problems NOT LIKE '%"kind":"update"%' AND g.problems NOT LIKE '%"kind":"dlc"%'
		AND (g.name = game_wants.name OR (game_wants.grp <> '' AND g.parent = game_wants.grp)))`, system)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
