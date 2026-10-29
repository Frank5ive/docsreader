package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frank5ive/docsreader/internal/adapters/format/epub"
	"github.com/frank5ive/docsreader/internal/adapters/format/pdf"
	"github.com/frank5ive/docsreader/internal/adapters/platform"
	"github.com/frank5ive/docsreader/internal/adapters/storage/sqlite"
	"github.com/frank5ive/docsreader/internal/adapters/web"
	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

func epubBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`,
		"c.opf":                  `<package><metadata xmlns:dc="x"><dc:title>T</dc:title></metadata><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`,
		"a.xhtml":                `<html><body><p>one</p><p>two</p></body></html>`,
	} {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func newServer(t *testing.T, dir string) (*httptest.Server, *web.Server) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	readers := map[domain.Format]app.FormatReader{domain.FormatEPUB: epub.Reader{}, domain.FormatPDF: pdf.Reader{}}
	lib := &app.Library{Books: st, Files: platform.FileStore{Dir: dir}, Readers: readers, Now: time.Now}
	rd := &app.Reading{Books: st, Progress: st.Progress(), Sessions: st, Readers: readers, Now: time.Now}
	lib.Index = st.Search()
	t.Cleanup(func() { rd.Close() })
	s := web.New(lib, rd, &app.Annotate{Books: st, Anns: st.Annotations(), Now: time.Now}, &app.Search{Index: st.Search()})
	s.Backup = &app.Backup{DB: st, BooksDir: dir, TempDir: dir}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func client(ts *httptest.Server, s *web.Server) func(method, path string, body []byte, ctype string) *http.Response {
	return func(method, path string, body []byte, ctype string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "dr_token", Value: s.Token()})
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		return resp
	}
}

func TestAuthRequired(t *testing.T) {
	ts, _ := newServer(t, t.TempDir())
	resp, _ := http.Get(ts.URL + "/api/books")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/books", nil)
	req.Host = "evil.example"
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding status = %d", resp.StatusCode)
	}
}

// TestResumeAcrossRestart imports a book, saves a position, reopens the
// database from scratch and expects the same anchor back.
func TestResumeAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ts, s := newServer(t, dir)
	do := client(ts, s)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "t.epub")
	fw.Write(epubBytes(t))
	mw.Close()
	resp := do("POST", "/api/books", body.Bytes(), mw.FormDataContentType())
	var b domain.Book
	json.NewDecoder(resp.Body).Decode(&b)
	if resp.StatusCode != 201 || b.Title != "T" {
		t.Fatalf("import: %d %+v", resp.StatusCode, b)
	}

	a := `{"section":0,"block":1,"offset":0,"percent":0.5,"quote":"two"}`
	if r := do("PUT", "/api/books/"+b.ID+"/progress", []byte(a), "application/json"); r.StatusCode != 204 {
		t.Fatalf("save progress: %d", r.StatusCode)
	}
	if r := do("PUT", "/api/books/"+b.ID+"/progress", []byte(`{"percent":2}`), "application/json"); r.StatusCode != 400 {
		t.Fatalf("invalid anchor accepted: %d", r.StatusCode)
	}

	ts2, s2 := newServer(t, dir) // "restart": new store over the same files
	r := client(ts2, s2)("GET", "/api/books/"+b.ID, nil, "")
	var got struct {
		Progress    domain.Anchor
		HasProgress bool
	}
	json.NewDecoder(r.Body).Decode(&got)
	if !got.HasProgress || got.Progress.Block != 1 || got.Progress.Quote != "two" {
		t.Fatalf("progress after restart = %+v", got)
	}

	if r := client(ts2, s2)("POST", "/api/books/"+b.ID+"/heartbeat", []byte(`{"Seconds":60,"Words":300}`), "application/json"); r.StatusCode != 204 {
		t.Fatalf("heartbeat: %d", r.StatusCode)
	}
	var hist []domain.Session
	hr := client(ts2, s2)("GET", "/api/books/"+b.ID+"/history", nil, "")
	json.NewDecoder(hr.Body).Decode(&hist)
	if len(hist) != 1 || hist[0].Seconds != 60 || hist[0].Words != 300 {
		t.Fatalf("history = %+v", hist)
	}

	sec := client(ts2, s2)("GET", "/api/books/"+b.ID+"/section/0", nil, "")
	var sj map[string]string
	json.NewDecoder(sec.Body).Decode(&sj)
	if !strings.Contains(sj["html"], `data-b="1"`) {
		t.Fatalf("section html = %q", sj["html"])
	}
}

func TestAnnotationsAndSearch(t *testing.T) {
	ts, s := newServer(t, t.TempDir())
	do := client(ts, s)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "t.epub")
	fw.Write(epubBytes(t))
	mw.Close()
	var b domain.Book
	json.NewDecoder(do("POST", "/api/books", body.Bytes(), mw.FormDataContentType()).Body).Decode(&b)

	// Highlight with an invalid empty body is rejected; a real one is stored.
	if r := do("POST", "/api/books/"+b.ID+"/annotations", []byte(`{"kind":"highlight"}`), "application/json"); r.StatusCode != 400 {
		t.Fatalf("empty highlight: %d", r.StatusCode)
	}
	hl := `{"kind":"highlight","start":{"section":0,"block":1,"offset":0,"percent":0.5},"endBlock":1,"endOffset":3,"quote":"two","color":"yellow"}`
	r := do("POST", "/api/books/"+b.ID+"/annotations", []byte(hl), "application/json")
	var a domain.Annotation
	json.NewDecoder(r.Body).Decode(&a)
	if r.StatusCode != 201 || a.ID == 0 {
		t.Fatalf("add: %d %+v", r.StatusCode, a)
	}
	if r := do("PATCH", "/api/books/"+b.ID+"/annotations/"+strconv.FormatInt(a.ID, 10), []byte(`{"Note":"hello"}`), "application/json"); r.StatusCode != 204 {
		t.Fatalf("edit: %d", r.StatusCode)
	}
	var list []domain.Annotation
	json.NewDecoder(do("GET", "/api/books/"+b.ID+"/annotations", nil, "").Body).Decode(&list)
	if len(list) != 1 || list[0].Note != "hello" || list[0].Start.Block != 1 {
		t.Fatalf("list = %+v", list)
	}
	md, _ := io.ReadAll(do("GET", "/api/books/"+b.ID+"/export.md", nil, "").Body)
	if !strings.Contains(string(md), "> two") || !strings.Contains(string(md), "hello") {
		t.Fatalf("export = %q", md)
	}

	// Search: prefix match, block coordinates, and hostile input stays harmless.
	var hits []app.Hit
	json.NewDecoder(do("GET", "/api/search?q=tw", nil, "").Body).Decode(&hits)
	if len(hits) != 1 || hits[0].Block != 1 || !strings.Contains(hits[0].Snippet, "<mark>two</mark>") {
		t.Fatalf("hits = %+v", hits)
	}
	hits = nil
	json.NewDecoder(do("GET", "/api/search?q=twos", nil, "").Body).Decode(&hits)
	if len(hits) != 1 {
		t.Fatalf("stemmed query found %d hits", len(hits))
	}
	if r := do("GET", "/api/search?q="+url.QueryEscape(`"AND OR * ( NEAR`), nil, ""); r.StatusCode != 200 {
		t.Fatalf("hostile query: %d", r.StatusCode)
	}

	if r := do("DELETE", "/api/books/"+b.ID+"/annotations/"+strconv.FormatInt(a.ID, 10), nil, ""); r.StatusCode != 204 {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if r := do("DELETE", "/api/books/"+b.ID, nil, ""); r.StatusCode != 204 {
		t.Fatalf("delete book: %d", r.StatusCode)
	}
	json.NewDecoder(do("GET", "/api/search?q=two", nil, "").Body).Decode(&hits)
	if len(hits) != 0 {
		t.Fatalf("index not cleared: %+v", hits)
	}
}

