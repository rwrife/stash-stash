// Package model holds the core data types shared across stash-stash.
//
// The central type is Stash, a single git stash entry enriched with the
// metadata stash-stash cares about: a human-readable label (or the raw git
// subject until M4 sidecar labels land), the branch it was born on, its age,
// and — in later milestones — a diffstat.
package model

import (
	"time"

	"github.com/rwrife/stash-stash/internal/autolabel"
)

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
	// SHA. Empty when the stash has no stored label; callers fall back to an
	// auto-derived label (see AutoLabel) and then the raw Subject for display.
	Label string

	// TopFile is the stash's most significant changed file path, used to derive
	// an auto-label when no explicit Label exists (issue #7). It is populated by
	// git.EnrichDiffstats alongside Diffstat (same `git stash show --numstat`
	// call) and is empty for a metadata-only stash or before enrichment.
	TopFile string

	// Diffstat summarizes the stash's size (M4). Zero until computed.
	Diffstat Diffstat
}

// AutoLabel returns a derived "<area>: <hint>" label built from the origin
// Branch and the TopFile, used for display when the stash has no explicit
// sidecar Label (issue #7). It returns "" when neither signal is available, so
// callers fall back to the raw Subject. Auto-labels are advisory and never
// persisted: they are recomputed on demand and callers style them distinctly
// (e.g. dim/italic) so a guess is visibly different from a user-chosen name.
func (s Stash) AutoLabel() string {
	return autolabel.Derive(s.Branch, s.TopFile)
}

// LabelSource describes which kind of label Display() returned, so renderers
// and the JSON output can distinguish a user-chosen label from an auto-derived
// guess (or the raw git subject fallback).
type LabelSource int

const (
	// LabelNone means Display() fell back to the raw git Subject.
	LabelNone LabelSource = iota
	// LabelUser means Display() returned an explicit sidecar Label.
	LabelUser
	// LabelAuto means Display() returned an auto-derived "<area>: <hint>".
	LabelAuto
)

// String renders the source as a short, stable token ("user"/"auto"/"subject")
// suitable for the --json output and diagnostics.
func (k LabelSource) String() string {
	switch k {
	case LabelUser:
		return "user"
	case LabelAuto:
		return "auto"
	default:
		return "subject"
	}
}

// DisplaySource returns the best human label for the stash *and* where it came
// from: the sidecar Label when set (LabelUser), otherwise an auto-derived label
// when one can be built (LabelAuto), otherwise the raw git Subject (LabelNone).
// It is the single source of truth for label-fallback precedence so every
// renderer agrees.
func (s Stash) DisplaySource() (string, LabelSource) {
	if s.Label != "" {
		return s.Label, LabelUser
	}
	if auto := s.AutoLabel(); auto != "" {
		return auto, LabelAuto
	}
	return s.Subject, LabelNone
}

// Display returns the best human label for the stash: the sidecar Label when
// set, otherwise an auto-derived "<area>: <hint>" (issue #7), otherwise the raw
// git Subject. This keeps list/table rendering DRY; callers that need to style
// auto-labels differently use DisplaySource instead.
func (s Stash) Display() string {
	text, _ := s.DisplaySource()
	return text
}

// BranchSuggestion returns a safe git branch name suggested for promoting this
// stash to a branch (issue #9): the slug of its display label (its sidecar
// label, else an auto-derived name, else the git subject). When that slugs to
// nothing usable — e.g. a stash whose only "label" is punctuation — it falls
// back to a stable "stash-<index>" so the suggestion is never empty. It is a
// suggestion only; the user can edit it before the branch is created.
func (s Stash) BranchSuggestion() string {
	if slug := autolabel.Slug(s.Display()); slug != "" {
		return slug
	}
	return "stash-" + itoa(s.Index)
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
