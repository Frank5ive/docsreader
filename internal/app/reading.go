package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/frank5ive/docsreader/internal/domain"
)

// Reading serves book content and tracks where the reader stopped.
type Reading struct {
	Books    BookRepository
	Progress ProgressRepository
	Sessions SessionRepository
	Readers  map[domain.Format]FormatReader
	Now      func() time.Time

	mu   sync.Mutex
	docs map[string]Document // open documents by book ID
}

// Open returns the book and its saved position (zero Anchor if none).
func (r *Reading) Open(ctx context.Context, id string) (domain.Book, domain.Progress, error) {
	b, err := r.Books.Get(ctx, id)
	if err != nil {
		return domain.Book{}, domain.Progress{}, err
	}
	p, err := r.Progress.Get(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Book{}, domain.Progress{}, err
	}
	_ = r.Books.Touch(ctx, id, r.Now())
	return b, p, nil
}

// SaveProgress stores the reader's position.
func (r *Reading) SaveProgress(ctx context.Context, id string, a domain.Anchor) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return r.Progress.Save(ctx, domain.Progress{BookID: id, Anchor: a, UpdatedAt: r.Now()})
}

// Document returns the opened document for a book, opening it on first use.
func (r *Reading) Document(ctx context.Context, id string) (Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.docs[id]; ok {
		return d, nil
	}
	b, err := r.Books.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rd, ok := r.Readers[b.Format]
	if !ok {
		return nil, ErrUnsupported
	}
	d, err := rd.Open(b.Path)
	if err != nil {
		return nil, err
	}
	if r.docs == nil {
		r.docs = map[string]Document{}
	}
	r.docs[id] = d
	return d, nil
}

// Heartbeat records reading time. seconds is capped by domain.MaxIdle so a
// forgotten open tab does not inflate statistics.
func (r *Reading) Heartbeat(ctx context.Context, id string, seconds, words int) error {
	if seconds <= 0 {
		return nil
	}
	if max := int(domain.MaxIdle / time.Second); seconds > max {
		seconds = max
	}
	if words < 0 {
		words = 0
	}
	return r.Sessions.Add(ctx, domain.Session{BookID: id, Start: r.Now().Add(-time.Duration(seconds) * time.Second), Seconds: seconds, Words: words})
}

// Stats returns reading statistics for one book, or all books when id is empty.
func (r *Reading) Stats(ctx context.Context, id string) (domain.Stats, error) {
	return r.Sessions.Stats(ctx, id)
}

// AllProgress returns the saved position of every book keyed by book ID.
func (r *Reading) AllProgress(ctx context.Context) (map[string]domain.Progress, error) {
	return r.Progress.All(ctx)
}

// BookTimes returns reading time per book.
func (r *Reading) BookTimes(ctx context.Context) (map[string]domain.BookTime, error) {
	return r.Sessions.ByBook(ctx)
}

// History returns the most recent reading sessions.
func (r *Reading) History(ctx context.Context, id string, limit int) ([]domain.Session, error) {
	return r.Sessions.Recent(ctx, id, limit)
}
