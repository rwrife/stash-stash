// Package age turns timestamps into the short, skimmable strings stash-stash
// shows in its list ("2h", "5d", "23d", "3mo") and classifies a stash's age
// into staleness buckets (fresh / aging / stale / ancient) so the UI can color
// rows and nag about "dusty" stashes.
package age

import (
	"fmt"
	"time"
)

// Staleness is a coarse age bucket used to color a stash row and decide whether
// it counts toward the "gathering dust" nag. Buckets are derived from the
// user's stale threshold (--stale-days): anything at or past the threshold is
// Stale (and Ancient at 2× the threshold), with two younger buckets below it.
type Staleness int

const (
	// Fresh is a brand-new stash (< half the stale threshold). Nothing to see.
	Fresh Staleness = iota
	// Aging is getting on (>= half the threshold, < threshold) — a heads-up,
	// not yet dust.
	Aging
	// Stale is at or past the stale threshold: officially gathering dust.
	Stale
	// Ancient is well past it (>= 2× threshold): triage this or bury it.
	Ancient
)

// String renders the bucket name (handy for --json and tests).
func (s Staleness) String() string {
	switch s {
	case Fresh:
		return "fresh"
	case Aging:
		return "aging"
	case Stale:
		return "stale"
	case Ancient:
		return "ancient"
	default:
		return "unknown"
	}
}

// Dusty reports whether a bucket counts as "gathering dust" for the nag banner
// and stale highlighting: Stale and Ancient do, the younger buckets do not.
func (s Staleness) Dusty() bool { return s >= Stale }

// Classify buckets the age of `then` (as of `now`) against a stale threshold in
// days. A non-positive staleDays disables staleness (everything is Fresh), as
// does a zero/unknown `then`. The thresholds are: Fresh < staleDays/2 <= Aging
// < staleDays <= Stale < 2*staleDays <= Ancient.
func Classify(then, now time.Time, staleDays int) Staleness {
	if staleDays <= 0 || then.IsZero() {
		return Fresh
	}
	d := now.Sub(then)
	if d < 0 {
		return Fresh
	}
	days := d.Hours() / 24
	stale := float64(staleDays)
	switch {
	case days >= 2*stale:
		return Ancient
	case days >= stale:
		return Stale
	case days >= stale/2:
		return Aging
	default:
		return Fresh
	}
}

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
