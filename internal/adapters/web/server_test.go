package web_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	s := web.New(lib, rd, &app.Annotate{Books: st, Anns: st.Annotations(), Now: time.Now}, &app.Search{Index: st.Search()})
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
