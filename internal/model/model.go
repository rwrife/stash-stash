// Package model holds the core data types shared across stash-stash.
//
// The central type is Stash, a single git stash entry enriched with the
// metadata stash-stash cares about: a human-readable label (or the raw git
// subject until M4 sidecar labels land), the branch it was born on, its age,
// and — in later milestones — a diffstat.
package model

import "time"

// Stash is a single entry from `git stash list`, enriched for display.
//
// At M2 we populate Index, SHA, Subject, Branch, and Created. Later milestones
// add Label (sidecar metadata) and Diffstat.
type Stash struct {
	// Index is the stash's position in the LIFO stack, i.e. the N in
	// stash@{N}. It is 0 for the most recent stash.
	Index int

	// SHA is the full commit SHA of the stash commit. Sidecar metadata (M4+)
	// keys labels by this value so they survive index reshuffling.
	SHA string

	// Subject is the raw one-line subject git records for the stash, e.g.
	// "WIP on main: a1b2c3d Some commit subject" or, for an explicit push,
	// "On main: my message".
	Subject string

	// Branch is the branch the stash was created on, parsed from Subject.
	// Empty if it could not be determined.
	Branch string

	// Created is the stash commit's author time, used to compute age.
	Created time.Time
}

// Ref returns the canonical git reference for the stash, e.g. "stash@{0}".
func (s Stash) Ref() string {
	return "stash@{" + itoa(s.Index) + "}"
}

// itoa is a tiny dependency-free integer formatter for small, non-negative
// stash indices (avoids pulling strconv into this leaf package).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
