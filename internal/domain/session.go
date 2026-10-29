package domain

import "time"

// Session is one continuous stretch of reading.
type Session struct {
	ID      int64     `json:"id"`
	BookID  string    `json:"bookId"`
	Start   time.Time `json:"start"`
	Seconds int       `json:"seconds"`
	Words   int       `json:"words"`
}

// MaxIdle is the longest gap between heartbeats still counted as reading.
const MaxIdle = 90 * time.Second

// Stats summarises reading activity.
type Stats struct {
	TotalSeconds int            `json:"totalSeconds"`
	TotalWords   int            `json:"totalWords"`
	WordsPerMin  float64        `json:"wordsPerMin"`
	StreakDays   int            `json:"streakDays"`
	PerDay       map[string]int `json:"perDay"` // YYYY-MM-DD -> seconds
}

// Settings are reader preferences, stored as opaque JSON by the UI.
type Settings struct {
	JSON string
}
