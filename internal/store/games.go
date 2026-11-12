package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// GameSystem is one system's folder of games, found in a games root
// folder.
type GameSystem struct {
	ID         int64     `json:"id"`
	System     string    `json:"system"`
	Path       string    `json:"path"`
	RootFolder string    `json:"rootFolder"`
	Locked     bool      `json:"locked"`
	Format     string    `json:"format,omitempty"`
	List       string    `json:"list,omitempty"`
	Support    int       `json:"support"`
	Note       string    `json:"note,omitempty"`
	AddedAt    time.Time `json:"addedAt"`
	CheckedAt  time.Time `json:"checkedAt,omitzero"`
	// Counted from its games when listed: all of them, how many games
	// they are once clones and other versions are put with their game,
	// and how many have problems.
	Games    int `json:"games"`
	Groups   int `json:"groups"`
	Problems int `json:"problems"`
}

// GameProblem is one thing wrong with a game's files.
type GameProblem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Game is a game found on disk.
type Game struct {
	ID          int64         `json:"id"`
	SystemID    int64         `json:"systemId"`
	Name        string        `json:"name"`
	Title       string        `json:"title"`
	Parent      string        `json:"parent,omitempty"`
	ParentTitle string        `json:"parentTitle,omitempty"`
	Year        string        `json:"year,omitempty"`
	Maker       string        `json:"maker,omitempty"`
	Players     int           `json:"players,omitempty"`
	Genre       string        `json:"genre,omitempty"`
	Path        string        `json:"path"`
	InParent    bool          `json:"inParent,omitempty"`
	Size        int64         `json:"size"`
	MTime       int64         `json:"-"`
	CRC         string        `json:"crc,omitempty"`
	Status      string        `json:"status"`
	Problems    []GameProblem `json:"problems"`
	Art         string        `json:"-"`
	AddedAt     time.Time     `json:"addedAt"`
}

const gameSystemCols = `s.id, s.system, s.path, s.root_folder, s.locked, s.format, s.list, s.support, s.note, s.added_at, s.checked_at,
	(SELECT COUNT(*) FROM games g WHERE g.system_id = s.id),
	(SELECT COUNT(DISTINCT CASE WHEN g.parent <> '' THEN g.parent ELSE g.name END) FROM games g WHERE g.system_id = s.id),
	(SELECT COUNT(*) FROM games g WHERE g.system_id = s.id AND g.status <> 'complete')`

func scanGameSystem(r interface{ Scan(...any) error }) (GameSystem, error) {
	var g GameSystem
	var locked int
	var added, checked int64
	err := r.Scan(&g.ID, &g.System, &g.Path, &g.RootFolder, &locked, &g.Format, &g.List, &g.Support, &g.Note, &added, &checked,
		&g.Games, &g.Groups, &g.Problems)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	g.Locked = locked == 1
	g.AddedAt = time.Unix(added, 0).UTC()
	if checked > 0 {
		g.CheckedAt = time.Unix(checked, 0).UTC()
	}
	return g, nil
}

