// Package pdf reads PDF metadata with pdfcpu and page text with ledongthuc/pdf
// (both pure Go). Rendering happens in the browser; the server only streams
// the original file.
package pdf

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	lpdf "github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

type Reader struct{}

func (Reader) Format() domain.Format { return domain.FormatPDF }

func (Reader) Open(path string) (app.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	conf := model.NewDefaultConfiguration()
	info, err := api.PDFInfo(context.Background(), f, path, nil, false, conf)
	if err != nil {
		return nil, err
	}
	return &doc{path: path, meta: app.DocMeta{Title: info.Title, Author: info.Author, Sections: info.PageCount}}, nil
}

// doc opens the file for text extraction lazily and releases it in Close, so
// a book that is only being read never holds the file open (Windows cannot
// delete open files).
type doc struct {
	path string
	meta app.DocMeta
	file *os.File
	rd   *lpdf.Reader
}

func (d *doc) Meta() app.DocMeta                       { return d.meta }
func (d *doc) TOC() []domain.Chapter                   { return nil }
func (d *doc) SectionWords() []int                     { return nil }
func (d *doc) Section(int) (string, error)             { return "", app.ErrUnsupported }
func (d *doc) Resource(string) ([]byte, string, error) { return nil, "", app.ErrUnsupported }
func (d *doc) Cover() ([]byte, string, error)          { return nil, "", app.ErrNotFound }

func (d *doc) Close() error {
	d.rd = nil
	if d.file != nil {
		err := d.file.Close()
		d.file = nil
		return err
	}
	return nil
}

// Blocks returns the text of page i as a single block. Malformed pages yield
// no text instead of failing the whole import.
func (d *doc) Blocks(i int) (out []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("pdf page %d: %v", i+1, r)
		}
	}()
	if d.rd == nil {
		d.file, d.rd, err = lpdf.Open(d.path)
		if err != nil {
			return nil, err
		}
	}
	p := d.rd.Page(i + 1)
	if p.V.IsNull() {
		return nil, nil
	}
	txt := pageText(p)
	txt = strings.Join(strings.Fields(txt), " ")
	if txt == "" {
		return nil, nil
	}
	return []string{txt}, nil
}

// pageText rebuilds the text of a page from glyph positions. GetPlainText only
// breaks lines on a few operators, so PDFs that position text with Td/Tm glue
// the last word of a line to the first of the next ("pagelives"). Here a new
// line or a gap wider than a fraction of the font size becomes a space.
func pageText(p lpdf.Page) string {
	var sb strings.Builder
	var prev lpdf.Text
	for i, g := range p.Content().Text {
		if i > 0 {
			size := math.Max(prev.FontSize, 1)
			switch {
			case math.Abs(g.Y-prev.Y) > size*0.5: // new line
				sb.WriteByte(' ')
			case prev.W > 0 && g.X-(prev.X+prev.W) > size*0.2: // word gap
				sb.WriteByte(' ')
			case prev.W <= 0 && g.X-prev.X > size: // width unknown: only a large jump counts
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(g.S)
		prev = g
	}
	return sb.String()
}
