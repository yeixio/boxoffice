package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// compressed gzips what's worth it on the way out: JSON, the web UI's
// script, styles and pages, and other text, for browsers that take it.
// Files handed over to save, pictures and partial answers go as they are.
func compressed(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || !takesGzip(r) || r.Header.Get("Range") != "" {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		h.ServeHTTP(gw, r)
	})
}

func takesGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(enc), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			q := strings.ReplaceAll(params, " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}

// compressible says whether a content type shrinks enough to bother.
func compressible(ct string) bool {
	ct, _, _ = strings.Cut(ct, ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch ct {
	case "application/json", "application/javascript", "application/xml", "application/manifest+json", "image/svg+xml", "application/x-nzb":
		return true
	case "text/event-stream":
		return false
	}
	return strings.HasPrefix(ct, "text/")
}

// smallest is the size under which gzip's own overhead isn't worth it.
const smallest = 1024

var gzipPool = sync.Pool{New: func() any {
	z, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return z
}}

// gzipWriter decides once the answer's headers are known. Until the first
// kilobyte is in, it holds what's written, so short answers go plain.
type gzipWriter struct {
	http.ResponseWriter
	status  int
	held    []byte
	decided bool
	z       *gzip.Writer
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	// Interim answers (1xx) go straight out and don't settle anything.
	if code < 200 {
		w.ResponseWriter.WriteHeader(code)
		w.status = 0
	}
}

// wanted says whether the answer, as its headers stand, is one to gzip.
func (w *gzipWriter) wanted() bool {
	h := w.Header()
	if w.status < 200 || w.status == http.StatusNoContent || w.status == http.StatusNotModified || w.status == http.StatusPartialContent {
		return false
	}
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	return compressible(h.Get("Content-Type"))
}

// decide sends the headers, gzipped or plain, and what was held.
func (w *gzipWriter) decide(gz bool) error {
	w.decided = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if gz {
		h := w.Header()
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		// A gzipped answer isn't the bytes its ETag or ranges are for.
		h.Del("Accept-Ranges")
		if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
			h.Set("ETag", "W/"+et)
		}
		w.z = gzipPool.Get().(*gzip.Writer)
		w.z.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
	held := w.held
	w.held = nil
	if len(held) == 0 {
		return nil
	}
	if w.z != nil {
		_, err := w.z.Write(held)
		return err
	}
	_, err := w.ResponseWriter.Write(held)
	return err
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.decided {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(append(w.held, b...)))
		}
		if !w.wanted() {
			if err := w.decide(false); err != nil {
				return 0, err
			}
		} else if len(w.held)+len(b) < smallest {
			w.held = append(w.held, b...)
			return len(b), nil
		} else if err := w.decide(true); err != nil {
			return 0, err
		}
	}
	if w.z != nil {
		return w.z.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush sends what there is so far, so answers that stream keep streaming.
func (w *gzipWriter) Flush() {
	if !w.decided {
		w.decide(w.wanted() && len(w.held) >= smallest)
	}
	if w.z != nil {
		w.z.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// close finishes the answer: short ones held back go out plain.
func (w *gzipWriter) close() {
	if !w.decided {
		if w.status == 0 && len(w.held) == 0 {
			// Nothing was written: the server sends its own empty 200.
			return
		}
		w.decide(false)
	}
	if w.z != nil {
		w.z.Close()
		w.z.Reset(io.Discard)
		gzipPool.Put(w.z)
		w.z = nil
	}
}
