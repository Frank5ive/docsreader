package sqlite

import (
	"context"
	"html"
	"strings"
	"unicode"

	"github.com/frank5ive/docsreader/internal/app"
)

const ftsSchema = `CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(
	text, book_id UNINDEXED, section UNINDEXED, block UNINDEXED, tokenize='unicode61 remove_diacritics 2');`

// Search returns the app.SearchIndex view of the store.
func (s *Store) Search() app.SearchIndex { return searchIdx{s} }

type searchIdx struct{ s *Store }

func (x searchIdx) Index(ctx context.Context, bookID string, blocks []app.Block) error {
	tx, err := x.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fts WHERE book_id=?`, bookID); err != nil {
		return err
	}
	st, err := tx.PrepareContext(ctx, `INSERT INTO fts(text,book_id,section,block) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		if _, err := st.ExecContext(ctx, b.Text, bookID, b.Section, b.Index); err != nil {
			return err
		}
	}
	// Marker row so books with no text (e.g. PDFs) are not re-indexed on every start.
	if _, err := tx.ExecContext(ctx, `INSERT INTO fts(text,book_id,section,block) VALUES('',?,-1,-1)`, bookID); err != nil {
		return err
	}
	return tx.Commit()
}

func (x searchIdx) Indexed(ctx context.Context, bookID string) (bool, error) {
	var n int
	err := x.s.db.QueryRowContext(ctx, `SELECT count(*) FROM fts WHERE book_id=? AND section=-1`, bookID).Scan(&n)
	return n > 0, err
}

func (x searchIdx) Remove(ctx context.Context, bookID string) error {
	_, err := x.s.db.ExecContext(ctx, `DELETE FROM fts WHERE book_id=?`, bookID)
	return err
}

// matchQuery turns free text into a safe FTS5 expression: every word is quoted
// (so operators and punctuation in user input cannot break the query) and the
// last word is a prefix match so results appear while typing.
func matchQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) == 0 {
		return ""
	}
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " ") + "*"
}

const (
	markOpen  = "\x01"
	markClose = "\x02"
)

func (x searchIdx) Search(ctx context.Context, query, bookID string, limit int) ([]app.Hit, error) {
	m := matchQuery(query)
	if m == "" {
		return []app.Hit{}, nil
	}
	rows, err := x.s.db.QueryContext(ctx, `SELECT f.book_id, b.title, f.section, f.block,
		snippet(fts, 0, ?, ?, '…', 14)
		FROM fts f JOIN books b ON b.id=f.book_id
		WHERE fts MATCH ? AND f.section>=0 AND (?='' OR f.book_id=?)
		ORDER BY rank LIMIT ?`, markOpen, markClose, m, bookID, bookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []app.Hit{}
	for rows.Next() {
		var h app.Hit
		var snip string
		if err := rows.Scan(&h.BookID, &h.Title, &h.Section, &h.Block, &snip); err != nil {
			return nil, err
		}
		// Escape everything, then turn our private markers into <mark> tags.
		snip = html.EscapeString(snip)
		snip = strings.ReplaceAll(strings.ReplaceAll(snip, markOpen, "<mark>"), markClose, "</mark>")
		h.Snippet = snip
		out = append(out, h)
	}
	return out, rows.Err()
}
