package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteFixture builds a minimal two-chapter EPUB3 and returns its path.
func writeFixture(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf": `<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Test Book</dc:title><dc:creator>Ann Author</dc:creator></metadata>
<manifest><item id="n" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/>
<item id="img" href="img/a.png" media-type="image/png"/></manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/nav.xhtml": `<html><body><nav epub:type="toc" xmlns:epub="http://www.idpf.org/2007/ops"><ol><li><a href="c1.xhtml">One</a></li><li><a href="c2.xhtml#x">Two</a></li></ol></nav></body></html>`,
		"OEBPS/c1.xhtml":  `<html><body><h1>One</h1><p onclick="x()">Hello <b>world</b>.</p><script>bad()</script><img src="img/a.png"/><a href="javascript:alert(1)">x</a></body></html>`,
		"OEBPS/c2.xhtml":  `<html><body><p>Second chapter text here.</p></body></html>`,
		"OEBPS/img/a.png": "png",
	}
	p := filepath.Join(t.TempDir(), "t.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	f.Close()
	return p
}

func TestOpenAndSanitise(t *testing.T) {
	d, err := Reader{}.Open(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m := d.Meta()
	if m.Title != "Test Book" || m.Author != "Ann Author" || m.Sections != 2 {
		t.Fatalf("meta = %+v", m)
	}
	if toc := d.TOC(); len(toc) != 2 || toc[1].Section != 1 || toc[1].Title != "Two" {
		t.Fatalf("toc = %+v", toc)
	}
	h, err := d.Section(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if strings.Contains(h, bad) {
			t.Errorf("section still contains %q: %s", bad, h)
		}
	}
	for _, want := range []string{`data-b="0"`, `data-b="1"`, `src="res/OEBPS/img/a.png"`} {
		if !strings.Contains(h, want) {
			t.Errorf("section missing %q: %s", want, h)
		}
	}
	if _, _, err := d.Resource("OEBPS/img/a.png"); err != nil {
		t.Errorf("resource: %v", err)
	}
	if _, _, err := d.Resource("META-INF/container.xml"); err == nil {
		t.Error("non-manifest file must not be served")
	}
}
