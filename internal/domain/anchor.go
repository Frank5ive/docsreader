package domain

import (
	"errors"
	"time"
)

// Anchor is a reflow-stable reading position. Unlike a page number it survives
// font, size and viewport changes.
//
// For EPUB: Section is the spine index, Block the paragraph index within the
// section and Offset the rune offset inside that block.
// For PDF: Section is the page (0-based) and Offset is the vertical position
// within the page in thousandths (0..1000).
type Anchor struct {
	Section int     `json:"section"`
	Block   int     `json:"block"`
	Offset  int     `json:"offset"`
	Percent float64 `json:"percent"` // 0..1 across the whole book
	Quote   string  `json:"quote"`   // short text snippet used to re-locate after file changes
}

// Validate reports whether the anchor is well formed.
func (a Anchor) Validate() error {
	if a.Section < 0 || a.Block < 0 || a.Offset < 0 {
		return errors.New("anchor: negative coordinate")
	}
	if a.Percent < 0 || a.Percent > 1 {
		return errors.New("anchor: percent out of range")
	}
	return nil
}

// Progress is the saved position of a book.
type Progress struct {
	BookID    string
	Anchor    Anchor
	UpdatedAt time.Time
}
