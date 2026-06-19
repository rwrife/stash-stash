// Package render produces the non-interactive, plain-text table stash-stash
// prints for non-TTY output (the Bubble Tea TUI handles interactive use). It
// deliberately avoids external dependencies and writes a simple, aligned,
// header-prefixed table. M4 adds the sidecar label (falling back to the raw
// subject) and a diffstat column.
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

// Table writes an aligned table of stashes to w using `now` to compute ages.
// Columns: INDEX | LABEL | AGE | BRANCH | CHANGES. LABEL is the sidecar label
// when set, otherwise the raw git subject.
func Table(w io.Writer, stashes []model.Stash, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "INDEX\tLABEL\tAGE\tBRANCH\tCHANGES"); err != nil {
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
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			"stash@{"+strconv.Itoa(s.Index)+"}",
			truncate(s.Display(), maxLabelWidth),
			age.Humanize(s.Created, now),
			branch,
			changes,
		); err != nil {
			return err
		}
	}

	return tw.Flush()
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
