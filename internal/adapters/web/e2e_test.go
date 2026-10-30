//go:build e2e

// End-to-end UI tests in headless Chrome. Run with:
//
//	go test -tags e2e ./internal/adapters/web
package web_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/frank5ive/docsreader/internal/adapters/web"
	"github.com/frank5ive/docsreader/internal/domain"
)

const (
	epubReady = `#page [data-b]`
	pdfReady  = `#pdfWrap .pdfPage canvas`
)

type ui struct {
	t   *testing.T
	ctx context.Context
	ts  *httptest.Server
	srv *web.Server
	dir string
}

// start runs the app and opens it in a fresh headless Chrome on the library page.
func start(t *testing.T) *ui {
	t.Helper()
	dir := t.TempDir()
	ts, s := newServer(t, dir)
	// A cold Chrome on a CI runner can take longer than chromedp's default 20s to
	// open its DevTools socket; /dev/shm is also tiny there.
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("disable-gpu", true),
		chromedp.WSURLReadTimeout(90*time.Second), chromedp.WindowSize(1100, 800))
	a, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	c, cancelC := chromedp.NewContext(a)
	c, cancelT := context.WithTimeout(c, 120*time.Second)
	t.Cleanup(func() { cancelT(); cancelC(); cancelA() })
	u := &ui{t: t, ctx: c, ts: ts, srv: s, dir: dir}
	chromedp.ListenTarget(c, func(ev any) {
		switch e := ev.(type) {
		case *page.EventJavascriptDialogOpening:
			// Accept confirm() prompts (e.g. "Remove book?").
			go chromedp.Run(c, chromedp.ActionFunc(func(ctx context.Context) error {
				return page.HandleJavaScriptDialog(true).Do(ctx)
			}))
		case *runtime.EventExceptionThrown:
			t.Errorf("page exception: %s", e.ExceptionDetails.Exception.Description)
		}
	})
	u.run(chromedp.Navigate(ts.URL+"/?t="+s.Token()), chromedp.WaitVisible(`#libSearch`, chromedp.ByQuery))
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
	if err := chromedp.Run(u.ctx, chromedp.Evaluate(js, &v, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		u.t.Fatalf("eval %.80q: %v", js, err)
	}
	return v
}

func (u *ui) js(js string) { u.t.Helper(); eval[any](u, js) }

// importAPI adds a book through the HTTP API and returns it.
func (u *ui) importAPI(name string, content []byte) domain.Book {
	u.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(content)
	mw.Close()
	resp := client(u.ts, u.srv)("POST", "/api/books", body.Bytes(), mw.FormDataContentType())
	var b domain.Book
	json.NewDecoder(resp.Body).Decode(&b)
	if resp.StatusCode != 201 {
		u.t.Fatalf("import %s: status %d", name, resp.StatusCode)
	}
	return b
}

func (u *ui) api(method, path string, body string) []byte {
	u.t.Helper()
	resp := client(u.ts, u.srv)(method, path, []byte(body), "application/json")
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes()
}

func (u *ui) annotations(id string) []domain.Annotation {
	var l []domain.Annotation
	json.Unmarshal(u.api("GET", "/api/books/"+id+"/annotations", ""), &l)
	return l
}

func (u *ui) progress(id string) domain.Anchor {
	var out struct {
		Progress    domain.Anchor
		HasProgress bool
	}
	json.Unmarshal(u.api("GET", "/api/books/"+id, ""), &out)
	return out.Progress
}

// library reloads the page and shows the library.
func (u *ui) library() {
	u.t.Helper()
	u.run(chromedp.Navigate(u.ts.URL+"/"), chromedp.WaitVisible(`#libSearch`, chromedp.ByQuery), chromedp.Sleep(500*time.Millisecond))
}

// open clicks the shelf card whose title contains title and waits for ready.
func (u *ui) open(title, ready string) {
	u.t.Helper()
	u.js(fmt.Sprintf(`[...document.querySelectorAll('#shelf .card')].find((c) => c.textContent.includes(%q)).click()`, title))
	u.run(chromedp.WaitVisible(ready), chromedp.Sleep(800*time.Millisecond)) // let the restore scroll settle
}

// home returns to the library through the UI (which awaits the progress save).
func (u *ui) home() {
	u.t.Helper()
	u.run(chromedp.Click(`#home`), chromedp.WaitVisible(`#libSearch`, chromedp.ByQuery), chromedp.Sleep(600*time.Millisecond))
}

// waitText waits until the element's text contains want.
func (u *ui) waitText(sel, want string) {
	u.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(u.text(sel), want) {
		if time.Now().After(deadline) {
			u.t.Fatalf("timed out waiting for %q to contain %q (has %q)", sel, want, u.text(sel))
		}
		u.run(chromedp.Sleep(150 * time.Millisecond))
	}
}

func (u *ui) count(sel string) int {
	return eval[int](u, fmt.Sprintf(`document.querySelectorAll(%q).length`, sel))
}

