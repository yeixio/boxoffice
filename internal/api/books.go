package api

import (
	"errors"
	"net/http"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/openlibrary"
	"github.com/yeixio/boxoffice/internal/store"
)

// booksErr turns the book list's own errors into answers.
func booksErr(err error) error {
	var off books.ErrOff
	switch {
	case errors.As(err, &off):
		return errorf(http.StatusConflict, err.Error())
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

// deleteAuthor takes an author off the list; files stay where they are.
func (s *Server) deleteAuthor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAuthor(r.Context(), id); err != nil {
		return err
	}
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
