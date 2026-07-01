package tui

import (
	"context"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rwrife/stash-stash/internal/model"
)

// Run launches the interactive stash browser and blocks until the user quits.
//
// It is the TUI counterpart to the plain-table path in main: callers should
// only reach here when stdout is a TTY and there is at least one stash. store
// persists relabels (may be nil to disable labeling); actions supplies the
// mutating apply/pop/drop operations plus a reload (a zero Actions disables
// them). staleDays drives the "gathering dust" banner and per-row staleness
// coloring (0 disables). The out/in writers/readers let tests and non-default
// terminals be wired explicitly; pass os.Stdout / os.Stdin for normal use.
// initialFilter (normalized tags) pre-seeds the live tag filter so a `--tag`
// invocation opens the browser already narrowed; pass nil for no filter.
func Run(stashes []model.Stash, show ShowFunc, store labeler, actions Actions, now time.Time, staleDays int, initialFilter []string, in io.Reader, out io.Writer) error {
	m := New(stashes, show, store, actions, now, staleDays, initialFilter)
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ensure ShowFunc lines up with the git.Show signature at compile time when a
// caller wires it; this is a documentation aid, not used at runtime.
var _ ShowFunc = func(ctx context.Context, ref string) (string, error) { return "", nil }

// truncate shortens s to at most max runes, appending "…" when it cuts. A
// max <= 1 returns s unchanged. Mirrors render.truncate so list/preview share
// the same ellipsis behavior without exporting the unexported helper.
func truncate(s string, max int) string {
	if max <= 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// Diff line styles. Kept deliberately muted so the preview reads like a pager,
// not a circus.
var (
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))  // green
	diffDelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203")) // red
	diffHunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // cyan
	diffHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
)

// colorizeDiff applies minimal syntax coloring to a unified diff for the
// preview pane: additions green, deletions red, hunk headers cyan, and file
// headers bold. It is line-oriented and leaves content otherwise untouched.
func colorizeDiff(patch string) string {
	lines := strings.Split(patch, "\n")
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"),
			strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
			lines[i] = diffHeaderStyle.Render(ln)
		case strings.HasPrefix(ln, "@@"):
			lines[i] = diffHunkStyle.Render(ln)
		case strings.HasPrefix(ln, "+"):
			lines[i] = diffAddStyle.Render(ln)
		case strings.HasPrefix(ln, "-"):
			lines[i] = diffDelStyle.Render(ln)
		}
	}
	return strings.Join(lines, "\n")
}
