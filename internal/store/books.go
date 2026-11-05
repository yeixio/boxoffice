package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Book formats.
const (
	Ebook     = "ebook"
	Audiobook = "audiobook"
)

// Author is a writer on the list, for ebooks, audiobooks or both.
type Author struct {
	ID            int64     `json:"id"`
	OLKey         string    `json:"olKey"`
	Name          string    `json:"name"`
	SortName      string    `json:"sortName"`
	Born          string    `json:"born,omitempty"`
	Died          string    `json:"died,omitempty"`
	Bio           string    `json:"bio,omitempty"`
	NewEbooks     bool      `json:"newEbooks"`
	NewAudiobooks bool      `json:"newAudiobooks"`
	AddedAt       time.Time `json:"addedAt"`
	RefreshedAt   time.Time `json:"refreshedAt"`
	// Counted from its books when listed.
	Books            int `json:"books"`
	WantedEbooks     int `json:"wantedEbooks"`
	WantedAudiobooks int `json:"wantedAudiobooks"`
}

// Book is one of an author's books.
type Book struct {
	ID            int64  `json:"id"`
	AuthorID      int64  `json:"authorId"`
	OLKey         string `json:"olKey"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle,omitempty"`
	Year          int    `json:"year,omitempty"`
	CoverID       int64  `json:"-"`
	Editions      int    `json:"editions"`
	WantEbook     bool   `json:"wantEbook"`
	WantAudiobook bool   `json:"wantAudiobook"`
}

const authorCols = `a.id, a.ol_key, a.name, a.sort_name, a.born, a.died, a.bio, a.new_ebooks, a.new_audiobooks, a.added_at, a.refreshed_at,
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_ebook = 1),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_audiobook = 1)`

func scanAuthor(r interface{ Scan(...any) error }) (Author, error) {
	var a Author
	var ne, na int
	var added, refreshed int64
	err := r.Scan(&a.ID, &a.OLKey, &a.Name, &a.SortName, &a.Born, &a.Died, &a.Bio, &ne, &na, &added, &refreshed,
		&a.Books, &a.WantedEbooks, &a.WantedAudiobooks)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.NewEbooks, a.NewAudiobooks = ne == 1, na == 1
	a.AddedAt = time.Unix(added, 0).UTC()
	if refreshed > 0 {
		a.RefreshedAt = time.Unix(refreshed, 0).UTC()
	}
	return a, nil
}

// Authors lists the authors on the list, by sort name.
func (s *Store) Authors(ctx context.Context) ([]Author, error) {
	rows, err := s.query(ctx, "SELECT "+authorCols+" FROM authors a ORDER BY a.sort_name, a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Author{}
	for rows.Next() {
		a, err := scanAuthor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Author finds one author.
func (s *Store) Author(ctx context.Context, id int64) (Author, error) {
	return scanAuthor(s.row(ctx, "SELECT "+authorCols+" FROM authors a WHERE a.id = ?", id))
}

// AuthorByKey finds an author by Open Library key.
func (s *Store) AuthorByKey(ctx context.Context, key string) (Author, error) {
	return scanAuthor(s.row(ctx, "SELECT "+authorCols+" FROM authors a WHERE a.ol_key = ?", key))
}

// AddAuthor puts an author on the list; ErrExists if it's there already.
func (s *Store) AddAuthor(ctx context.Context, a Author) (Author, error) {
	if a.SortName == "" {
		a.SortName = SortTitle(a.Name)
	}
	id, err := s.insert(ctx, `INSERT INTO authors (ol_key, name, sort_name, born, died, bio, new_ebooks, new_audiobooks, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.OLKey, a.Name, a.SortName, a.Born, a.Died, a.Bio, b2i(a.NewEbooks), b2i(a.NewAudiobooks), now())
	if err != nil {
		return Author{}, err
	}
	return s.Author(ctx, id)
}

// UpdateAuthor saves an author's details and choices.
func (s *Store) UpdateAuthor(ctx context.Context, a Author) error {
	_, err := s.exec(ctx, "UPDATE authors SET name = ?, sort_name = ?, born = ?, died = ?, bio = ?, new_ebooks = ?, new_audiobooks = ? WHERE id = ?",
		a.Name, a.SortName, a.Born, a.Died, a.Bio, b2i(a.NewEbooks), b2i(a.NewAudiobooks), a.ID)
	return err
}

// AuthorRefreshed notes that an author's books were just brought up to
// date.
func (s *Store) AuthorRefreshed(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE authors SET refreshed_at = ? WHERE id = ?", now(), id)
	return err
}

// DeleteAuthor takes an author and their books off the list. It doesn't
// touch any files.
func (s *Store) DeleteAuthor(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM books WHERE author_id = ?", id); err != nil {
		return err
	}
	res, err := s.exec(ctx, "DELETE FROM authors WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const bookCols = "id, author_id, ol_key, title, subtitle, year, cover_id, editions, want_ebook, want_audiobook"

func scanBook(r interface{ Scan(...any) error }) (Book, error) {
	var b Book
	var we, wa int
	err := r.Scan(&b.ID, &b.AuthorID, &b.OLKey, &b.Title, &b.Subtitle, &b.Year, &b.CoverID, &b.Editions, &we, &wa)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	b.WantEbook, b.WantAudiobook = we == 1, wa == 1
	return b, err
}

// Books lists an author's books, those with the most editions first.
func (s *Store) Books(ctx context.Context, authorID int64) ([]Book, error) {
	rows, err := s.query(ctx, "SELECT "+bookCols+" FROM books WHERE author_id = ? ORDER BY editions DESC, year DESC, title", authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Book finds one book.
func (s *Store) Book(ctx context.Context, id int64) (Book, error) {
	return scanBook(s.row(ctx, "SELECT "+bookCols+" FROM books WHERE id = ?", id))
}

// UpsertBook adds a book, or brings its details up to date. A book already
// on the list keeps what's wanted of it; a new one takes b's wants. It
// reports whether the book is new.
func (s *Store) UpsertBook(ctx context.Context, b Book) (bool, error) {
	res, err := s.exec(ctx, "UPDATE books SET title = ?, subtitle = ?, year = ?, cover_id = ?, editions = ? WHERE ol_key = ? AND author_id = ?",
		b.Title, b.Subtitle, b.Year, b.CoverID, b.Editions, b.OLKey, b.AuthorID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = s.insert(ctx, "INSERT INTO books (author_id, ol_key, title, subtitle, year, cover_id, editions, want_ebook, want_audiobook) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		b.AuthorID, b.OLKey, b.Title, b.Subtitle, b.Year, b.CoverID, b.Editions, b2i(b.WantEbook), b2i(b.WantAudiobook))
	if errors.Is(err, ErrExists) {
		// Co-written with another author on the list; it stays with the
		// first.
		return false, nil
	}
	return err == nil, err
}

// SetBookWanted says whether a book is wanted in a format.
func (s *Store) SetBookWanted(ctx context.Context, id int64, format string, on bool) error {
	col := "want_ebook"
	if format == Audiobook {
		col = "want_audiobook"
	}
	res, err := s.exec(ctx, "UPDATE books SET "+col+" = ? WHERE id = ?", b2i(on), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAuthorBooksWanted says whether every book of an author is wanted in a
// format.
func (s *Store) SetAuthorBooksWanted(ctx context.Context, authorID int64, format string, on bool) error {
	col := "want_ebook"
	if format == Audiobook {
		col = "want_audiobook"
	}
	_, err := s.exec(ctx, "UPDATE books SET "+col+" = ? WHERE author_id = ?", b2i(on), authorID)
	return err
}
