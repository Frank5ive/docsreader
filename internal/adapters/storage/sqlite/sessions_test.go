package sqlite

import (
	"testing"
	"time"
)

func TestStreaks(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	day := func(back int) string { return now.AddDate(0, 0, -back).Format("2006-01-02") }
	per := map[string]int{day(0): 60, day(1): 60, day(2): 60, day(5): 60, day(6): 60, day(7): 60, day(8): 60, day(9): 0}

	if got := streak(per, now); got != 3 {
		t.Errorf("streak = %d, want 3", got)
	}
	if got := longestStreak(per); got != 4 {
		t.Errorf("longest = %d, want 4", got)
	}
	// Not yet read today: yesterday's run still counts.
	delete(per, day(0))
	if got := streak(per, now); got != 2 {
		t.Errorf("streak without today = %d, want 2", got)
	}
	if got := longestStreak(map[string]int{}); got != 0 {
		t.Errorf("empty longest = %d", got)
	}
}
