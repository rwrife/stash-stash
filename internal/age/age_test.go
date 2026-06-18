package age

import (
	"testing"
	"time"
)

func TestHumanize(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		then time.Time
		want string
	}{
		{"seconds", now.Add(-30 * time.Second), "30s"},
		{"minutes", now.Add(-5 * time.Minute), "5m"},
		{"hours", now.Add(-2 * time.Hour), "2h"},
		{"days", now.Add(-5 * 24 * time.Hour), "5d"},
		{"weeks", now.Add(-10 * 24 * time.Hour), "1w"},
		{"months", now.Add(-60 * 24 * time.Hour), "2mo"},
		{"years", now.Add(-800 * 24 * time.Hour), "2y"},
		{"future clamps to now", now.Add(1 * time.Hour), "now"},
	}
	for _, c := range cases {
		if got := Humanize(c.then, now); got != c.want {
			t.Errorf("%s: Humanize = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHumanizeZero(t *testing.T) {
	if got := Humanize(time.Time{}, time.Now()); got != "?" {
		t.Errorf("Humanize(zero) = %q, want %q", got, "?")
	}
}
