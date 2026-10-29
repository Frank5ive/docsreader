// Package app contains the use cases and the ports they depend on.
package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/frank5ive/docsreader/internal/domain"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrUnsupported = errors.New("unsupported")
)

type BookRepository interface {
	Save(ctx context.Context, b domain.Book) error
	Get(ctx context.Context, id string) (domain.Book, error)
	List(ctx context.Context) ([]domain.Book, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
}

type ProgressRepository interface {
	Save(ctx context.Context, p domain.Progress) error
	Get(ctx context.Context, bookID string) (domain.Progress, error)
	// All returns the latest progress of every book keyed by book ID.
	All(ctx context.Context) (map[string]domain.Progress, error)
}

type SessionRepository interface {
	Add(ctx context.Context, s domain.Session) error
	Stats(ctx context.Context, bookID string) (domain.Stats, error)
	// Recent returns the newest sessions first; bookID "" means all books.
	Recent(ctx context.Context, bookID string, limit int) ([]domain.Session, error)
}

// DocMeta is what a format reader learns about a file.
type DocMeta struct {
	Title, Author string
	Sections      int
	WordCount     int
	HasCover      bool
}

// Document is an opened book file.
type Document interface {
	Meta() DocMeta
	TOC() []domain.Chapter
	// SectionWords returns the word count of each section (nil if unknown).
	SectionWords() []int
	// Blocks returns the plain text of each block of a section, indexed by block number.
	Blocks(section int) ([]string, error)
	// Section returns sanitised HTML for one reflowable section.
	Section(i int) (string, error)
	// Resource returns an embedded file such as an image or stylesheet.
	Resource(name string) (data []byte, mime string, err error)
	Cover() (data []byte, mime string, err error)
	Close() error
}

// FormatReader opens files of one format.
type FormatReader interface {
	Format() domain.Format
	Open(path string) (Document, error)
}

// FileStore keeps imported book files in a managed location.
type FileStore interface {
	Put(id string, ext string, r io.Reader) (path string, err error)
	// Rename moves a stored file to its final id-based name.
	Rename(path, id, ext string) (string, error)
	Remove(path string) error
}

type AnnotationRepository interface {
	Add(ctx context.Context, a domain.Annotation) (domain.Annotation, error)
	List(ctx context.Context, bookID string) ([]domain.Annotation, error)
	UpdateNote(ctx context.Context, bookID string, id int64, note string) error
	Delete(ctx context.Context, bookID string, id int64) error
}

// Block is one paragraph-level piece of text, addressed like an Anchor.
type Block struct {
	Section, Index int
	Text           string
}

// Hit is a full-text search result.
type Hit struct {
	BookID  string `json:"bookId"`
	Title   string `json:"title"`
	Section int    `json:"section"`
	Block   int    `json:"block"`
	Snippet string `json:"snippet"` // contains <mark> around matches; all other text is HTML-escaped
}

// SearchIndex stores book text for full-text search.
type SearchIndex interface {
	Index(ctx context.Context, bookID string, blocks []Block) error
	Indexed(ctx context.Context, bookID string) (bool, error)
	Remove(ctx context.Context, bookID string) error
	Search(ctx context.Context, query, bookID string, limit int) ([]Hit, error)
}
