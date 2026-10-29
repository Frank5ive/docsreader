package web_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// buildEPUB returns an EPUB3 with a nav document. Each chapter has `paras`
// paragraphs; extra maps "chapter/paragraph" (0-based) to a sentence appended
// to that paragraph so tests can search for it.
func buildEPUB(title string, chapters, paras int, extra map[[2]int]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, content string) {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	put("META-INF/container.xml", `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`)
	var manifest, spine, nav strings.Builder
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	for c := 0; c < chapters; c++ {
		fmt.Fprintf(&manifest, `<item id="c%d" href="c%d.xhtml" media-type="application/xhtml+xml"/>`, c, c)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, c)
		fmt.Fprintf(&nav, `<li><a href="c%d.xhtml">Chapter %d</a></li>`, c, c+1)
		var body strings.Builder
		fmt.Fprintf(&body, "<h1>Chapter %d</h1>", c+1)
		for p := 0; p < paras; p++ {
			fmt.Fprintf(&body, "<p>Paragraph %d-%d. %s %s</p>", c+1, p, strings.Repeat("lorem ipsum dolor sit amet ", 20), extra[[2]int{c, p}])
		}
		put(fmt.Sprintf("c%d.xhtml", c), `<html><body>`+body.String()+`</body></html>`)
	}
	put("nav.xhtml", `<html xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol>`+nav.String()+`</ol></nav></body></html>`)
	put("c.opf", fmt.Sprintf(`<package><metadata xmlns:dc="x"><dc:title>%s</dc:title><dc:creator>Test Author</dc:creator></metadata><manifest>%s</manifest><spine>%s</spine></package>`, title, manifest.String(), spine.String()))
	zw.Close()
	return buf.Bytes()
}

// buildPDF returns a minimal valid PDF with one text page per entry. Each page
// has several lines so the text layer has more than one span.
func buildPDF(pages []string) []byte {
	var buf bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	buf.WriteString("%PDF-1.4\n")
	n := len(pages)
	kids := make([]string, n)
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, text := range pages {
		var cs strings.Builder
		cs.WriteString("BT /F1 18 Tf\n")
		y := 740
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintf(&cs, "1 0 0 1 72 %d Tm (%s) Tj\n", y, line)
			y -= 30
		}
		cs.WriteString("ET")
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", cs.Len(), cs.String()))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return buf.Bytes()
}
