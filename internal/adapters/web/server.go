// Package web exposes the use cases as a JSON API and serves the embedded UI.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

//go:embed ui
var uiFS embed.FS

const cookieName = "dr_token"

// Server wires HTTP routes to the use cases.
type Server struct {
	Library *app.Library
	Reading *app.Reading
	Notes   *app.Annotate
	Search  *app.Search
	Backup  *app.Backup // optional
	token   string
}

// New creates a Server with a fresh random access token.
func New(l *app.Library, r *app.Reading, n *app.Annotate, q *app.Search) *Server {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Server{Library: l, Reading: r, Notes: n, Search: q, token: hex.EncodeToString(b)}
}

// Token is the per-run secret that must accompany the first request.
func (s *Server) Token() string { return s.token }

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/books", s.listBooks)
	mux.HandleFunc("POST /api/books", s.importBook)
	mux.HandleFunc("GET /api/books/{id}", s.getBook)
	mux.HandleFunc("DELETE /api/books/{id}", s.deleteBook)
	mux.HandleFunc("GET /api/books/{id}/section/{n}", s.section)
	mux.HandleFunc("GET /api/books/{id}/res/{path...}", s.resource)
	mux.HandleFunc("GET /api/books/{id}/cover", s.cover)
	mux.HandleFunc("GET /api/books/{id}/file", s.file)
	mux.HandleFunc("PUT /api/books/{id}/progress", s.saveProgress)
	mux.HandleFunc("POST /api/books/{id}/progress", s.saveProgress) // sendBeacon
	mux.HandleFunc("POST /api/books/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /api/books/{id}/history", s.history)
	mux.HandleFunc("GET /api/books/{id}/annotations", s.listNotes)
	mux.HandleFunc("POST /api/books/{id}/annotations", s.addNote)
	mux.HandleFunc("PATCH /api/books/{id}/annotations/{aid}", s.editNote)
	mux.HandleFunc("DELETE /api/books/{id}/annotations/{aid}", s.deleteNote)
	mux.HandleFunc("GET /api/books/{id}/export.md", s.exportNotes)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("PUT /api/books/{id}/tags", s.setTags)
	mux.HandleFunc("GET /api/backup", s.backup)
	mux.HandleFunc("GET /api/stats/books", s.bookTimes)
	mux.HandleFunc("GET /api/stats", s.stats)
	ui, _ := fs.Sub(uiFS, "ui")
	mux.Handle("/", http.FileServerFS(ui))
	return s.guard(mux)
}

// guard rejects foreign Host headers (DNS rebinding) and requests without the
// per-run token. A valid ?t= token sets an HttpOnly cookie and redirects.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && s.valid(t) {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !s.valid(c.Value) {
			http.Error(w, "unauthorized: open the URL printed at startup", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) valid(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, app.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, app.ErrUnsupported):
		status = http.StatusUnsupportedMediaType
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type bookDTO struct {
	domain.Book
	Progress *domain.Anchor `json:"progress,omitempty"`
	LastRead *time.Time     `json:"lastRead,omitempty"`
}

func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	books, err := s.Library.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	prog, err := s.Reading.AllProgress(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]bookDTO, 0, len(books))
	for _, b := range books {
		dto := bookDTO{Book: b}
		if p, ok := prog[b.ID]; ok {
			a := p.Anchor
			dto.Progress = &a
			t := p.UpdatedAt
			dto.LastRead = &t
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) importBook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file"})
		return
	}
	defer file.Close()
	b, err := s.Library.ImportReader(r.Context(), hdr.Filename, file)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
	b, p, err := s.Reading.Open(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	doc, err := s.Reading.Document(r.Context(), b.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": b, "progress": p.Anchor, "hasProgress": !p.UpdatedAt.IsZero(), "updatedAt": p.UpdatedAt, "toc": doc.TOC(), "sectionWords": doc.SectionWords()})
}

func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) {
	if err := s.Library.Delete(r.Context(), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) section(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, err := s.Reading.Document(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	h, err := doc.Section(n)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": h})
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	doc, err := s.Reading.Document(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	data, mt, err := doc.Resource(r.PathValue("path"))
	if err != nil {
		fail(w, err)
		return
	}
	serveBytes(w, data, mt)
}

func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	doc, err := s.Reading.Document(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	data, mt, err := doc.Cover()
	if err != nil {
		fail(w, err)
		return
	}
	serveBytes(w, data, mt)
}

// serveBytes sends embedded book content. nosniff plus a sandboxing CSP keep
// SVG and HTML resources from running script in the app origin.
func serveBytes(w http.ResponseWriter, data []byte, mt string) {
	if strings.Contains(mt, "html") || strings.Contains(mt, "xml") && !strings.Contains(mt, "svg") {
		mt = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	b, _, err := s.Reading.Open(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if b.Format != domain.FormatPDF {
		fail(w, app.ErrUnsupported)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	http.ServeFile(w, r, b.Path)
}

func (s *Server) saveProgress(w http.ResponseWriter, r *http.Request) {
	var a domain.Anchor
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&a); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad anchor"})
		return
	}
	if err := s.Reading.SaveProgress(r.Context(), r.PathValue("id"), a); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct{ Seconds, Words int }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := s.Reading.Heartbeat(r.Context(), r.PathValue("id"), in.Seconds, in.Words); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Reading.Stats(r.Context(), r.URL.Query().Get("book"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	h, err := s.Reading.History(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		fail(w, err)
		return
	}
	if h == nil {
		h = []domain.Session{}
	}
	writeJSON(w, http.StatusOK, h)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	l, err := s.Notes.List(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) addNote(w http.ResponseWriter, r *http.Request) {
	var a domain.Annotation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<17)).Decode(&a); err != nil {
		badRequest(w, "bad annotation")
		return
	}
	a.BookID = r.PathValue("id")
	out, err := s.Notes.Add(r.Context(), a)
	switch {
	case errors.Is(err, app.ErrNotFound):
		fail(w, err)
	case err != nil:
		badRequest(w, err.Error())
	default:
		writeJSON(w, http.StatusCreated, out)
	}
}

func (s *Server) editNote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("aid"), 10, 64)
	var in struct{ Note string }
	if err != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<17)).Decode(&in) != nil {
		badRequest(w, "bad request")
		return
	}
	if err := s.Notes.UpdateNote(r.Context(), r.PathValue("id"), id, in.Note); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("aid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Notes.Delete(r.Context(), r.PathValue("id"), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) exportNotes(w http.ResponseWriter, r *http.Request) {
	md, err := s.Notes.ExportMarkdown(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="notes.md"`)
	_, _ = w.Write([]byte(md))
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	hits, err := s.Search.Find(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("book"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func (s *Server) bookTimes(w http.ResponseWriter, r *http.Request) {
	t, err := s.Reading.BookTimes(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) setTags(w http.ResponseWriter, r *http.Request) {
	var in struct{ Tags []string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil {
		badRequest(w, "bad tags")
		return
	}
	tags, err := s.Library.SetTags(r.Context(), r.PathValue("id"), in.Tags)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	if s.Backup == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="docsreader-backup-`+time.Now().Format("2006-01-02")+`.zip"`)
	if err := s.Backup.WriteZip(r.Context(), w); err != nil {
		// Headers are already sent; the truncated archive fails to open, which is the right signal.
		log.Printf("backup: %v", err)
	}
}
