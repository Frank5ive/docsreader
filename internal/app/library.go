package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frank5ive/docsreader/internal/domain"
)

// Library imports and lists books.
type Library struct {
	Books   BookRepository
	Files   FileStore
	Readers map[domain.Format]FormatReader
	Index   SearchIndex // optional
	Now     func() time.Time
}

// Import copies a file into the store and registers it. Importing the same
// content twice returns the existing book.
func (l *Library) Import(ctx context.Context, src string) (domain.Book, error) {
	f, err := os.Open(src)
	if err != nil {
		return domain.Book{}, err
	}
	defer f.Close()
	return l.ImportReader(ctx, filepath.Base(src), f)
}

// ImportReader imports content whose original file name is name.
func (l *Library) ImportReader(ctx context.Context, name string, content io.Reader) (domain.Book, error) {
	src := name
	format, err := formatOf(src)
	if err != nil {
		return domain.Book{}, err
	}
	rd, ok := l.Readers[format]
	if !ok {
		return domain.Book{}, fmt.Errorf("%w: %s", ErrUnsupported, format)
	}
	// Stream into the store under a temporary id, hashing on the way.
	h := sha256.New()
	path, err := l.Files.Put("incoming-"+hex.EncodeToString(randBytes()), "."+string(format), io.TeeReader(content, h))
	if err != nil {
		return domain.Book{}, err
	}
	id := hex.EncodeToString(h.Sum(nil))
	if b, err := l.Books.Get(ctx, id); err == nil {
		_ = l.Files.Remove(path)
		return b, nil
	}
	if path, err = l.Files.Rename(path, id, "."+string(format)); err != nil {
		return domain.Book{}, err
	}
	if err != nil {
		return domain.Book{}, err
	}
	doc, err := rd.Open(path)
	if err != nil {
		_ = l.Files.Remove(path)
		return domain.Book{}, fmt.Errorf("open %s: %w", filepath.Base(src), err)
	}
	defer doc.Close()
	m := doc.Meta()
	title := m.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	b := domain.Book{
		ID: id, Title: title, Author: m.Author, Format: format, Path: path,
		Sections: m.Sections, WordCount: m.WordCount, HasCover: m.HasCover,
		AddedAt: l.Now(),
	}
	if err := l.Books.Save(ctx, b); err != nil {
		_ = l.Files.Remove(path)
		return domain.Book{}, err
	}
	if l.Index != nil {
		words, err := IndexBook(ctx, l.Index, id, doc)
		if err != nil {
			return b, fmt.Errorf("imported, but indexing failed: %w", err)
		}
		if b.WordCount == 0 && words > 0 { // formats without a word count (PDF)
			b.WordCount = words
			_ = l.Books.Save(ctx, b)
		}
	}
	return b, nil
}

func (l *Library) List(ctx context.Context) ([]domain.Book, error) { return l.Books.List(ctx) }

func (l *Library) Delete(ctx context.Context, id string) error {
	b, err := l.Books.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := l.Books.Delete(ctx, id); err != nil {
		return err
	}
	if l.Index != nil {
		_ = l.Index.Remove(ctx, id)
	}
	return l.Files.Remove(b.Path)
}

func formatOf(path string) (domain.Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return domain.FormatEPUB, nil
	case ".pdf":
		return domain.FormatPDF, nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupported, filepath.Ext(path))
}

func randBytes() []byte {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return b
}

// EnsureIndexed indexes books that have no search index yet (e.g. imported
// before search existed).
func (l *Library) EnsureIndexed(ctx context.Context) error {
	if l.Index == nil {
		return nil
	}
	books, err := l.Books.List(ctx)
	if err != nil {
		return err
	}
	for _, b := range books {
		if ok, err := l.Index.Indexed(ctx, b.ID); err != nil || ok {
			continue
		}
		rd, ok := l.Readers[b.Format]
		if !ok {
			continue
		}
		doc, err := rd.Open(b.Path)
		if err != nil {
			continue
		}
		words, err := IndexBook(ctx, l.Index, b.ID, doc)
		doc.Close()
		if err != nil {
			return err
		}
		if b.WordCount == 0 && words > 0 {
			b.WordCount = words
			_ = l.Books.Save(ctx, b)
		}
	}
	return nil
}
