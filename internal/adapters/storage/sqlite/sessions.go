package sqlite

import (
	"context"
	"time"

	"github.com/frank5ive/docsreader/internal/domain"
)

func (s *Store) Add(ctx context.Context, x domain.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(book_id,start,seconds,words) VALUES(?,?,?,?)`,
		x.BookID, ts(x.Start), x.Seconds, x.Words)
	return err
}

// Stats aggregates sessions for one book, or all books when bookID is empty.
func (s *Store) Stats(ctx context.Context, bookID string) (domain.Stats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT start, seconds, words FROM sessions WHERE (?1='' OR book_id=?1) ORDER BY start`, bookID)
	if err != nil {
		return domain.Stats{}, err
	}
	defer rows.Close()
	st := domain.Stats{PerDay: map[string]int{}}
	for rows.Next() {
		var start int64
		var sec, words int
		if err := rows.Scan(&start, &sec, &words); err != nil {
			return st, err
		}
		st.TotalSeconds += sec
		st.TotalWords += words
		st.PerDay[time.Unix(start, 0).Format("2006-01-02")] += sec
	}
	if st.TotalSeconds > 0 {
		st.WordsPerMin = float64(st.TotalWords) / (float64(st.TotalSeconds) / 60)
	}
	st.StreakDays = streak(st.PerDay, time.Now())
	return st, rows.Err()
}

// streak counts consecutive days with reading ending today (or yesterday).
func streak(perDay map[string]int, now time.Time) int {
	d := now
	if perDay[d.Format("2006-01-02")] == 0 {
		d = d.AddDate(0, 0, -1)
	}
	n := 0
	for perDay[d.Format("2006-01-02")] > 0 {
		n++
		d = d.AddDate(0, 0, -1)
	}
	return n
}
