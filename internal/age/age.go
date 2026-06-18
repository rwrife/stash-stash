// Package age turns timestamps into the short, skimmable strings stash-stash
// shows in its list ("2h", "5d", "23d", "3mo"). Staleness color buckets
// (M6) will also live here; M2 only needs the humanizer.
package age

import (
	"fmt"
	"time"
)

// Humanize renders the duration between then and now as a compact, single-token
// age like "12s", "5m", "2h", "5d", "3w", "4mo", or "2y". It always rounds down
// to the dominant unit so the list stays narrow and scannable.
//
// A zero or future `then` (clock skew, or a not-yet-known time) renders as "now".
func Humanize(then, now time.Time) string {
	if then.IsZero() {
		return "?"
	}
	d := now.Sub(then)
	if d < 0 {
		return "now"
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}

	days := int(d.Hours()) / 24
	switch {
	case days < 7:
		return fmt.Sprintf("%dd", days)
	case days < 30:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}
