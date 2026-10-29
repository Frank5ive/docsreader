//go:build e2e

// End-to-end UI tests in headless Chrome. Run with:
//
//	go test -tags e2e ./internal/adapters/web
//
// Set DR_TEST_PDF to a PDF path to also run the PDF test.
package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/frank5ive/docsreader/internal/adapters/web"
	"github.com/frank5ive/docsreader/internal/domain"
)

func longEPUB() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var body strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&body, "<p>Paragraph %d. %s</p>", i, strings.Repeat("lorem ipsum dolor sit amet ", 20))
	}
	for n, c := range map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`,
		"c.opf":                  `<package><metadata xmlns:dc="x"><dc:title>E2E Book</dc:title></metadata><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`,
		"a.xhtml":                `<html><body>` + body.String() + `</body></html>`,
	} {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

type ui struct {
	t    *testing.T
	ctx  context.Context
	ts   *httptest.Server
	srv  *web.Server
	book domain.Book
}

// openBook imports content, loads the app in headless Chrome and opens the book.
func openBook(t *testing.T, name string, content []byte, ready string) *ui {
	t.Helper()
	ts, s := newServer(t, t.TempDir())
	do := client(ts, s)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(content)
	mw.Close()
	resp := do("POST", "/api/books", body.Bytes(), mw.FormDataContentType())
	var b domain.Book
	json.NewDecoder(resp.Body).Decode(&b)
	if resp.StatusCode != 201 {
		t.Fatalf("import status %d", resp.StatusCode)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("no-sandbox", true), chromedp.WindowSize(1100, 800))
	a, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	c, cancelC := chromedp.NewContext(a)
	c, cancelT := context.WithTimeout(c, 60*time.Second)
	t.Cleanup(func() { cancelT(); cancelC(); cancelA() })
	chromedp.ListenTarget(c, func(ev any) {
		switch e := ev.(type) {
		case *page.EventJavascriptDialogOpening:
			t.Logf("DIALOG %s: %q", e.Type, e.Message)
		case *runtime.EventExceptionThrown:
			t.Errorf("page exception: %s", e.ExceptionDetails.Exception.Description)
		}
	})
	u := &ui{t: t, ctx: c, ts: ts, srv: s, book: b}
	u.run(
		chromedp.Navigate(ts.URL+"/?t="+s.Token()),
		chromedp.WaitVisible(`#shelf .card`),
		chromedp.Click(`#shelf .card`),
		chromedp.WaitVisible(ready),
		chromedp.Sleep(800*time.Millisecond), // let the restore scroll settle
	)
	return u
}

func (u *ui) run(a ...chromedp.Action) {
	u.t.Helper()
	if err := chromedp.Run(u.ctx, a...); err != nil {
		u.t.Fatal(err)
	}
}

func eval[T any](u *ui, js string) T {
	u.t.Helper()
	var v T
	if err := chromedp.Run(u.ctx, chromedp.Evaluate(js, &v)); err != nil {
		u.t.Fatalf("eval %.70q: %v", js, err)
	}
	return v
}

// locate is JS that maps a character offset in block el to a DOM (node, offset).
const locateJS = `window.locate = (el, off) => {
	const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT); let n, left = off, last = null;
	while ((n = w.nextNode())) { if (!n.length) continue; last = n; if (left <= n.length) return [n, left]; left -= n.length; }
	return [last, last.length];
};`

func (u *ui) annotations() []domain.Annotation {
	var l []domain.Annotation
	json.NewDecoder(client(u.ts, u.srv)("GET", "/api/books/"+u.book.ID+"/annotations", nil, "").Body).Decode(&l)
	return l
}

// selectText selects chars [from,to) of the block data-b=n inside root, as a
// user's drag would, and highlights it through the selection bar.
func (u *ui) highlight(root string, block, from, to int) {
	u.t.Helper()
	eval[any](u, locateJS+fmt.Sprintf(`(() => {
		const el = document.querySelector('%s [data-b="%d"]');
		el.scrollIntoView({block: 'center'});
		const [a, ao] = locate(el, %d), [b, bo] = locate(el, %d);
		const r = document.createRange(); r.setStart(a, ao); r.setEnd(b, bo);
		const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
		document.dispatchEvent(new MouseEvent('mouseup', {bubbles: true}));
	})()`, root, block, from, to))
	u.run(chromedp.WaitVisible(`#selbar`), chromedp.Click(`#hlBtn`), chromedp.Sleep(600*time.Millisecond))
}

