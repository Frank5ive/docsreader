// Package sqlite implements the repositories on top of a pure-Go SQLite driver.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS books (
	id TEXT PRIMARY KEY, title TEXT NOT NULL, author TEXT NOT NULL, format TEXT NOT NULL,
	path TEXT NOT NULL, sections INTEGER NOT NULL, words INTEGER NOT NULL,
	has_cover INTEGER NOT NULL, added_at INTEGER NOT NULL, opened_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS progress (
	book_id TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
	section INTEGER NOT NULL, block INTEGER NOT NULL, offset INTEGER NOT NULL,
	percent REAL NOT NULL, quote TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	start INTEGER NOT NULL, seconds INTEGER NOT NULL, words INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS sessions_book ON sessions(book_id);
CREATE TABLE IF NOT EXISTS book_tags (
	book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	tag TEXT NOT NULL, PRIMARY KEY (book_id, tag));
CREATE TABLE IF NOT EXISTS annotations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	kind TEXT NOT NULL, section INTEGER NOT NULL, block INTEGER NOT NULL, offset INTEGER NOT NULL,
	end_block INTEGER NOT NULL, end_offset INTEGER NOT NULL,
	quote TEXT NOT NULL, note TEXT NOT NULL, color TEXT NOT NULL, created_at INTEGER NOT NULL);
`

// Store implements app.BookRepository, app.ProgressRepository and app.SessionRepository.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + ftsSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromTS(u int64) time.Time {
	if u == 0 {
		return time.Time{}
	}
	return time.Unix(u, 0)
}

func (s *Store) Save(ctx context.Context, b domain.Book) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO books(id,title,author,format,path,sections,words,has_cover,added_at,opened_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, author=excluded.author, path=excluded.path,
		sections=excluded.sections, words=excluded.words, has_cover=excluded.has_cover`,
		b.ID, b.Title, b.Author, b.Format, b.Path, b.Sections, b.WordCount, b.HasCover, ts(b.AddedAt), ts(b.OpenedAt))
	return err
}

const bookCols = `id,title,author,format,path,sections,words,has_cover,added_at,opened_at`

type scanner interface{ Scan(...any) error }

func scanBook(r scanner) (domain.Book, error) {
	var b domain.Book
	var added, opened int64
	err := r.Scan(&b.ID, &b.Title, &b.Author, &b.Format, &b.Path, &b.Sections, &b.WordCount, &b.HasCover, &added, &opened)
	b.AddedAt, b.OpenedAt = fromTS(added), fromTS(opened)
	return b, err
}

func (s *Store) Get(ctx context.Context, id string) (domain.Book, error) {
	b, err := scanBook(s.db.QueryRowContext(ctx, `SELECT `+bookCols+` FROM books WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return b, app.ErrNotFound
	}
	if err == nil {
		tags, terr := s.tags(ctx)
		b.Tags, err = tags[id], terr
	}
	return b, err
}

func (s *Store) List(ctx context.Context) ([]domain.Book, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+bookCols+` FROM books ORDER BY opened_at DESC, added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := s.tags(ctx)
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, err
}

// tags returns every book's tags keyed by book ID, sorted.
func (s *Store) tags(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT book_id, tag FROM book_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		out[id] = append(out[id], tag)
	}
	return out, rows.Err()
}

// SetTags replaces the tags of a book.
func (s *Store) SetTags(ctx context.Context, id string, tags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_tags WHERE book_id=?`, id); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO book_tags(book_id, tag) VALUES(?,?)`, id, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Snapshot writes a consistent copy of the database to dst (which must not exist).
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}

func (s *Store) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE books SET opened_at=? WHERE id=?`, ts(at), id)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM books WHERE id=?`, id)
	return err
}

// ProgressRepository

func (s *Store) progressSave(ctx context.Context, p domain.Progress) error {
	a := p.Anchor
	_, err := s.db.ExecContext(ctx, `INSERT INTO progress(book_id,section,block,offset,percent,quote,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(book_id) DO UPDATE SET section=excluded.section, block=excluded.block, offset=excluded.offset,
		percent=excluded.percent, quote=excluded.quote, updated_at=excluded.updated_at`,
		p.BookID, a.Section, a.Block, a.Offset, a.Percent, a.Quote, ts(p.UpdatedAt))
	return err
}

func scanProgress(r scanner) (domain.Progress, error) {
	var p domain.Progress
	var u int64
	err := r.Scan(&p.BookID, &p.Anchor.Section, &p.Anchor.Block, &p.Anchor.Offset, &p.Anchor.Percent, &p.Anchor.Quote, &u)
	p.UpdatedAt = fromTS(u)
	return p, err
}

const progCols = `book_id,section,block,offset,percent,quote,updated_at`

func (s *Store) progressGet(ctx context.Context, id string) (domain.Progress, error) {
	p, err := scanProgress(s.db.QueryRowContext(ctx, `SELECT `+progCols+` FROM progress WHERE book_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Progress{BookID: id}, app.ErrNotFound
	}
	return p, err
}

func (s *Store) progressAll(ctx context.Context) (map[string]domain.Progress, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+progCols+` FROM progress`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.Progress{}
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		out[p.BookID] = p
	}
	return out, rows.Err()
}

// Progress returns the app.ProgressRepository view of the store.
func (s *Store) Progress() app.ProgressRepository { return progressRepo{s} }

type progressRepo struct{ s *Store }

func (r progressRepo) Save(ctx context.Context, p domain.Progress) error {
	return r.s.progressSave(ctx, p)
}
func (r progressRepo) Get(ctx context.Context, id string) (domain.Progress, error) {
	return r.s.progressGet(ctx, id)
}
func (r progressRepo) All(ctx context.Context) (map[string]domain.Progress, error) {
	return r.s.progressAll(ctx)
}
