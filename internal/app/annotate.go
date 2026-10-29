package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/frank5ive/docsreader/internal/domain"
)

// Annotate manages bookmarks, highlights and notes.
type Annotate struct {
	Books Annotated
	Anns  AnnotationRepository
	Now   func() time.Time
}

// Annotated is the slice of BookRepository that Annotate needs.
type Annotated interface {
	Get(ctx context.Context, id string) (domain.Book, error)
}

const maxQuote, maxNote = 2000, 10000

func (s *Annotate) Add(ctx context.Context, a domain.Annotation) (domain.Annotation, error) {
	if _, err := s.Books.Get(ctx, a.BookID); err != nil {
		return a, err
	}
	if len(a.Quote) > maxQuote {
		a.Quote = a.Quote[:maxQuote]
	}
	if len(a.Note) > maxNote {
		return a, fmt.Errorf("note too long")
	}
	if a.Kind == domain.KindHighlight && a.Note != "" {
		a.Kind = domain.KindNote // a highlight with text is a note
	}
	if err := a.Validate(); err != nil {
		return a, err
	}
	a.ID, a.CreatedAt = 0, s.Now()
	return s.Anns.Add(ctx, a)
}

func (s *Annotate) List(ctx context.Context, bookID string) ([]domain.Annotation, error) {
	return s.Anns.List(ctx, bookID)
}

func (s *Annotate) UpdateNote(ctx context.Context, bookID string, id int64, note string) error {
	if len(note) > maxNote {
		return fmt.Errorf("note too long")
	}
	return s.Anns.UpdateNote(ctx, bookID, id, note)
}

func (s *Annotate) Delete(ctx context.Context, bookID string, id int64) error {
	return s.Anns.Delete(ctx, bookID, id)
}

// ExportMarkdown renders all annotations of a book as a Markdown document.
func (s *Annotate) ExportMarkdown(ctx context.Context, bookID string) (string, error) {
	b, err := s.Books.Get(ctx, bookID)
	if err != nil {
		return "", err
	}
	list, err := s.Anns.List(ctx, bookID)
	if err != nil {
		return "", err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start.Percent < list[j].Start.Percent })
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", b.Title)
	if b.Author != "" {
		fmt.Fprintf(&sb, "*%s*\n", b.Author)
	}
	sb.WriteString("\n")
	for _, a := range list {
		pct := int(a.Start.Percent * 100)
		switch a.Kind {
		case domain.KindBookmark:
			fmt.Fprintf(&sb, "- 🔖 Bookmark at %d%%", pct)
			if a.Quote != "" {
				fmt.Fprintf(&sb, ": %s", oneLine(a.Quote))
			}
			sb.WriteString("\n")
		default:
			fmt.Fprintf(&sb, "> %s\n", strings.ReplaceAll(strings.TrimSpace(a.Quote), "\n", "\n> "))
			if a.Note != "" {
				fmt.Fprintf(&sb, "\n**Note:** %s\n", a.Note)
			}
			fmt.Fprintf(&sb, "\n— %d%%, %s\n\n", pct, a.CreatedAt.Format("2006-01-02"))
		}
	}
	return sb.String(), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
