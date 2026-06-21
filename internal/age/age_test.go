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

func TestClassify(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	const staleDays = 14
	cases := []struct {
		name string
		then time.Time
		want Staleness
	}{
		{"brand new", now.Add(-1 * time.Hour), Fresh},
		{"just under half", now.Add(-6 * 24 * time.Hour), Fresh},
		{"exactly half is aging", now.Add(-7 * 24 * time.Hour), Aging},
		{"aging", now.Add(-10 * 24 * time.Hour), Aging},
		{"exactly threshold is stale", now.Add(-14 * 24 * time.Hour), Stale},
		{"stale", now.Add(-20 * 24 * time.Hour), Stale},
		{"exactly 2x is ancient", now.Add(-28 * 24 * time.Hour), Ancient},
		{"ancient", now.Add(-100 * 24 * time.Hour), Ancient},
		{"future clamps to fresh", now.Add(24 * time.Hour), Fresh},
	}
	for _, c := range cases {
		if got := Classify(c.then, now, staleDays); got != c.want {
			t.Errorf("%s: Classify = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassifyDisabled(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	old := now.Add(-365 * 24 * time.Hour)
	// Non-positive staleDays disables staleness entirely.
	if got := Classify(old, now, 0); got != Fresh {
		t.Errorf("Classify with staleDays=0 = %v, want Fresh", got)
	}
	if got := Classify(old, now, -5); got != Fresh {
		t.Errorf("Classify with negative staleDays = %v, want Fresh", got)
	}
	// A zero/unknown timestamp is never stale.
	if got := Classify(time.Time{}, now, 14); got != Fresh {
		t.Errorf("Classify(zero time) = %v, want Fresh", got)
	}
}

func TestStalenessStringAndDusty(t *testing.T) {
	cases := []struct {
		s     Staleness
		name  string
		dusty bool
	}{
		{Fresh, "fresh", false},
		{Aging, "aging", false},
		{Stale, "stale", true},
		{Ancient, "ancient", true},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.name {
			t.Errorf("%v.String() = %q, want %q", c.s, got, c.name)
		}
		if got := c.s.Dusty(); got != c.dusty {
			t.Errorf("%v.Dusty() = %v, want %v", c.s, got, c.dusty)
		}
	}
}
