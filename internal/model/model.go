// Package model holds the core data types shared across stash-stash.
//
// The central type is Stash, a single git stash entry enriched with the
// metadata stash-stash cares about: a human-readable label (or the raw git
// subject until M4 sidecar labels land), the branch it was born on, its age,
// and — in later milestones — a diffstat.
package model

import "time"

// Diffstat summarizes how much a stash changes: how many lines were added and
// removed and how many files were touched. It is derived from
// `git stash show --numstat` (M4).
type Diffstat struct {
	// Added is the total number of inserted lines across all files.
	Added int

	// Deleted is the total number of removed lines across all files.
	Deleted int

	// Files is the number of files the stash touches.
	Files int

	// Binary reports whether any touched file was binary (git reports "-"
	Binary bool
}

// IsZero reports whether the diffstat carries no information (e.g. it was never
// computed, or the stash is genuinely empty).
func (d Diffstat) IsZero() bool {
	return d.Added == 0 && d.Deleted == 0 && d.Files == 0 && !d.Binary
}

// String renders a compact, skimmable diffstat token like "+12 -3 · 2 files"
// (or "1 file" / "bin"). It returns "" for a zero diffstat so callers can omit
// it entirely. The middot keeps it readable in a narrow list column.
func (d Diffstat) String() string {
	if d.IsZero() {
		return ""
	}
	filesTok := itoa(d.Files) + " " + plural(d.Files, "file", "files")
	if d.Added == 0 && d.Deleted == 0 {
		// Binary-only change (no line counts) — show files (+ a bin marker).
		if d.Binary {
			return filesTok + " · bin"
		}
		return filesTok
	}
	counts := "+" + itoa(d.Added) + " -" + itoa(d.Deleted)
	if d.Binary {
		counts += " bin"
	}
	return counts + " · " + filesTok
}

// plural picks the singular or plural noun for n. Kept here (dependency-free)
// so this leaf package stays import-light.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Stash is a single entry from `git stash list`, enriched for display.
//
// At M2 we populate Index, SHA, Subject, Branch, and Created. M4 adds Label
// (from sidecar metadata) and Diffstat.
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

	// Label is the human-friendly name from sidecar metadata (M4), matched by
	// SHA. Empty when the stash has no stored label; callers fall back to
	// Subject for display.
	Label string

	// Diffstat summarizes the stash's size (M4). Zero until computed.
	Diffstat Diffstat
}

// Display returns the best human label for the stash: the sidecar Label when
// set, otherwise the raw git Subject. This keeps list/table rendering DRY.
func (s Stash) Display() string {
	if s.Label != "" {
		return s.Label
	}
	return s.Subject
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
