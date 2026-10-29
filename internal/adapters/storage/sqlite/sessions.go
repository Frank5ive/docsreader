package sqlite

import (
	"context"
	"sort"
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
		st.Sessions++
		st.TotalSeconds += sec
		st.TotalWords += words
		st.PerDay[time.Unix(start, 0).Format("2006-01-02")] += sec
	}
	if st.TotalSeconds > 0 {
		st.WordsPerMin = float64(st.TotalWords) / (float64(st.TotalSeconds) / 60)
	}
	st.StreakDays = streak(st.PerDay, time.Now())
	st.LongestStreak = longestStreak(st.PerDay)
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

func (s *Store) Recent(ctx context.Context, bookID string, limit int) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, book_id, start, seconds, words FROM sessions WHERE (?1='' OR book_id=?1) ORDER BY start DESC LIMIT ?2`, bookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		var x domain.Session
		var start int64
		if err := rows.Scan(&x.ID, &x.BookID, &start, &x.Seconds, &x.Words); err != nil {
			return nil, err
		}
		x.Start = fromTS(start)
		out = append(out, x)
	}
	return out, rows.Err()
}

// longestStreak returns the longest run of consecutive reading days.
func longestStreak(perDay map[string]int) int {
	days := make([]time.Time, 0, len(perDay))
	for k, v := range perDay {
		if v <= 0 {
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02", k, time.Local); err == nil {
			days = append(days, t)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	best, run := 0, 0
	for i, d := range days {
		// AddDate keeps calendar-day arithmetic correct across DST changes.
		if i > 0 && days[i-1].AddDate(0, 0, 1).Equal(d) {
			run++
		} else {
			run = 1
		}
		if run > best {
			best = run
		}
	}
	return best
}

func (s *Store) ByBook(ctx context.Context) (map[string]domain.BookTime, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT book_id, sum(seconds), sum(words) FROM sessions GROUP BY book_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.BookTime{}
	for rows.Next() {
		var id string
		var t domain.BookTime
		if err := rows.Scan(&id, &t.Seconds, &t.Words); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}
