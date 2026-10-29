package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestRealPDF runs against a PDF named by DR_TEST_PDF; it is skipped otherwise.
func TestRealPDF(t *testing.T) {
	p := os.Getenv("DR_TEST_PDF")
	if p == "" {
		t.Skip("DR_TEST_PDF not set")
	}
	d, err := Reader{}.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	words, empty := 0, 0
	for i := 0; i < d.Meta().Sections; i++ {
		b, err := d.Blocks(i)
		if err != nil || len(b) == 0 {
			empty++
			continue
		}
		words += len(strings.Fields(b[0]))
	}
	t.Logf("pages=%d emptyPages=%d words=%d", d.Meta().Sections, empty, words)
	if words == 0 {
		t.Fatal("no text extracted")
	}
}