// GameSystems lists the systems' folders, by path.
func (s *Store) GameSystems(ctx context.Context) ([]GameSystem, error) {
	rows, err := s.query(ctx, "SELECT "+gameSystemCols+" FROM game_systems s ORDER BY s.path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GameSystem{}
	for rows.Next() {
		g, err := scanGameSystem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GameSystem finds one system's folder.
func (s *Store) GameSystem(ctx context.Context, id int64) (GameSystem, error) {
	return scanGameSystem(s.row(ctx, "SELECT "+gameSystemCols+" FROM game_systems s WHERE s.id = ?", id))
}

// GameSystemByPath finds a system's folder by its path.
func (s *Store) GameSystemByPath(ctx context.Context, path string) (GameSystem, error) {
	return scanGameSystem(s.row(ctx, "SELECT "+gameSystemCols+" FROM game_systems s WHERE s.path = ?", path))
}

// AddGameSystem notes a system's folder; ErrExists if it's there already.
func (s *Store) AddGameSystem(ctx context.Context, g GameSystem) (GameSystem, error) {
	id, err := s.insert(ctx, "INSERT INTO game_systems (system, path, root_folder, added_at) VALUES (?, ?, ?, ?)", g.System, g.Path, g.RootFolder, now())
	if err != nil {
		return GameSystem{}, err
	}
	return s.GameSystem(ctx, id)
}

// SetGameSystem changes what system a folder holds.
func (s *Store) SetGameSystem(ctx context.Context, id int64, system string) error {
	res, err := s.exec(ctx, "UPDATE game_systems SET system = ? WHERE id = ?", system, id)
	if err != nil {
		return err
	}
	return affected(res)
}

// LockGameSystem turns a system's Do not touch on or off.
func (s *Store) LockGameSystem(ctx context.Context, id int64, on bool) error {
	res, err := s.exec(ctx, "UPDATE game_systems SET locked = ? WHERE id = ?", b2i(on), id)
	if err != nil {
		return err
	}
	return affected(res)
}

// GameSystemNote saves why a system's check couldn't finish, without
// touching its games.
func (s *Store) GameSystemNote(ctx context.Context, id int64, note string) error {
	_, err := s.exec(ctx, "UPDATE game_systems SET note = ? WHERE id = ?", note, id)
	return err
}

// DeleteGameSystem forgets a system's folder and its games. The files
// stay where they are.
func (s *Store) DeleteGameSystem(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM games WHERE system_id = ?", id); err != nil {
		return err
	}
	res, err := s.exec(ctx, "DELETE FROM game_systems WHERE id = ?", id)
	if err != nil {
		return err
	}
	return affected(res)
}

// GameCheck is what one check of a system's folder found.
type GameCheck struct {
	Format  string
	List    string
	Support int
	Games   []Game
}

// SaveGameCheck puts a check's games in place of the system's last ones.
// A game found before keeps the day it was first found.
func (s *Store) SaveGameCheck(ctx context.Context, systemID int64, c GameCheck) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type key struct{ name, path string }
	added := map[key]int64{}
	rows, err := tx.QueryContext(ctx, s.q("SELECT name, path, added_at FROM games WHERE system_id = ?"), systemID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k key
		var at int64
		if err := rows.Scan(&k.name, &k.path, &at); err != nil {
			rows.Close()
			return err
		}
		added[k] = at
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, s.q("DELETE FROM games WHERE system_id = ?"), systemID); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, s.q(`INSERT INTO games (system_id, name, title, parent, parent_title, year, maker, players, genre, path, in_parent, size, mtime, crc, status, problems, art, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`))
	if err != nil {
		return err
	}
	defer ins.Close()
	t := now()
	for _, g := range c.Games {
		at, ok := added[key{g.Name, g.Path}]
		if !ok {
			at = t
		}
		if g.Problems == nil {
			g.Problems = []GameProblem{}
		}
		probs, _ := json.Marshal(g.Problems)
		if _, err := ins.ExecContext(ctx, systemID, g.Name, g.Title, g.Parent, g.ParentTitle, g.Year, g.Maker, g.Players, g.Genre, g.Path, b2i(g.InParent),
			g.Size, g.MTime, g.CRC, g.Status, string(probs), g.Art, at); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q("UPDATE game_systems SET format = ?, list = ?, support = ?, note = '', checked_at = ? WHERE id = ?"),
		c.Format, c.List, c.Support, t, systemID); err != nil {
		return err
	}
	return tx.Commit()
}

const gameCols = `id, system_id, name, title, parent, parent_title, year, maker, players, genre, path, in_parent, size, mtime, crc, status, problems, art, added_at`

func scanGame(r interface{ Scan(...any) error }) (Game, error) {
	var g Game
	var inParent int
	var probs string
	var added int64
	err := r.Scan(&g.ID, &g.SystemID, &g.Name, &g.Title, &g.Parent, &g.ParentTitle, &g.Year, &g.Maker, &g.Players, &g.Genre, &g.Path, &inParent,
		&g.Size, &g.MTime, &g.CRC, &g.Status, &probs, &g.Art, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	g.InParent = inParent == 1
	if json.Unmarshal([]byte(probs), &g.Problems) != nil || g.Problems == nil {
		g.Problems = []GameProblem{}
	}
	g.AddedAt = time.Unix(added, 0).UTC()
	return g, nil
}

// Games lists a system's games, by title.
func (s *Store) Games(ctx context.Context, systemID int64) ([]Game, error) {
	rows, err := s.query(ctx, "SELECT "+gameCols+" FROM games WHERE system_id = ? ORDER BY title, name", systemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Game{}
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Game finds one game.
func (s *Store) Game(ctx context.Context, id int64) (Game, error) {
	return scanGame(s.row(ctx, "SELECT "+gameCols+" FROM games WHERE id = ?", id))
}
