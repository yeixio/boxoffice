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
	ID            int64  `json:"id"`
	OLKey         string `json:"olKey"`
	Name          string `json:"name"`
	SortName      string `json:"sortName"`
	Born          string `json:"born,omitempty"`
	Died          string `json:"died,omitempty"`
	Bio           string `json:"bio,omitempty"`
	NewEbooks     bool   `json:"newEbooks"`
	NewAudiobooks bool   `json:"newAudiobooks"`
	// Locked is Do not touch: Box Office never searches for, downloads or
	// moves files for this author's books.
	Locked      bool      `json:"locked"`
	AddedAt     time.Time `json:"addedAt"`
	RefreshedAt time.Time `json:"refreshedAt"`
	// Counted from its books when listed.
	Books            int `json:"books"`
	WantedEbooks     int `json:"wantedEbooks"`
	WantedAudiobooks int `json:"wantedAudiobooks"`
	// Books wanted in each format with a file in the library.
	HaveEbooks     int `json:"haveEbooks"`
	HaveAudiobooks int `json:"haveAudiobooks"`
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
	// The copies in the library, once Box Office has moved them in. An
	// audiobook's is its folder.
	EbookFile     *File `json:"ebookFile,omitempty"`
	AudiobookFile *File `json:"audiobookFile,omitempty"`
	// AuthorName is filled in where books of several authors are listed.
	AuthorName string `json:"authorName,omitempty"`
}

// IsBookKind says whether kind is a book format: the kind of a book's
// downloads and files.
func IsBookKind(kind string) bool { return kind == Ebook || kind == Audiobook }

const authorCols = `a.id, a.ol_key, a.name, a.sort_name, a.born, a.died, a.bio, a.new_ebooks, a.new_audiobooks, a.locked, a.added_at, a.refreshed_at,
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_ebook = 1),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_audiobook = 1),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_ebook = 1
		AND EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'ebook' AND f.owner_id = b.id)),
	(SELECT COUNT(*) FROM books b WHERE b.author_id = a.id AND b.want_audiobook = 1
		AND EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'audiobook' AND f.owner_id = b.id))`

func scanAuthor(r interface{ Scan(...any) error }) (Author, error) {
	var a Author
	var ne, na, locked int
	var added, refreshed int64
	err := r.Scan(&a.ID, &a.OLKey, &a.Name, &a.SortName, &a.Born, &a.Died, &a.Bio, &ne, &na, &locked, &added, &refreshed,
		&a.Books, &a.WantedEbooks, &a.WantedAudiobooks, &a.HaveEbooks, &a.HaveAudiobooks)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.NewEbooks, a.NewAudiobooks, a.Locked = ne == 1, na == 1, locked == 1
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
	_, err := s.exec(ctx, "UPDATE authors SET name = ?, sort_name = ?, born = ?, died = ?, bio = ?, new_ebooks = ?, new_audiobooks = ?, locked = ? WHERE id = ?",
		a.Name, a.SortName, a.Born, a.Died, a.Bio, b2i(a.NewEbooks), b2i(a.NewAudiobooks), b2i(a.Locked), a.ID)
	return err
}

// AuthorRefreshed notes that an author's books were just brought up to
// date.
func (s *Store) AuthorRefreshed(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE authors SET refreshed_at = ? WHERE id = ?", now(), id)
	return err
}

// DeleteAuthor takes an author and their books off the list. It doesn't
// touch any files: Box Office just forgets them.
func (s *Store) DeleteAuthor(ctx context.Context, id int64) error {
	if _, err := s.exec(ctx, "DELETE FROM media_files WHERE kind IN ('ebook', 'audiobook') AND owner_id IN (SELECT id FROM books WHERE author_id = ?)", id); err != nil {
		return err
	}
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

// withFiles fills in the books' copies in the library.
func (s *Store) withFiles(ctx context.Context, list []Book) error {
	if len(list) == 0 {
		return nil
	}
	at := map[int64]int{}
	for i, b := range list {
		at[b.ID] = i
	}
	rows, err := s.query(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE kind IN ('ebook', 'audiobook')")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f File
		var added int64
		if err := rows.Scan(&f.ID, &f.Kind, &f.OwnerID, &f.Path, &f.Size, &f.Quality, &added); err != nil {
			return err
		}
		f.AddedAt = time.Unix(added, 0).UTC()
		i, ok := at[f.OwnerID]
		if !ok {
			continue
		}
		if f.Kind == Ebook {
			list[i].EbookFile = &f
		} else {
			list[i].AudiobookFile = &f
		}
	}
	return rows.Err()
}

// File is the book's copy in a format, if Box Office moved one in.
func (b Book) File(format string) *File {
	if format == Audiobook {
		return b.AudiobookFile
	}
	return b.EbookFile
}

// Wants says whether the book is wanted in a format.
func (b Book) Wants(format string) bool {
	if format == Audiobook {
		return b.WantAudiobook
	}
	return b.WantEbook
}

func (s *Store) bookList(ctx context.Context, q string, args ...any) ([]Book, error) {
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, s.withFiles(ctx, out)
}

// AllBooks lists every book on the list, with its author's name, by
// author and title.
func (s *Store) AllBooks(ctx context.Context) ([]Book, error) {
	list, err := s.bookList(ctx, "SELECT "+bookCols+" FROM books ORDER BY title, id")
	if err != nil {
		return nil, err
	}
	as, err := s.Authors(ctx)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, a := range as {
		names[a.ID] = a.Name
	}
	for i := range list {
		list[i].AuthorName = names[list[i].AuthorID]
	}
	return list, nil
}

// Books lists an author's books, those with the most editions first.
func (s *Store) Books(ctx context.Context, authorID int64) ([]Book, error) {
	return s.bookList(ctx, "SELECT "+bookCols+" FROM books WHERE author_id = ? ORDER BY editions DESC, year DESC, title", authorID)
}

// Book finds one book.
func (s *Store) Book(ctx context.Context, id int64) (Book, error) {
	b, err := scanBook(s.row(ctx, "SELECT "+bookCols+" FROM books WHERE id = ?", id))
	if err != nil {
		return b, err
	}
	for _, f := range []string{Ebook, Audiobook} {
		var file File
		var added int64
		err := s.row(ctx, "SELECT id, kind, owner_id, path, size, quality, added_at FROM media_files WHERE kind = ? AND owner_id = ?", f, id).
			Scan(&file.ID, &file.Kind, &file.OwnerID, &file.Path, &file.Size, &file.Quality, &added)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return b, err
		}
		file.AddedAt = time.Unix(added, 0).UTC()
		if f == Ebook {
			b.EbookFile = &file
		} else {
			b.AudiobookFile = &file
		}
	}
	return b, nil
}

// WantedBooks lists the books wanted in a format that have no copy in the
// library yet, leaving out authors on Do not touch.
func (s *Store) WantedBooks(ctx context.Context, format string) ([]Book, error) {
	col := "want_ebook"
	if format == Audiobook {
		col = "want_audiobook"
	}
	return s.bookList(ctx, `SELECT `+bookCols+` FROM books b WHERE b.`+col+` = 1
		AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.kind = ? AND f.owner_id = b.id)
		AND NOT EXISTS (SELECT 1 FROM authors a WHERE a.id = b.author_id AND a.locked = 1)
		ORDER BY b.id`, format)
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
