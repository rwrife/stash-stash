// Package render produces the non-interactive, plain-text table stash-stash
// prints for non-TTY output (the Bubble Tea TUI handles interactive use). It
// deliberately avoids external dependencies and writes a simple, aligned,
// header-prefixed table. M4 adds the sidecar label (falling back to the raw
// subject) and a diffstat column. M6 adds a "gathering dust" banner above the
// table and a staleness marker so dusty stashes stand out even in plain text
// (where we can't rely on color).
package render

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/model"
)

// maxLabelWidth keeps the table scannable; longer labels/subjects are truncated
// with an ellipsis. The TUI shows full subjects in a preview pane.
const maxLabelWidth = 60

// Banner returns the "gathering dust" nag line for the given stashes, or "" when
// nothing is dusty (or staleness is disabled). It is exported so both the plain
// table and callers that want just the banner can share the exact wording and
// threshold rule.
func Banner(stashes []model.Stash, now time.Time, staleDays int) string {
	if staleDays <= 0 {
		return ""
	}
	dusty := 0
	for _, s := range stashes {
		if age.Classify(s.Created, now, staleDays).Dusty() {
			dusty++
		}
	}
	if dusty == 0 {
		return ""
	}
	noun := "stash is"
	if dusty != 1 {
		noun = "stashes are"
	}
	return fmt.Sprintf("\U0001F9F9 %d %s gathering dust (older than %dd) \u2014 triage them?",
		dusty, noun, staleDays)
}

// Table writes an aligned table of stashes to w using `now` to compute ages.
// When staleDays > 0 it first prints a "gathering dust" banner (if any stash is
// dusty) and flags stale rows with a trailing "*" on the age token.
// Columns: AGE | INDEX | LABEL | BRANCH | CHANGES. LABEL is the sidecar label
// when set, otherwise the raw git subject.
func Table(w io.Writer, stashes []model.Stash, now time.Time, staleDays int) error {
	banner := Banner(stashes, now, staleDays)
	if banner != "" {
		if _, err := fmt.Fprintln(w, banner); err != nil {
			return err
		}
	}

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "AGE\tINDEX\tLABEL\tBRANCH\tCHANGES"); err != nil {
		return err
	}

	for _, s := range stashes {
		branch := s.Branch
		if branch == "" {
			branch = "-"
		}
		changes := s.Diffstat.String()
		if changes == "" {
			changes = "-"
		}
		// Mark dusty rows with a trailing "*" on the age token so the plain
		// table flags staleness without relying on color.
		ageTok := age.Humanize(s.Created, now)
		if age.Classify(s.Created, now, staleDays).Dusty() {
			ageTok += "*"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			ageTok,
			"stash@{"+strconv.Itoa(s.Index)+"}",
			truncate(s.Display(), maxLabelWidth),
			branch,
			changes,
		); err != nil {
			return err
		}
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	// Legend only when we actually flagged something, so clean output stays
	// uncluttered.
	if banner != "" {
		if _, err := fmt.Fprintf(w, "\n(* = stale: older than %dd)\n", staleDays); err != nil {
			return err
		}
	}
	return nil
}

// truncate shortens s to at most max runes, appending "…" when it cuts.
// A max <= 1 returns s unchanged (nothing sensible to truncate to).
func truncate(s string, max int) string {
	if max <= 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}
