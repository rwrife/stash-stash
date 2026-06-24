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
	"github.com/rwrife/stash-stash/internal/search"
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
// when set, otherwise an auto-derived "<area>: <hint>" (flagged with a trailing
// "~" since plain text can't italicize it), otherwise the raw git subject.
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

	sawAuto := false
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
		// Auto-derived labels get a trailing "~" so the plain table distinguishes
		// a guess from a typed name (the TUI uses italics for the same purpose).
		label, src := s.DisplaySource()
		label = truncate(label, maxLabelWidth)
		if src == model.LabelAuto {
			sawAuto = true
			label += " ~"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			ageTok,
			"stash@{"+strconv.Itoa(s.Index)+"}",
			label,
			branch,
			changes,
		); err != nil {
			return err
		}
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	// Legends only when we actually flagged something, so clean output stays
	// uncluttered. Staleness and auto-label legends are independent.
	if banner != "" {
		if _, err := fmt.Fprintf(w, "\n(* = stale: older than %dd)\n", staleDays); err != nil {
			return err
		}
	}
	if sawAuto {
		if _, err := fmt.Fprintln(w, "(~ = auto-label from branch + top file; run `stash-stash push -m` or `l` to name it)"); err != nil {
			return err
		}
	}
	return nil
}

// SearchHit pairs a stash with the matching lines found in its patch, so the
// renderer can print a per-stash header (label · age · branch) followed by the
// matching snippets. It is the unit returned by the command layer's scan of
// each stash's `git stash show -p` output (issue #8).
type SearchHit struct {
	// Stash is the matched stash (for its label, ref, age, and branch).
	Stash model.Stash
	// Matches are the matching lines within that stash's patch, in patch order.
	Matches []search.Match
}

// maxSnippetWidth keeps matching lines from blowing out a terminal; longer
// matched lines are truncated with an ellipsis (the full diff is one
// `stash-stash` / `git stash show` away).
const maxSnippetWidth = 100

// SearchResults writes a grouped, skimmable report of stashes whose contents
// matched a search, using `now` to render each stash's age. For each hit it
// prints a header line — "stash@{N}  <label>  (<age>[, <branch>])" — followed by
// indented matching snippets of the form "<file>:<line>: <±> <text>". term is
// echoed in the summary line so the output is self-describing in a scrollback.
//
// It returns the number of matching lines written across all hits (0 only when
// hits is empty), so the caller can choose an exit message. A nil/empty hits
// slice prints a friendly "no matches" line and returns 0.
func SearchResults(w io.Writer, hits []SearchHit, term string, now time.Time) (int, error) {
	if len(hits) == 0 {
		if _, err := fmt.Fprintf(w, "No stash contents matched %q. 🔍\n", term); err != nil {
			return 0, err
		}
		return 0, nil
	}

	total := 0
	for i, hit := range hits {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return total, err
			}
		}

		label := truncate(hit.Stash.Display(), maxLabelWidth)
		ageTok := age.Humanize(hit.Stash.Created, now)
		meta := ageTok
		if b := hit.Stash.Branch; b != "" {
			meta = ageTok + ", " + b
		}
		countTok := strconv.Itoa(len(hit.Matches)) + " " + plural(len(hit.Matches), "match", "matches")
		if _, err := fmt.Fprintf(w, "%s  %s  (%s) \u2014 %s\n",
			hit.Stash.Ref(), label, meta, countTok); err != nil {
			return total, err
		}

		for _, mt := range hit.Matches {
			loc := mt.File
			if mt.Line > 0 {
				loc += ":" + strconv.Itoa(mt.Line)
			}
			if loc == "" {
				loc = "(unknown)"
			}
			if _, err := fmt.Fprintf(w, "    %s: %s %s\n",
				loc, mt.Kind.Symbol(), truncate(mt.Text, maxSnippetWidth)); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

// plural picks the singular or plural noun for n (kept local so this package
// stays dependency-light, mirroring the helper in internal/model).
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
