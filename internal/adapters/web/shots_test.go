//go:build e2e

package web_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestScreenshots writes PNGs of the main screens to $DR_SHOTS (skipped if unset).
// It is a visual aid for UI work, not an assertion.
func TestScreenshots(t *testing.T) {
	out := os.Getenv("DR_SHOTS")
	if out == "" {
		t.Skip("DR_SHOTS not set")
	}
	os.MkdirAll(out, 0o755)
	u := start(t)
	var ids []string
	for i, title := range []string{"The Savannah Chronicles", "A Short History of Everything", "Notes on Machine Learning", "Cooking for Two"} {
		b := u.importAPI(fmt.Sprintf("b%d.epub", i), buildEPUB(title, 3, 30, nil))
		ids = append(ids, b.ID)
		u.api("PUT", "/api/books/"+b.ID+"/progress", fmt.Sprintf(`{"section":1,"block":%d,"offset":0,"percent":%.2f}`, 5+i*3, 0.1+0.2*float64(i)))
		u.api("POST", "/api/books/"+b.ID+"/heartbeat", `{"Seconds":80,"Words":300}`)
	}
	u.api("PUT", "/api/books/"+ids[0]+"/tags", `{"Tags":["fiction","to read"]}`)
	u.api("PUT", "/api/books/"+ids[2]+"/tags", `{"Tags":["work"]}`)
	u.importAPI("report.pdf", buildPDF(pdfPages()))
	shot := func(name string) {
		var buf []byte
		u.run(chromedp.Sleep(900*time.Millisecond), chromedp.CaptureScreenshot(&buf))
		os.WriteFile(filepath.Join(out, name+".png"), buf, 0o644)
	}
	u.library()
	shot("1-library")
	u.open("Savannah", epubReady)
	u.highlight("#page", 3, 0, 40)
	u.js(`getSelection().removeAllRanges(); document.getElementById('selbar').hidden = true`)
	shot("2-reader")
	u.run(chromedp.Click(`#setBtn`))
	shot("3-settings")
	u.setRange("size", 19)
	u.js(`(() => { const s = document.getElementById('theme'); s.value = 'dark'; s.dispatchEvent(new Event('input', {bubbles: true})); })()`)
	u.run(chromedp.Click(`#setBtn`))
	shot("4-reader-dark")
	u.home()
	shot("5-library-dark")
	u.open("report", pdfReady)
	shot("6-pdf-dark")
	u.run(chromedp.EmulateViewport(390, 800, chromedp.EmulateMobile))
	u.home()
	shot("7-library-mobile")
}