func (u *ui) text(sel string) string {
	return eval[string](u, fmt.Sprintf(`(document.querySelector(%q) || {textContent: ''}).textContent`, sel))
}

// topBlock is the data-b of the first EPUB paragraph visible under the header.
func (u *ui) topBlock() int {
	return eval[int](u, `(() => { for (const el of document.querySelectorAll('#page [data-b]')) if (el.getBoundingClientRect().bottom > 90) return +el.dataset.b; return -1; })()`)
}

// scrollToBlock brings block n of the EPUB to the top and waits for the debounced save.
func (u *ui) scrollToBlock(n int) {
	u.t.Helper()
	u.js(fmt.Sprintf(`(() => { const el = document.querySelector('#page [data-b="%d"]'); scrollTo(0, el.getBoundingClientRect().top + scrollY - 80); })()`, n))
	u.run(chromedp.Sleep(1500 * time.Millisecond))
}

const locateJS = `window.locate = (el, off) => {
	const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT); let n, left = off, last = null;
	while ((n = w.nextNode())) { if (!n.length) continue; last = n; if (left <= n.length) return [n, left]; left -= n.length; }
	return [last, last.length];
};`

// selectText selects characters [from,to) of block n under root like a mouse drag.
func (u *ui) selectText(root string, block, from, to int) {
	u.t.Helper()
	u.js(locateJS + fmt.Sprintf(`(() => {
		const el = document.querySelector('%s [data-b="%d"]');
		el.scrollIntoView({block: 'center'});
		const [a, ao] = locate(el, %d), [b, bo] = locate(el, %d);
		const r = document.createRange(); r.setStart(a, ao); r.setEnd(b, bo);
		const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
		document.dispatchEvent(new MouseEvent('mouseup', {bubbles: true}));
	})()`, root, block, from, to))
	u.run(chromedp.WaitVisible(`#selbar`))
}

func (u *ui) highlight(root string, block, from, to int) {
	u.t.Helper()
	u.selectText(root, block, from, to)
	u.run(chromedp.Click(`#hlBtn`), chromedp.Sleep(600*time.Millisecond))
}

// clickAt clicks the on-screen position of character offset `at` in block n.
func (u *ui) clickAt(root string, block, at int) {
	u.t.Helper()
	pt := eval[[]float64](u, locateJS+fmt.Sprintf(`(() => {
		const el = document.querySelector('%s [data-b="%d"]');
		const [a, ao] = locate(el, %d), [b, bo] = locate(el, %d);
		const r = document.createRange(); r.setStart(a, ao); r.setEnd(b, bo);
		const rc = r.getBoundingClientRect();
		return [rc.x + rc.width / 2, rc.y + rc.height / 2];
	})()`, root, block, at, at+1))
	u.run(chromedp.MouseClickXY(pt[0], pt[1]))
}

func (u *ui) setRange(id string, v int) {
	u.js(fmt.Sprintf(`(() => { const e = document.getElementById(%q); e.value = %d; e.dispatchEvent(new Event('input', {bubbles: true})); })()`, id, v))
}

func pdfPages() []string {
	return []string{"First page here\nalpha line", "Second page here\nbeta line", "Third page here\nGiraffe lives here", "Fourth page here\ndelta line", "Fifth page here\nepsilon line"}
}

// ---------------------------------------------------------------- library

func TestLibraryUploadAndDelete(t *testing.T) {
	u := start(t)
	dir := t.TempDir()
	ep := filepath.Join(dir, "up.epub")
	pf := filepath.Join(dir, "up.pdf")
	os.WriteFile(ep, buildEPUB("Uploaded Book", 1, 3, nil), 0o600)
	os.WriteFile(pf, buildPDF(pdfPages()), 0o600)
	u.run(chromedp.SetUploadFiles(`#upload`, []string{ep, pf}, chromedp.ByQuery), chromedp.Sleep(1500*time.Millisecond))
	if n := u.count(`#shelf .card`); n != 2 {
		t.Fatalf("want 2 books on the shelf after upload, got %d", n)
	}
	if !strings.Contains(u.text(`#shelf`), "Uploaded Book") {
		t.Errorf("EPUB title missing from shelf: %q", u.text(`#shelf`))
	}
	// Importing identical content again must not create a duplicate.
	u.run(chromedp.SetUploadFiles(`#upload`, []string{ep}, chromedp.ByQuery), chromedp.Sleep(1000*time.Millisecond))
	if n := u.count(`#shelf .card`); n != 2 {
		t.Errorf("duplicate import created a card: %d", n)
	}
	u.js(`document.querySelector('#shelf .card .del').click()`) // confirm() is auto-accepted
	u.run(chromedp.Sleep(1000 * time.Millisecond))
	if n := u.count(`#shelf .card`); n != 1 {
		t.Errorf("want 1 book after delete, got %d", n)
	}
}

// ---------------------------------------------------------------- resume

