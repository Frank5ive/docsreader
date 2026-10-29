package web_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frank5ive/docsreader/internal/adapters/format/pdf"
)

// The generated PDF must be readable by the same code the app uses, and
// separate lines must stay separate words (otherwise search cannot find them).
func TestGeneratedPDFIsReadable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "g.pdf")
	os.WriteFile(p, buildPDF([]string{"Alpha one\nAlpha two", "Giraffe page\nlives here", "Third"}), 0o600)
	d, err := pdf.Reader{}.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Meta().Sections != 3 {
		t.Fatalf("pages = %d", d.Meta().Sections)
	}
	b, err := d.Blocks(1)
	if err != nil || len(b) != 1 || b[0] != "Giraffe page lives here" {
		t.Fatalf("page 2 text = %q, %v", b, err)
	}
}
