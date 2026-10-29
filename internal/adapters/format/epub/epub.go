// Package epub reads EPUB files using only the standard library and x/net/html.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

// Reader opens EPUB files.
type Reader struct{}

func (Reader) Format() domain.Format { return domain.FormatEPUB }

func (Reader) Open(p string) (app.Document, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	d := &doc{zr: zr, files: map[string]*zip.File{}}
	for _, f := range zr.File {
		d.files[f.Name] = f
	}
	if err := d.parse(); err != nil {
		zr.Close()
		return nil, err
	}
	return d, nil
}

type doc struct {
	zr       *zip.ReadCloser
	files    map[string]*zip.File
	title    string
	author   string
	spine    []string // zip paths in reading order
	manifest map[string]item
	coverID  string
	toc      []domain.Chapter
	words    int
	secWords []int
}

type item struct {
	href, mediaType, props string
}

type opf struct {
	Metadata struct {
		Title   []string `xml:"title"`
		Creator []string `xml:"creator"`
		Meta    []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		TOC      string `xml:"toc,attr"`
		ItemRefs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func (d *doc) read(name string) ([]byte, error) {
	f, ok := d.files[name]
	if !ok {
		return nil, app.ErrNotFound
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 64<<20))
}

func (d *doc) parse() error {
	data, err := d.read("META-INF/container.xml")
	if err != nil {
		return errors.New("epub: missing container.xml")
	}
	var c struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(data, &c); err != nil || len(c.Rootfiles) == 0 {
		return errors.New("epub: bad container.xml")
	}
	opfPath := c.Rootfiles[0].Path
	data, err = d.read(opfPath)
	if err != nil {
		return fmt.Errorf("epub: missing package %s", opfPath)
	}
	var o opf
	if err := xml.Unmarshal(data, &o); err != nil {
		return fmt.Errorf("epub: bad package: %w", err)
	}
	base := path.Dir(opfPath)
	if len(o.Metadata.Title) > 0 {
		d.title = strings.TrimSpace(o.Metadata.Title[0])
	}
	if len(o.Metadata.Creator) > 0 {
		d.author = strings.TrimSpace(o.Metadata.Creator[0])
	}
	d.manifest = map[string]item{}
	ids := map[string]string{} // id -> zip path
	for _, it := range o.Manifest.Items {
		p := resolve(base, it.Href)
		d.manifest[p] = item{href: p, mediaType: it.MediaType, props: it.Properties}
		ids[it.ID] = p
		if strings.Contains(it.Properties, "cover-image") {
			d.coverID = p
		}
	}
	if d.coverID == "" {
		for _, m := range o.Metadata.Meta {
			if m.Name == "cover" {
				d.coverID = ids[m.Content]
			}
		}
	}
	for _, ref := range o.Spine.ItemRefs {
		if ref.Linear == "no" {
			continue
		}
		if p, ok := ids[ref.IDRef]; ok {
			d.spine = append(d.spine, p)
		}
	}
	if len(d.spine) == 0 {
		return errors.New("epub: empty spine")
	}
	d.buildTOC(o.Spine.TOC, ids)
	d.countWords()
	return nil
}

func resolve(base, href string) string {
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	return path.Clean(path.Join(base, href))
}

func (d *doc) countWords() {
	d.secWords = make([]int, len(d.spine))
	for i := range d.spine {
		h, err := d.Section(i)
		if err != nil {
			continue
		}
		d.secWords[i] = len(strings.FieldsFunc(stripTags(h), unicode.IsSpace))
		d.words += d.secWords[i]
	}
}

func (d *doc) Meta() app.DocMeta {
	return app.DocMeta{Title: d.title, Author: d.author, Sections: len(d.spine), WordCount: d.words, HasCover: d.coverID != ""}
}

func (d *doc) TOC() []domain.Chapter { return d.toc }

func (d *doc) SectionWords() []int { return d.secWords }

func (d *doc) Close() error { return d.zr.Close() }

func (d *doc) Cover() ([]byte, string, error) {
	if d.coverID == "" {
		return nil, "", app.ErrNotFound
	}
	return d.Resource(d.coverID)
}

// Resource serves a file from the archive. Only manifest entries are exposed.
func (d *doc) Resource(name string) ([]byte, string, error) {
	name = path.Clean(name)
	it, ok := d.manifest[name]
	if !ok {
		return nil, "", app.ErrNotFound
	}
	data, err := d.read(name)
	if err != nil {
		return nil, "", err
	}
	mt := it.mediaType
	if mt == "" {
		mt = mime.TypeByExtension(path.Ext(name))
	}
	return data, mt, nil
}

func stripTags(h string) string {
	var b strings.Builder
	in := false
	for _, r := range h {
		switch {
		case r == '<':
			in = true
			b.WriteByte(' ')
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