func importEPUB(t *testing.T, do func(method, path string, body []byte, ctype string) *http.Response) domain.Book {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "t.epub")
	fw.Write(epubBytes(t))
	mw.Close()
	var b domain.Book
	json.NewDecoder(do("POST", "/api/books", body.Bytes(), mw.FormDataContentType()).Body).Decode(&b)
	return b
}

func TestTags(t *testing.T) {
	ts, s := newServer(t, t.TempDir())
	do := client(ts, s)
	b := importEPUB(t, do)

	r := do("PUT", "/api/books/"+b.ID+"/tags", []byte(`{"Tags":["  Sci-Fi ","sci-fi","To   Read","",  "`+strings.Repeat("x", 100)+`"]}`), "application/json")
	var got []string
	json.NewDecoder(r.Body).Decode(&got)
	if r.StatusCode != 200 || len(got) != 3 || got[0] != "Sci-Fi" || got[1] != "To Read" || len(got[2]) != 40 {
		t.Fatalf("normalised tags = %q (status %d)", got, r.StatusCode)
	}
	var list []struct{ Tags []string }
	json.NewDecoder(do("GET", "/api/books", nil, "").Body).Decode(&list)
	if len(list) != 1 || len(list[0].Tags) != 3 {
		t.Fatalf("list tags = %+v", list)
	}
	if r := do("PUT", "/api/books/nope/tags", []byte(`{"Tags":["a"]}`), "application/json"); r.StatusCode != 404 {
		t.Errorf("unknown book: %d", r.StatusCode)
	}
	do("PUT", "/api/books/"+b.ID+"/tags", []byte(`{"Tags":[]}`), "application/json")
	json.NewDecoder(do("GET", "/api/books", nil, "").Body).Decode(&list)
	if len(list[0].Tags) != 0 {
		t.Errorf("tags not cleared: %+v", list[0].Tags)
	}
}

