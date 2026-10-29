// Package pdf reads PDF metadata with pdfcpu (pure Go). Rendering happens in
// the browser; the server only streams the original file.
package pdf

import (
	"context"
	"os"

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
	return &doc{meta: app.DocMeta{Title: info.Title, Author: info.Author, Sections: info.PageCount}}, nil
}

type doc struct{ meta app.DocMeta }

func (d *doc) Meta() app.DocMeta                       { return d.meta }
func (d *doc) TOC() []domain.Chapter                   { return nil }
func (d *doc) Section(int) (string, error)             { return "", app.ErrUnsupported }
func (d *doc) Resource(string) ([]byte, string, error) { return nil, "", app.ErrUnsupported }
func (d *doc) Cover() ([]byte, string, error)          { return nil, "", app.ErrNotFound }
func (d *doc) Close() error                            { return nil }
