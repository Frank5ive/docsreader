package sqlite

import (
	"context"
	"time"

	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

// Annotations returns the app.AnnotationRepository view of the store.
func (s *Store) Annotations() app.AnnotationRepository { return annRepo{s} }

type annRepo struct{ s *Store }

func (r annRepo) Add(ctx context.Context, a domain.Annotation) (domain.Annotation, error) {
	res, err := r.s.db.ExecContext(ctx, `INSERT INTO annotations
		(book_id,kind,section,block,offset,end_block,end_offset,quote,note,color,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.BookID, a.Kind, a.Start.Section, a.Start.Block, a.Start.Offset, a.EndBlock, a.EndOffset,
		a.Quote, a.Note, a.Color, ts(a.CreatedAt))
	if err != nil {
		return a, err
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

func (r annRepo) List(ctx context.Context, bookID string) ([]domain.Annotation, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id,book_id,kind,section,block,offset,end_block,end_offset,quote,note,color,created_at
		FROM annotations WHERE book_id=? ORDER BY section, block, offset, id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Annotation{}
	for rows.Next() {
		var a domain.Annotation
		var created int64
		if err := rows.Scan(&a.ID, &a.BookID, &a.Kind, &a.Start.Section, &a.Start.Block, &a.Start.Offset,
			&a.EndBlock, &a.EndOffset, &a.Quote, &a.Note, &a.Color, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(created, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r annRepo) UpdateNote(ctx context.Context, bookID string, id int64, note string) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE annotations SET note=? WHERE id=? AND book_id=?`, note, id, bookID)
	return affected(res, err)
}

func (r annRepo) Delete(ctx context.Context, bookID string, id int64) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM annotations WHERE id=? AND book_id=?`, id, bookID)
	return affected(res, err)
}

func affected(res interface{ RowsAffected() (int64, error) }, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}