func TestResumeEPUB(t *testing.T) {
	u := start(t)
	b := u.importAPI("long.epub", buildEPUB("Long Book", 3, 40, nil))
	u.library()
	u.open("Long Book", epubReady)
	u.scrollToBlock(25)
	want := u.topBlock()
	if want < 24 || want > 26 {
		t.Fatalf("test setup: top block %d, wanted about 25", want)
	}
	u.home()
	if u.count(`#continue .hero`) != 1 {
		t.Fatal("no Continue reading card after leaving a book mid-way")
	}
	if p := u.progress(b.ID); p.Block < 24 || p.Block > 26 {
		t.Errorf("saved block = %d, want about 25", p.Block)
	}
	u.run(chromedp.Click(`#continue .hero`), chromedp.WaitVisible(epubReady), chromedp.Sleep(800*time.Millisecond))
	if got := u.topBlock(); got != want {
		t.Errorf("resumed at block %d, left at %d", got, want)
	}
	// Changing typography reflows the page; the position must be kept.
	u.setRange("size", 28)
	u.run(chromedp.Sleep(800 * time.Millisecond))
	if got := u.topBlock(); got < want-1 || got > want+1 {
		t.Errorf("after font change top block = %d, want about %d", got, want)
	}
}

func TestResumePDF(t *testing.T) {
	u := start(t)
	b := u.importAPI("doc.pdf", buildPDF(pdfPages()))
	u.library()
	u.open("doc", pdfReady)
	u.run(chromedp.Click(`#next`), chromedp.Sleep(700*time.Millisecond), chromedp.Click(`#next`), chromedp.Sleep(900*time.Millisecond))
	if got := u.text(`#pct`); got != "p.3/5" {
		t.Fatalf("after two next clicks page indicator = %q", got)
	}
	u.home()
	if p := u.progress(b.ID); p.Section != 2 {
		t.Errorf("saved PDF page = %d, want 2 (page 3)", p.Section)
	}
	u.open("doc", pdfReady)
	if got := u.text(`#pct`); got != "p.3/5" {
		t.Errorf("reopened PDF on %q, want p.3/5", got)
	}
	// Resume must also survive a full reload, as after restarting the app.
	u.library()
	u.open("doc", pdfReady)
	if got := u.text(`#pct`); got != "p.3/5" {
		t.Errorf("after reload PDF on %q, want p.3/5", got)
	}
}

// ---------------------------------------------------------------- bookmarks

func TestBookmarksEPUB(t *testing.T) {
	u := start(t)
	b := u.importAPI("bm.epub", buildEPUB("Bookmark Book", 1, 40, nil))
	u.library()
	u.open("Bookmark Book", epubReady)
	u.scrollToBlock(20)
	u.run(chromedp.Click(`#markBtn`), chromedp.Sleep(600*time.Millisecond))
	l := u.annotations(b.ID)
	if len(l) != 1 || l[0].Kind != domain.KindBookmark {
		t.Fatalf("want one bookmark, got %+v", l)
	}
	if u.count(`#markBtn.on`) != 1 {
		t.Error("bookmark button not shown as active at a bookmarked spot")
	}
	u.scrollToBlock(2)
	if u.count(`#markBtn.on`) != 0 {
		t.Error("bookmark button still active elsewhere")
	}
	u.run(chromedp.Click(`#notesBtn`), chromedp.WaitVisible(`#notes .ann`))
	u.run(chromedp.Click(`#notes .ann q`), chromedp.Sleep(900*time.Millisecond))
	if got := u.topBlock(); got < l[0].Start.Block-1 || got > l[0].Start.Block+1 {
		t.Errorf("jump from Notes landed on block %d, bookmark is at %d", got, l[0].Start.Block)
	}
	u.run(chromedp.Click(`#markBtn`), chromedp.Sleep(600*time.Millisecond)) // toggles off
	if l := u.annotations(b.ID); len(l) != 0 {
		t.Errorf("bookmark not removed by second press: %+v", l)
	}
}

func TestBookmarksPDF(t *testing.T) {
	u := start(t)
	b := u.importAPI("bm.pdf", buildPDF(pdfPages()))
	u.library()
	u.open("bm", pdfReady)
	u.run(chromedp.Click(`#next`), chromedp.Sleep(700*time.Millisecond), chromedp.Click(`#next`), chromedp.Sleep(900*time.Millisecond))
	u.run(chromedp.Click(`#markBtn`), chromedp.Sleep(600*time.Millisecond))
	l := u.annotations(b.ID)
	if len(l) != 1 || l[0].Start.Section != 2 {
		t.Fatalf("want one bookmark on page 3, got %+v", l)
	}
	u.run(chromedp.Click(`#prev`), chromedp.Sleep(700*time.Millisecond), chromedp.Click(`#prev`), chromedp.Sleep(900*time.Millisecond))
	if got := u.text(`#pct`); got != "p.1/5" {
		t.Fatalf("setup: on %q", got)
	}
	u.run(chromedp.Click(`#notesBtn`), chromedp.WaitVisible(`#notes .ann`), chromedp.Click(`#notes .ann q`), chromedp.Sleep(1200*time.Millisecond))
	if got := u.text(`#pct`); got != "p.3/5" {
		t.Errorf("jump to bookmark landed on %q, want p.3/5", got)
	}
}

