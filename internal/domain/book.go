// Package domain holds the core entities of docsreader. It depends only on the
// standard library.
package domain

import "time"

// Format is the file format of a book.
type Format string

const (
	FormatEPUB Format = "epub"
	FormatPDF  Format = "pdf"
)

// Book is an imported document in the library.
type Book struct {
	ID        string // hex SHA-256 of the file content
	Title     string
	Author    string
	Format    Format
	Path      string // location inside the managed library store
	Sections  int    // spine items (EPUB) or pages (PDF)
	AddedAt   time.Time
	OpenedAt  time.Time
	HasCover  bool
	WordCount int
}

// Chapter is one entry in a table of contents.
type Chapter struct {
	Title   string
	Section int // spine index or page number (0-based)
	Depth   int
}
