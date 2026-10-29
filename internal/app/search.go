package app

import (
	"context"
	"strings"
)

// Search runs full-text queries and keeps the index in sync with the library.
type Search struct {
	Index SearchIndex
}

// IndexBook extracts the text of every section of doc and stores it.
func IndexBook(ctx context.Context, idx SearchIndex, bookID string, doc Document) error {
	var blocks []Block
	for s := 0; s < doc.Meta().Sections; s++ {
		texts, err := doc.Blocks(s)
		if err != nil {
			if err == ErrUnsupported {
				break
			}
			continue
		}
		for i, t := range texts {
			if strings.TrimSpace(t) != "" {
				blocks = append(blocks, Block{Section: s, Index: i, Text: t})
			}
		}
	}
	return idx.Index(ctx, bookID, blocks)
}

// Find searches one book, or the whole library when bookID is empty.
func (s *Search) Find(ctx context.Context, query, bookID string) ([]Hit, error) {
	return s.Index.Search(ctx, query, bookID, 50)
}