// ---------------------------------------------------------------- notes

func TestNotesEPUB(t *testing.T) {
	u := start(t)
	b := u.importAPI("n.epub", buildEPUB("Notes Book", 1, 20, nil))
	u.library()
	u.open("Notes Book", epubReady)
	u.selectText("#page", 4, 0, 24)
	u.run(chromedp.Click(`#noteBtn`), chromedp.WaitVisible(`#noteText`), chromedp.SendKeys(`#noteText`, "remember this"), chromedp.Click(`#noteSave`), chromedp.Sleep(800*time.Millisecond))
	l := u.annotations(b.ID)
	if len(l) != 1 || l[0].Kind != domain.KindNote || l[0].Note != "remember this" {
		t.Fatalf("want one note, got %+v", l)
	}
	if u.count(`mark.hl.note`) == 0 {
		t.Error("note is not drawn on the page")
	}
	// Edit from the panel.
	u.run(chromedp.Click(`#notesBtn`), chromedp.WaitVisible(`#notes textarea`), chromedp.SendKeys(`#notes textarea`, " twice"), chromedp.Click(`#notes .acts button`), chromedp.Sleep(700*time.Millisecond))
	if l := u.annotations(b.ID); len(l) != 1 || l[0].Note != "remember this twice" {
		t.Errorf("edited note not saved: %+v", l)
	}
	// Markdown export carries the quote and the note.
	md := string(u.api("GET", "/api/books/"+b.ID+"/export.md", ""))
	if !strings.Contains(md, "remember this twice") || !strings.Contains(md, "Paragraph 1-3") {
		t.Errorf("export missing content:\n%s", md)
	}
	// Delete from the panel.
	u.run(chromedp.Click(`#notes .acts button:last-child`), chromedp.Sleep(700*time.Millisecond))
	if l := u.annotations(b.ID); len(l) != 0 {
		t.Errorf("note not deleted: %+v", l)
	}
	if u.count(`mark.hl`) != 0 {
		t.Error("deleted note still drawn")
	}
}

func TestNotesPDF(t *testing.T) {
	u := start(t)
	b := u.importAPI("n.pdf", buildPDF(pdfPages()))
	u.library()
	u.open("n", `#pdfWrap .textLayer span[data-b]`)
	u.selectText("#pdfWrap", 0, 0, 5)
	u.run(chromedp.Click(`#noteBtn`), chromedp.WaitVisible(`#noteText`), chromedp.SendKeys(`#noteText`, "pdf note"), chromedp.Click(`#noteSave`), chromedp.Sleep(900*time.Millisecond))
	l := u.annotations(b.ID)
	if len(l) != 1 || l[0].Note != "pdf note" || l[0].Start.Section != 0 {
		t.Fatalf("want one note on page 1, got %+v", l)
	}
	// Leave the page and come back: the highlight must be redrawn.
	u.run(chromedp.Click(`#next`), chromedp.Sleep(900*time.Millisecond), chromedp.Click(`#prev`), chromedp.Sleep(1200*time.Millisecond))
	if u.count(`#pdfWrap mark.hl`) == 0 {
		t.Error("PDF note not redrawn after changing pages")
	}
}

// ---------------------------------------------------------------- highlights