// TestBackupRestoreRoundTrip downloads a backup, restores it into a different
// directory (so stored absolute paths are wrong) and expects the library,
// reading position and annotations to come back with a readable book.
func TestBackupRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, app.DBName))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "books"), 0o700)
	readers := map[domain.Format]app.FormatReader{domain.FormatEPUB: epub.Reader{}}
	lib := &app.Library{Books: st, Files: platform.FileStore{Dir: filepath.Join(dir, "books")}, Readers: readers, Index: st.Search(), Now: time.Now}
	rd := &app.Reading{Books: st, Progress: st.Progress(), Sessions: st, Readers: readers, Now: time.Now}
	t.Cleanup(func() { rd.Close() })
	srv := web.New(lib, rd, &app.Annotate{Books: st, Anns: st.Annotations(), Now: time.Now}, &app.Search{Index: st.Search()})
	srv.Backup = &app.Backup{DB: st, BooksDir: filepath.Join(dir, "books"), TempDir: dir}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	do := client(ts, srv)
	b := importEPUB(t, do)
	do("PUT", "/api/books/"+b.ID+"/progress", []byte(`{"section":0,"block":1,"offset":0,"percent":0.5,"quote":"two"}`), "application/json")
	do("POST", "/api/books/"+b.ID+"/annotations", []byte(`{"kind":"bookmark","start":{"section":0,"block":1,"percent":0.5}}`), "application/json")
	do("PUT", "/api/books/"+b.ID+"/tags", []byte(`{"Tags":["keep"]}`), "application/json")

	resp := do("GET", "/api/backup", nil, "")
	zipPath := filepath.Join(t.TempDir(), "backup.zip")
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("backup: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	os.WriteFile(zipPath, data, 0o600)
	st.Close()

	newDir := t.TempDir()
	if err := app.Restore(zipPath, newDir); err != nil {
		t.Fatal(err)
	}
	st2, err := sqlite.Open(filepath.Join(newDir, app.DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	lib2 := &app.Library{Books: st2, Readers: readers, Now: time.Now}
	if err := lib2.RepairPaths(context.Background(), filepath.Join(newDir, "books")); err != nil {
		t.Fatal(err)
	}
	got, err := st2.Get(context.Background(), b.ID)
	if err != nil || !strings.HasPrefix(got.Path, newDir) || len(got.Tags) != 1 || got.Tags[0] != "keep" {
		t.Fatalf("restored book = %+v, %v", got, err)
	}
	if doc, err := readers[domain.FormatEPUB].Open(got.Path); err != nil {
		t.Fatalf("restored book not readable: %v", err)
	} else {
		doc.Close()
	}
	p, err := st2.Progress().Get(context.Background(), b.ID)
	if err != nil || p.Anchor.Block != 1 || p.Anchor.Quote != "two" {
		t.Fatalf("restored progress = %+v, %v", p, err)
	}
	if l, _ := st2.Annotations().List(context.Background(), b.ID); len(l) != 1 {
		t.Fatalf("restored annotations = %d", len(l))
	}
}

func TestRestoreRejectsHostileArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, n := range []string{app.DBName, "../escape.txt", "books/../../escape2.txt", "books/sub/x.epub"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	f.Close()
	dir := t.TempDir()
	if err := app.Restore(p, dir); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(dir, "..", "escape.txt"), filepath.Join(dir, "..", "..", "escape2.txt"), filepath.Join(dir, "books", "sub")} {
		if _, err := os.Stat(bad); err == nil {
			t.Errorf("hostile entry was extracted: %s", bad)
		}
	}
	if err := app.Restore(filepath.Join(t.TempDir(), "missing.zip"), dir); err == nil {
		t.Error("missing archive accepted")
	}
}
