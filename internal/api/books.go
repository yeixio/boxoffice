package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/search"
	"github.com/yeixio/boxoffice/internal/store"
)

// booksErr turns the book list's own errors into answers.
func booksErr(err error) error {
	var off books.ErrOff
	var formatOff search.FormatOffError
	switch {
	case errors.As(err, &off), errors.As(err, &formatOff):
		return errorf(http.StatusConflict, err.Error())
	case errors.Is(err, search.ErrNoIndexers):
		return errorf(http.StatusBadRequest, err.Error())
	case errors.Is(err, openlibrary.ErrNotFound):
		return errorf(http.StatusNotFound, err.Error())
	}
	return err
}

func (s *Server) lookupAuthors(w http.ResponseWriter, r *http.Request) error {
	found, err := s.Books.Lookup(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		return booksErr(err)
	}
	return writeJSON(w, http.StatusOK, found)
}

func (s *Server) authors(w http.ResponseWriter, r *http.Request) error {
	as, err := s.Store.Authors(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, as)
}

func (s *Server) addAuthor(w http.ResponseWriter, r *http.Request) error {
	var in books.AddOptions
	if err := readJSON(r, &in); err != nil {
		return err
	}
	a, err := s.Books.Add(r.Context(), in)
	if err != nil {
		if a.ID != 0 {
			// On the list, but the books couldn't be fetched yet; a
			// refresh tries again.
			s.Log.Warn("fetching an author's books", "author", a.Name, "err", err)
			return writeJSON(w, http.StatusCreated, a)
		}
		return booksErr(err)
	}
	return writeJSON(w, http.StatusCreated, a)
}

type authorView struct {
	store.Author
	BookList []bookView `json:"bookList"`
}

type bookView struct {
	store.Book
	Cover string `json:"cover,omitempty"`
}

func viewBook(b store.Book) bookView { return bookView{b, openlibrary.CoverURL(b.CoverID)} }

func (s *Server) writeAuthor(w http.ResponseWriter, r *http.Request, a store.Author) error {
	bs, err := s.Store.Books(r.Context(), a.ID)
	if err != nil {
		return err
	}
	v := authorView{Author: a, BookList: make([]bookView, 0, len(bs))}
	for _, b := range bs {
		v.BookList = append(v.BookList, viewBook(b))
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) author(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.Store.Author(r.Context(), id)
	if err != nil {
		return err
	}
	return s.writeAuthor(w, r, a)
}

func (s *Server) editAuthor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in books.Edit
	if err := readJSON(r, &in); err != nil {
		return err
	}
	a, err := s.Books.Update(r.Context(), id, in)
	if err != nil {
		return booksErr(err)
	}
	return s.writeAuthor(w, r, a)
}

func (s *Server) refreshAuthor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.Books.Refresh(r.Context(), id)
	if err != nil {
		return booksErr(err)
	}
	return s.writeAuthor(w, r, a)
}

// deleteAuthor takes an author off the list, cancelling the downloads for
// their books; files stay where they are.
func (s *Server) deleteAuthor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := s.Store.Books(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAuthor(r.Context(), id); err != nil {
		return err
	}
	ids := make([]int64, len(list))
	for i, b := range list {
		ids[i] = b.ID
	}
	s.cancelDownloads(r.Context(), "book", ids, fmt.Sprintf("author %d", id))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type wantInput struct {
	Format string `json:"format"`
	Wanted *bool  `json:"wanted"`
}

func (in wantInput) check() error {
	if in.Wanted == nil {
		return errorf(http.StatusBadRequest, "say whether it's wanted")
	}
	return nil
}

// wantAllBooks wants, or stops wanting, every book of an author in one
// format.
func (s *Server) wantAllBooks(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in wantInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.check(); err != nil {
		return err
	}
	if err := s.Books.WantAll(r.Context(), id, in.Format, *in.Wanted); err != nil {
		return booksErr(err)
	}
	a, err := s.Store.Author(r.Context(), id)
	if err != nil {
		return err
	}
	return s.writeAuthor(w, r, a)
}

// wantSeries wants, or stops wanting, every book of one of an author's
// series in one format.
func (s *Server) wantSeries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		wantInput
		Series string `json:"series"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.check(); err != nil {
		return err
	}
	if err := s.Books.WantSeries(r.Context(), id, in.Series, in.Format, *in.Wanted); err != nil {
		return booksErr(err)
	}
	a, err := s.Store.Author(r.Context(), id)
	if err != nil {
		return err
	}
	return s.writeAuthor(w, r, a)
}

func (s *Server) wantBook(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in wantInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := in.check(); err != nil {
		return err
	}
	b, err := s.Books.Want(r.Context(), id, in.Format, *in.Wanted)
	if err != nil {
		return booksErr(err)
	}
	return writeJSON(w, http.StatusOK, viewBook(b))
}

// allBooks lists every book on the list, with its author, for picking the
// book a file is for.
func (s *Server) allBooks(w http.ResponseWriter, r *http.Request) error {
	bs, err := s.Store.AllBooks(r.Context())
	if err != nil {
		return err
	}
	out := make([]bookView, 0, len(bs))
	for _, b := range bs {
		out = append(out, viewBook(b))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	b, err := s.Store.Book(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, viewBook(b))
}

// bookReleases searches the indexers for a book in a format, for picking
// a release by hand.
func (s *Server) bookReleases(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.Search.Book(r.Context(), id, r.URL.Query().Get("format"))
	if err != nil {
		return booksErr(err)
	}
	return writeJSON(w, http.StatusOK, res)
}

// searchBook searches for a book in a format in the background, and grabs
// the best release.
func (s *Server) searchBook(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Format string `json:"format"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Books.Search(r.Context(), id, in.Format); err != nil {
		return booksErr(err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}