func TestHighlightLifecycleEPUB(t *testing.T) {
	u := start(t)
	b := u.importAPI("h.epub", buildEPUB("Highlight Book", 1, 30, nil))
	u.library()
	u.open("Highlight Book", epubReady)
	u.highlight("#page", 3, 0, 20)
	if n := u.count(`mark.hl`); n != 1 {
		t.Fatalf("want 1 highlight mark, got %d", n)
	}
	if bg := eval[string](u, `getComputedStyle(document.querySelector('mark.hl')).backgroundColor`); !strings.HasPrefix(bg, "rgba(") {
		t.Errorf("highlight background %q is opaque; it would hide the text", bg)
	}
	u.run(chromedp.Click(`mark.hl`), chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if u.count(`mark.hl`) != 0 || len(u.annotations(b.ID)) != 0 {
		t.Error("highlight not removed")
	}
}

func TestOverlappingHighlightsRemoveTogether(t *testing.T) {
	u := start(t)
	b := u.importAPI("h.epub", buildEPUB("Highlight Book", 1, 30, nil))
	u.library()
	u.open("Highlight Book", epubReady)
	u.highlight("#page", 3, 0, 20)
	u.highlight("#page", 3, 10, 30)
	if l := u.annotations(b.ID); len(l) != 2 {
		t.Fatalf("want 2 annotations, got %d", len(l))
	}
	u.highlight("#page", 3, 12, 18) // already covered: must not stack another layer
	if l := u.annotations(b.ID); len(l) != 2 {
		t.Fatalf("duplicate highlight stored: %d annotations", len(l))
	}
	u.clickAt("#page", 3, 15)
	u.run(chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if u.count(`mark.hl`) != 0 || len(u.annotations(b.ID)) != 0 {
		t.Error("overlapping highlights not fully removed")
	}
}

func TestHighlightLifecyclePDF(t *testing.T) {
	u := start(t)
	b := u.importAPI("h.pdf", buildPDF(pdfPages()))
	u.library()
	u.open("h", `#pdfWrap .textLayer span[data-b]`)
	if u.count(`#pdfWrap canvas`) < 1 {
		t.Fatal("PDF page canvas not rendered")
	}
	u.highlight("#pdfWrap", 0, 0, 5)
	if u.count(`#pdfWrap mark.hl`) == 0 {
		t.Fatal("no highlight drawn on the PDF text layer")
	}
	u.clickAt("#pdfWrap", 0, 1)
	u.run(chromedp.WaitVisible(`#hlbar`), chromedp.Click(`#hlRemove`), chromedp.Sleep(700*time.Millisecond))
	if u.count(`#pdfWrap mark.hl`) != 0 || len(u.annotations(b.ID)) != 0 {
		t.Error("PDF highlight not removed")
	}
}

// ---------------------------------------------------------------- search

func TestSearchLibrary(t *testing.T) {
	u := start(t)
	u.importAPI("a.epub", buildEPUB("Savannah Chronicles", 2, 10, map[[2]int]string{{1, 6}: "A zebra crossed the river."}))
	u.importAPI("b.pdf", buildPDF(pdfPages()))
	u.library()

	// Title filter narrows the shelf while typing.
	u.run(chromedp.SendKeys(`#libSearch`, "savannah"), chromedp.Sleep(500*time.Millisecond))
	if n := eval[int](u, `[...document.querySelectorAll('#shelf .card')].filter((c) => !c.hidden).length`); n != 1 {
		t.Errorf("title filter shows %d cards, want 1", n)
	}
	u.js(`document.getElementById('libSearch').value = ''; document.getElementById('libSearch').dispatchEvent(new Event('input'))`)

	// Content search in an EPUB finds the paragraph and opens the book there.
	u.run(chromedp.SendKeys(`#libSearch`, "zebra"))
	u.waitText(`#libHits`, "zebra")
	u.run(chromedp.Click(`#libHits .hit`), chromedp.WaitVisible(epubReady), chromedp.Sleep(900*time.Millisecond))
	if got := u.text(`#page h1`); got != "Chapter 2" {
		t.Errorf("search hit opened %q, want Chapter 2", got)
	}
	if got := u.topBlock(); got < 6 || got > 7 {
		t.Errorf("search hit scrolled to block %d, want 7 (zebra paragraph, counting the heading)", got)
	}
	u.home()

	// Content search in a PDF opens the right page.
	u.js(`document.getElementById('libSearch').value = ''`)
	u.run(chromedp.SendKeys(`#libSearch`, "giraffe"))
	u.waitText(`#libHits`, "iraffe")
	u.run(chromedp.Click(`#libHits .hit`), chromedp.WaitVisible(pdfReady), chromedp.Sleep(1000*time.Millisecond))
	if got := u.text(`#pct`); got != "p.3/5" {
		t.Errorf("PDF search hit opened %q, want p.3/5", got)
	}
}

func TestSearchInsideBook(t *testing.T) {
	u := start(t)
	u.importAPI("s.epub", buildEPUB("Search Book", 1, 30, map[[2]int]string{{0, 22}: "The quokka smiled."}))
	u.importAPI("pdfdoc.pdf", buildPDF(pdfPages()))
	u.library()
	u.open("Search Book", epubReady)
	u.run(chromedp.Click(`#findBtn`), chromedp.SendKeys(`#findInput`, "quokka"))
	u.waitText(`#findHits`, "quokka")
	u.run(chromedp.Click(`#findHits .hit`), chromedp.Sleep(900*time.Millisecond))
	if got := u.topBlock(); got < 22 || got > 23 {
		t.Errorf("in-book search jumped to block %d, want about 23", got)
	}
	u.home()
	u.open("pdfdoc", pdfReady)
	u.run(chromedp.Click(`#findBtn`), chromedp.SendKeys(`#findInput`, "epsilon"))
	u.waitText(`#findHits`, "epsilon")
	u.run(chromedp.Click(`#findHits .hit`), chromedp.Sleep(1200*time.Millisecond))
	if got := u.text(`#pct`); got != "p.5/5" {
		t.Errorf("in-PDF search jumped to %q, want p.5/5", got)
	}
}

// ---------------------------------------------------------------- stats & history

func TestStatsAndHistory(t *testing.T) {
	u := start(t)
	b := u.importAPI("st.epub", buildEPUB("Stats Book", 1, 10, nil))
	u.library()
	if !strings.Contains(u.text(`#stats`), "once you have read") {
		t.Errorf("empty state missing: %q", u.text(`#stats`))
	}
	u.api("POST", "/api/books/"+b.ID+"/heartbeat", `{"Seconds":60,"Words":200}`)
	u.api("POST", "/api/books/"+b.ID+"/heartbeat", `{"Seconds":30,"Words":100}`)
	u.library()
	if n := u.count(`#stats .stat`); n != 5 {
		t.Fatalf("want 5 stat cards, got %d (%q)", n, u.text(`#stats`))
	}
	if !strings.Contains(u.text(`#stats`), "2 min") && !strings.Contains(u.text(`#stats`), "1 min") {
		t.Errorf("total time not shown: %q", u.text(`#stats`))
	}
	if n := u.count(`#chart i`); n != 30 {
		t.Errorf("chart has %d bars, want 30", n)
	}
	if n := u.count(`#chart i.on`); n != 1 {
		t.Errorf("chart has %d active days, want 1 (today)", n)
	}
	if !strings.Contains(u.text(`.perbook`), "Stats Book") {
		t.Errorf("time per book missing: %q", u.text(`.perbook`))
	}
	u.open("Stats Book", epubReady)
	u.run(chromedp.Click(`#histBtn`), chromedp.WaitVisible(`#history div`))
	if n := u.count(`#history div`); n != 2 {
		t.Errorf("history lists %d sessions, want 2", n)
	}
}

// ---------------------------------------------------------------- settings & contents

func TestSettingsPersist(t *testing.T) {
	u := start(t)
	u.importAPI("t.epub", buildEPUB("Theme Book", 1, 10, nil))
	u.library()
	u.open("Theme Book", epubReady)
	u.js(`(() => { const s = document.getElementById('theme'); s.value = 'dark'; s.dispatchEvent(new Event('input', {bubbles: true})); })()`)
	u.setRange("size", 26)
	u.js(`(() => { const c = document.getElementById('focus'); c.checked = true; c.dispatchEvent(new Event('change', {bubbles: true})); })()`)
	if got := eval[string](u, `document.body.dataset.theme`); got != "dark" {
		t.Errorf("theme = %q", got)
	}
	if got := eval[string](u, `document.getElementById('page').style.getPropertyValue('--fs')`); got != "26px" {
		t.Errorf("font size = %q", got)
	}
	if u.count(`#page.focus`) != 1 {
		t.Error("focus mode not applied")
	}
	u.library() // full reload
	if got := eval[string](u, `document.body.dataset.theme`); got != "dark" {
		t.Errorf("theme not remembered after reload: %q", got)
	}
	u.open("Theme Book", epubReady)
	if got := eval[string](u, `document.getElementById('page').style.getPropertyValue('--fs')`); got != "26px" {
		t.Errorf("font size not remembered after reload: %q", got)
	}
}

func TestTableOfContents(t *testing.T) {
	u := start(t)
	u.importAPI("toc.epub", buildEPUB("Contents Book", 3, 8, nil))
	u.library()
	u.open("Contents Book", epubReady)
	u.run(chromedp.Click(`#tocBtn`), chromedp.WaitVisible(`#toc a`))
	if n := u.count(`#toc a`); n != 3 {
		t.Fatalf("TOC has %d entries, want 3", n)
	}
	u.js(`document.querySelectorAll('#toc a')[2].click()`)
	u.run(chromedp.Sleep(900 * time.Millisecond))
	if got := u.text(`#page h1`); got != "Chapter 3" {
		t.Errorf("TOC jump opened %q, want Chapter 3", got)
	}
	if got := u.text(`#pct`); got == "0%" || got == "" {
		t.Errorf("progress %q after jumping to the last chapter", got)
	}
}

func TestTagsAndFilter(t *testing.T) {
	u := start(t)
	a := u.importAPI("a.epub", buildEPUB("Alpha Book", 1, 3, nil))
	u.importAPI("b.epub", buildEPUB("Beta Book", 1, 3, nil))
	u.library()
	if u.count(`#tagbar .chip`) != 0 {
		t.Error("tag bar shown with no tags")
	}
	// Edit tags through the card's # button.
	u.js(`document.querySelector('#shelf .card .tagbtn').click()`)
	u.run(chromedp.WaitVisible(`.tagedit`))
	card := eval[string](u, `document.querySelector('.tagedit').closest('.card').textContent`)
	u.run(chromedp.SendKeys(`.tagedit`, "Sci-Fi, to read"+"\r"), chromedp.Sleep(900*time.Millisecond))
	if n := u.count(`#tagbar .chip`); n != 3 { // All + #sci-fi + #to read
		t.Fatalf("tag bar has %d chips, want 3", n)
	}
	tagged := "Alpha Book"
	if !strings.Contains(card, tagged) {
		tagged = "Beta Book"
	}
	_ = a
	// Filtering by a tag shows only the tagged book; All restores both.
	u.js(`[...document.querySelectorAll('#tagbar .chip')].find((c) => c.textContent === '#sci-fi').click()`)
	if n := eval[int](u, `[...document.querySelectorAll('#shelf .card')].filter((c) => !c.hidden).length`); n != 1 {
		t.Errorf("tag filter shows %d books, want 1", n)
	}
	if got := eval[string](u, `[...document.querySelectorAll('#shelf .card')].find((c) => !c.hidden).textContent`); !strings.Contains(got, tagged) {
		t.Errorf("filter kept the wrong book: %q", got)
	}
	u.js(`[...document.querySelectorAll('#tagbar .chip')].find((c) => c.textContent === 'All').click()`)
	if n := eval[int](u, `[...document.querySelectorAll('#shelf .card')].filter((c) => !c.hidden).length`); n != 2 {
		t.Errorf("All shows %d books, want 2", n)
	}
	// Tags survive a reload.
	u.library()
	if n := u.count(`#tagbar .chip`); n != 3 {
		t.Errorf("tags lost after reload: %d chips", n)
	}
}

func TestBackupDownloadLink(t *testing.T) {
	u := start(t)
	u.importAPI("a.epub", buildEPUB("Alpha Book", 1, 3, nil))
	u.library()
	// Fetch through the page so the session cookie is used, as the link does.
	size := eval[int](u, `fetch('/api/backup').then(async (r) => { if (!r.ok || r.headers.get('content-type') !== 'application/zip') return -1; return (await r.arrayBuffer()).byteLength; })`)
	if size < 100 {
		t.Errorf("backup download returned %d bytes", size)
	}
	if got := eval[string](u, `document.getElementById('backupLink').getAttribute('href')`); got != "/api/backup" {
		t.Errorf("backup link href = %q", got)
	}
}

// ---------------------------------------------------------------- redesigned UI

func TestEmptyLibraryAndDragDrop(t *testing.T) {
	u := start(t)
	if !strings.Contains(u.text(`#empty`), "library is empty") || u.count(`#empty:not([hidden])`) != 1 {
		t.Fatal("empty state not shown for a new library")
	}
	drop := func(name string, data []byte) {
		u.js(fmt.Sprintf(`(() => {
			const arr = Uint8Array.from(atob(%q), (c) => c.charCodeAt(0));
			const dt = new DataTransfer(); dt.items.add(new File([arr], %q));
			window.dispatchEvent(new DragEvent('dragenter', {dataTransfer: dt, bubbles: true, cancelable: true}));
		})()`, base64.StdEncoding.EncodeToString(data), name))
	}
	drop("dropped.epub", buildEPUB("Dropped Book", 1, 3, nil))
	if u.count(`#dropveil:not([hidden])`) != 1 {
		t.Error("drop overlay not shown while dragging files over the page")
	}
	u.js(fmt.Sprintf(`(() => {
		const arr = Uint8Array.from(atob(%q), (c) => c.charCodeAt(0));
		const dt = new DataTransfer(); dt.items.add(new File([arr], 'dropped.epub'));
		window.dispatchEvent(new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true}));
	})()`, base64.StdEncoding.EncodeToString(buildEPUB("Dropped Book", 1, 3, nil))))
	u.run(chromedp.Sleep(1500 * time.Millisecond))
	if n := u.count(`#shelf .card`); n != 1 {
		t.Fatalf("dropped book not added: %d cards", n)
	}
	if u.count(`#empty:not([hidden])`) != 0 || u.count(`#dropveil:not([hidden])`) != 0 {
		t.Error("empty state or drop overlay still visible after a drop")
	}
	// Unsupported files are refused with a message instead of an error.
	u.js(`(() => { const dt = new DataTransfer(); dt.items.add(new File(['x'], 'notes.txt'));
		window.dispatchEvent(new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true})); })()`)
	u.run(chromedp.Sleep(400 * time.Millisecond))
	if !strings.Contains(u.text(`#toast`), "Only EPUB and PDF") || u.count(`#shelf .card`) != 1 {
		t.Errorf("unsupported drop not handled: toast %q, %d cards", u.text(`#toast`), u.count(`#shelf .card`))
	}
}

func TestGeneratedCovers(t *testing.T) {
	u := start(t)
	u.importAPI("a.epub", buildEPUB("Alpha Book", 1, 3, nil))
	u.importAPI("b.pdf", buildPDF(pdfPages()))
	u.library()
	if n := u.count(`#shelf .cover.gen`); n != 2 {
		t.Fatalf("want a generated cover for each book, got %d", n)
	}
	hues := eval[[]string](u, `[...document.querySelectorAll('#shelf .cover.gen')].map((c) => c.style.getPropertyValue('--h'))`)
	if len(hues) != 2 || hues[0] == hues[1] {
		t.Errorf("covers should differ per book: %v", hues)
	}
	badges := eval[[]string](u, `[...document.querySelectorAll('#shelf .cover .fmt')].map((c) => c.textContent).sort()`)
	if len(badges) != 2 || badges[0] != "EPUB" || badges[1] != "PDF" {
		t.Errorf("format badges = %v", badges)
	}
}

func TestDrawersAndEscape(t *testing.T) {
	u := start(t)
	u.importAPI("d.epub", buildEPUB("Drawer Book", 2, 10, nil))
	u.library()
	u.open("Drawer Book", epubReady)
	// The toolbar stays usable while a drawer is open: the same button closes it.
	u.run(chromedp.Click(`#setBtn`), chromedp.WaitVisible(`#settings`))
	if u.count(`#scrim:not([hidden])`) != 1 {
		t.Error("scrim not shown behind an open drawer")
	}
	u.run(chromedp.Click(`#setBtn`), chromedp.Sleep(300*time.Millisecond))
	if u.count(`#settings:not([hidden])`) != 0 || u.count(`#scrim:not([hidden])`) != 0 {
		t.Error("clicking the toolbar button again did not close the drawer")
	}
	// Clicking the scrim closes it too.
	u.run(chromedp.Click(`#tocBtn`), chromedp.WaitVisible(`#toc`), chromedp.MouseClickXY(150, 400), chromedp.Sleep(300*time.Millisecond))
	if u.count(`#toc:not([hidden])`) != 0 {
		t.Error("clicking outside did not close the drawer")
	}
	// Escape closes a drawer first and only then leaves the book.
	u.run(chromedp.Click(`#notesBtn`), chromedp.WaitVisible(`#notes`), chromedp.KeyEvent("\x1b"), chromedp.Sleep(400*time.Millisecond))
	if u.count(`#notes:not([hidden])`) != 0 || u.count(`#reader:not([hidden])`) != 1 {
		t.Error("first Escape should close the drawer and stay in the book")
	}
	u.run(chromedp.KeyEvent("\x1b"), chromedp.Sleep(600*time.Millisecond))
	if u.count(`#library:not([hidden])`) != 1 {
		t.Error("second Escape should return to the library")
	}
}

func TestReadingChromeAndFooter(t *testing.T) {
	u := start(t)
	u.importAPI("c.epub", buildEPUB("Chrome Book", 3, 20, nil))
	u.library()
	u.open("Chrome Book", epubReady)
	if u.text(`#chap`) != "Chapter 1" {
		t.Errorf("chapter label = %q, want Chapter 1", u.text(`#chap`))
	}
	if !strings.Contains(u.text(`#left`), "left") {
		t.Errorf("time left missing from footer: %q", u.text(`#left`))
	}
	// Scrolling down with the wheel tucks the toolbars away; up or hovering the edge brings them back.
	u.js(`window.dispatchEvent(new WheelEvent('wheel', {deltaY: 120}))`)
	if u.count(`body.chrome-hidden`) != 1 {
		t.Error("toolbars not hidden after scrolling down")
	}
	u.js(`window.dispatchEvent(new WheelEvent('wheel', {deltaY: -120}))`)
	if u.count(`body.chrome-hidden`) != 0 {
		t.Error("toolbars not restored after scrolling up")
	}
	u.js(`window.dispatchEvent(new WheelEvent('wheel', {deltaY: 120}))`)
	u.js(`window.dispatchEvent(new MouseEvent('mousemove', {clientY: 10}))`)
	if u.count(`body.chrome-hidden`) != 0 {
		t.Error("toolbars not restored by moving the pointer to the top edge")
	}
	// Programmatic jumps (restoring a position) must never hide them.
	u.scrollToBlock(15)
	if u.count(`body.chrome-hidden`) != 0 {
		t.Error("toolbars hidden by a non-user scroll")
	}
	// Jumping chapters updates the chapter label.
	u.js(`document.getElementById('tocBtn').click(); document.querySelectorAll('#toc a')[2].click()`)
	u.run(chromedp.Sleep(900 * time.Millisecond))
	if u.text(`#chap`) != "Chapter 3" {
		t.Errorf("chapter label after jump = %q, want Chapter 3", u.text(`#chap`))
	}
}

func TestDefaultThemeFollowsSystem(t *testing.T) {
	u := start(t)
	set := func(scheme string) {
		u.run(emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}}))
		u.library()
	}
	set("dark")
	if got := eval[string](u, `document.body.dataset.theme`); got != "dark" {
		t.Errorf("OS dark: theme = %q", got)
	}
	set("light")
	if got := eval[string](u, `document.body.dataset.theme`); got != "light" {
		t.Errorf("OS light: theme = %q (a default must not be frozen into storage)", got)
	}
	// An explicit choice wins over the OS.
	u.js(`(() => { const s = document.getElementById('theme'); s.value = 'sepia'; s.dispatchEvent(new Event('input', {bubbles: true})); })()`)
	set("dark")
	if got := eval[string](u, `document.body.dataset.theme`); got != "sepia" {
		t.Errorf("explicit theme lost: %q", got)
	}
}
