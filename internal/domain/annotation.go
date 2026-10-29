package domain

import (
	"errors"
	"time"
)

// AnnotationKind distinguishes bookmarks, highlights and notes.
type AnnotationKind string

const (
	KindBookmark  AnnotationKind = "bookmark"
	KindHighlight AnnotationKind = "highlight"
	KindNote      AnnotationKind = "note"
)

// Annotation is a user mark on a book. Highlights carry the quoted text so they
// can be re-attached if offsets drift.
type Annotation struct {
	ID        int64          `json:"id"`
	BookID    string         `json:"bookId"`
	Kind      AnnotationKind `json:"kind"`
	Start     Anchor         `json:"start"`
	EndBlock  int            `json:"endBlock"`
	EndOffset int            `json:"endOffset"`
	Quote     string         `json:"quote"`
	Note      string         `json:"note"`
	Color     string         `json:"color"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Validate reports whether the annotation is storable.
func (a Annotation) Validate() error {
	switch a.Kind {
	case KindBookmark:
	case KindHighlight, KindNote:
		if a.Quote == "" && a.Note == "" {
			return errors.New("annotation: highlight or note needs text")
		}
	default:
		return errors.New("annotation: unknown kind")
	}
	return a.Start.Validate()
}
