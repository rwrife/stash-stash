// Package render produces the non-interactive, plain-text table stash-stash
// prints at M2 (the Bubble Tea TUI arrives in M3). It deliberately avoids
// external dependencies and writes a simple, aligned, header-prefixed table.
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

// maxSubjectWidth keeps the table scannable; longer subjects are truncated
// with an ellipsis. The TUI (M3) will show full subjects in a preview pane.
const maxSubjectWidth = 60

// Table writes an aligned table of stashes to w using `now` to compute ages.
// Columns: INDEX | SUBJECT | AGE | BRANCH.
func Table(w io.Writer, stashes []model.Stash, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "INDEX\tSUBJECT\tAGE\tBRANCH"); err != nil {
		return err
	}

	for _, s := range stashes {
		branch := s.Branch
		if branch == "" {
			branch = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			"stash@{"+strconv.Itoa(s.Index)+"}",
			truncate(s.Subject, maxSubjectWidth),
			age.Humanize(s.Created, now),
			branch,
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