// clickAt clicks the on-screen position of character offset `at` in block n.
func (u *ui) clickAt(root string, block, at int) {
	u.t.Helper()
	pt := eval[[]float64](u, locateJS+fmt.Sprintf(`(() => {
		const el = document.querySelector('%s [data-b="%d"]');
		const [a, ao] = locate(el, %d), [b2, bo] = locate(el, %d);
		const r = document.createRange(); r.setStart(a, ao); r.setEnd(b2, bo);
		const b = r.getBoundingClientRect();
		return [b.x + b.width / 2, b.y + b.height / 2];
	})()`, root, block, at, at+1))
	u.run(chromedp.MouseClickXY(pt[0], pt[1]))
}

func TestHighlightLifecycle(t *testing.T) {
	u := openBook(t, "e2e.epub", longEPUB(), `#page [data-b]`)
	u.highlight("#page", 3, 0, 20)
	if n := eval[int](u, `document.querySelectorAll('mark.hl').length`); n != 1 {
		t.Fatalf("want 1 highlight mark, got %d", n)
	}
	bg := eval[string](u, `getComputedStyle(document.querySelector('mark.hl')).backgroundColor`)
	if !strings.HasPrefix(bg, "rgba(") {
		t.Errorf("highlight background %q is opaque; it would hide the text", bg)
	}
	u.run(chromedp.Click(`mark.hl`), chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if n := eval[int](u, `document.querySelectorAll('mark.hl').length`); n != 0 {
		t.Errorf("%d highlight marks remain after Remove", n)
	}
	if l := u.annotations(); len(l) != 0 {
		t.Errorf("%d annotations remain on the server", len(l))
	}
}

// Overlapping highlights must disappear together when removed at the shared
// spot; otherwise "Remove" looks like it did nothing.
func TestOverlappingHighlightsRemoveTogether(t *testing.T) {
	u := openBook(t, "e2e.epub", longEPUB(), `#page [data-b]`)
	u.highlight("#page", 3, 0, 20)
	u.highlight("#page", 3, 10, 30)
	if l := u.annotations(); len(l) != 2 {
		t.Fatalf("want 2 annotations, got %d", len(l))
	}
	u.highlight("#page", 3, 12, 18) // already covered: must not stack another layer
	if l := u.annotations(); len(l) != 2 {
		t.Fatalf("duplicate highlight was stored: %d annotations", len(l))
	}
	u.clickAt("#page", 3, 15)
	u.run(chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if n := eval[int](u, `document.querySelectorAll('mark.hl').length`); n != 0 {
		t.Errorf("%d marks remain after removing the overlap", n)
	}
	if l := u.annotations(); len(l) != 0 {
		t.Errorf("%d annotations remain", len(l))
	}
}

func TestPDFHighlightLifecycle(t *testing.T) {
	p := os.Getenv("DR_TEST_PDF")
	if p == "" {
		t.Skip("DR_TEST_PDF not set")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	u := openBook(t, "doc.pdf", data, `#pdfWrap .textLayer span[data-b]`)
	if n := eval[int](u, `document.querySelectorAll('#pdfWrap canvas').length`); n != 1 {
		t.Fatalf("PDF page canvas not rendered (%d)", n)
	}
	u.highlight("#pdfWrap", 2, 0, 3)
	if n := eval[int](u, `document.querySelectorAll('#pdfWrap mark.hl').length`); n == 0 {
		t.Fatal("no highlight rendered on the PDF text layer")
	}
	u.clickAt("#pdfWrap", 2, 1)
	u.run(chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if n := eval[int](u, `document.querySelectorAll('#pdfWrap mark.hl').length`); n != 0 {
		t.Errorf("%d PDF highlight marks remain after Remove", n)
	}
	if l := u.annotations(); len(l) != 0 {
		t.Errorf("%d annotations remain on the server", len(l))
	}
}
