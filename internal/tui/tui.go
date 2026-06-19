// Package tui implements stash-stash's interactive Bubble Tea interface (M3):
// a scrollable list of stashes on the left and a diff preview pane on the
// right that renders `git stash show -p` for the selected stash.
//
// The model owns no git logic itself; it is handed a list of stashes and a
// "show" function (so it stays unit-testable without a real repo). Selecting a
// different stash kicks off an asynchronous load of its patch, keeping the UI
// responsive on large diffs.
//
// Sidecar labels (M4) and mutating actions (M5) are intentionally out of scope
// here — this milestone is read-only browsing.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/model"
)

// ShowFunc loads the patch for a stash ref (e.g. "stash@{0}"). It mirrors
// git.Show but is injected so the model can be tested with a stub.
type ShowFunc func(ctx context.Context, ref string) (string, error)

// Layout constants. The list pane is a fixed fraction of the width; the
// preview takes the rest. A minimum keeps things sane on tiny terminals.
const (
	minListWidth = 24
	maxListWidth = 48
	listFraction = 40 // percent of total width given to the list pane
	gutter       = 1  // blank column between the two panes
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	listPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	previewPaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 1)

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("63"))

	itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	branchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))

	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1)

	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// diffLoadedMsg carries the result of an asynchronous git.Show for a stash.
// The index ties the result back to a row so a stale, slow load for a
// previously-selected stash can be discarded.
type diffLoadedMsg struct {
	index int
	diff  string
	err   error
}

// Model is the Bubble Tea model for the stash browser.
type Model struct {
	stashes []model.Stash
	show    ShowFunc
	now     time.Time

	cursor   int
	viewport viewport.Model
	ready    bool // viewport sized at least once

	width  int
	height int

	// layout-derived sizes, recomputed on resize.
	listInnerW    int
	previewInnerW int
	innerH        int

	// loadedFor is the stash index currently rendered in the preview, or -1.
	loadedFor   int
	currentDiff string // raw patch for loadedFor
	loading     bool
	loadErr     error
}

// New builds a Model over the given stashes, using show to fetch diffs and now
// to compute ages. The caller is responsible for the empty-stash and
// not-a-repo cases before reaching the TUI.
func New(stashes []model.Stash, show ShowFunc, now time.Time) Model {
	return Model{
		stashes:   stashes,
		show:      show,
		now:       now,
		loadedFor: -1,
	}
}

// Init triggers the first diff load for the top stash.
func (m Model) Init() tea.Cmd {
	if len(m.stashes) == 0 {
		return nil
	}
	return m.loadDiffCmd(m.cursor)
}

// loadDiffCmd returns a command that loads the diff for the stash at idx.
func (m Model) loadDiffCmd(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.stashes) {
		return nil
	}
	ref := m.stashes[idx].Ref()
	show := m.show
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		diff, err := show(ctx, ref)
		return diffLoadedMsg{index: idx, diff: diff, err: err}
	}
}

// Update handles input, resize, and async diff results.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.ready = true
		// Re-render current diff into the freshly-sized viewport.
		if m.loadedFor == m.cursor {
			m.viewport.SetContent(m.renderDiff())
		}
		return m, nil

	case diffLoadedMsg:
		// Ignore results for a stash we've since navigated away from.
		if msg.index != m.cursor {
			return m, nil
		}
		m.loading = false
		m.loadErr = msg.err
		m.loadedFor = msg.index
		m.currentDiff = msg.diff
		m.viewport.SetContent(m.renderDiff())
		m.viewport.GotoTop()
		return m, nil
	}

	// Forward anything else (e.g. mouse) to the viewport for scrolling.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// handleKey processes key presses: navigation, scrolling, and quit.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit

	case "up", "k":
		return m.moveCursor(-1)
	case "down", "j":
		return m.moveCursor(1)
	case "home", "g":
		return m.moveCursorTo(0)
	case "end", "G":
		return m.moveCursorTo(len(m.stashes) - 1)

	// Preview-pane scrolling.
	case "pgup", "ctrl+b":
		m.viewport.HalfViewUp()
		return m, nil
	case "pgdown", "ctrl+f", " ":
		m.viewport.HalfViewDown()
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// moveCursor moves the selection by delta and (if it changed) loads the new
// stash's diff.
func (m Model) moveCursor(delta int) (tea.Model, tea.Cmd) {
	return m.moveCursorTo(m.cursor + delta)
}

// moveCursorTo clamps to a valid row and triggers a diff load when the
// selection actually changes.
func (m Model) moveCursorTo(idx int) (tea.Model, tea.Cmd) {
	if len(m.stashes) == 0 {
		return m, nil
	}
	if idx < 0 {
		idx = 0
	}
	if idx > len(m.stashes)-1 {
		idx = len(m.stashes) - 1
	}
	if idx == m.cursor {
		return m, nil
	}
	m.cursor = idx
	m.loading = true
	m.loadErr = nil
	// Show a transient "loading" state immediately.
	m.viewport.SetContent(dimStyle.Render("Loading diff…"))
	m.viewport.GotoTop()
	return m, m.loadDiffCmd(idx)
}

// layout recomputes pane sizes from the current terminal dimensions.
func (m *Model) layout() {
	listW := m.width * listFraction / 100
	if listW < minListWidth {
		listW = minListWidth
	}
	if listW > maxListWidth {
		listW = maxListWidth
	}
	// Never let the list crowd out the preview on narrow terminals.
	if listW > m.width-minListWidth {
		listW = m.width / 2
	}

	// Account for borders (2) + horizontal padding (2) per pane.
	const chrome = 4
	previewW := m.width - listW - gutter - chrome
	if previewW < 1 {
		previewW = 1
	}

	// Reserve rows for the title (1) and the help line (1), plus pane chrome
	// (border top/bottom = 2).
	innerH := m.height - 2 /*title+help*/ - 2 /*pane border*/
	if innerH < 1 {
		innerH = 1
	}

	if !m.ready {
		m.viewport = viewport.New(previewW, innerH)
	} else {
		m.viewport.Width = previewW
		m.viewport.Height = innerH
	}

	// Stash computed widths for View via styles.
	m.listInnerW = listW
	m.previewInnerW = previewW
	m.innerH = innerH
}

// View renders the full UI: title, the two side-by-side panes, and a help line.
func (m Model) View() string {
	if !m.ready {
		return "Loading stash-stash…"
	}

	title := titleStyle.Render(fmt.Sprintf("🪦 stash-stash — %d %s",
		len(m.stashes), plural(len(m.stashes), "stash", "stashes")))

	list := listPaneStyle.
		Width(m.listInnerW).
		Height(m.innerH).
		Render(m.renderList())

	preview := previewPaneStyle.
		Width(m.previewInnerW).
		Height(m.innerH).
		Render(m.viewport.View())

	panes := lipgloss.JoinHorizontal(lipgloss.Top, list, strings.Repeat(" ", gutter), preview)

	help := helpStyle.Render("↑/↓ select · ⏎/space scroll · g/G top/bottom · q quit")

	return lipgloss.JoinVertical(lipgloss.Left, title, panes, help)
}

// renderList draws the stash rows, highlighting the cursor row.
func (m Model) renderList() string {
	var b strings.Builder
	for i, s := range m.stashes {
		line := m.formatRow(s)
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render(line))
		} else {
			b.WriteString(itemStyle.Render(line))
		}
		if i < len(m.stashes)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// formatRow renders a single stash list entry, sized to the list pane width.
func (m Model) formatRow(s model.Stash) string {
	branch := s.Branch
	if branch == "" {
		branch = "-"
	}
	ageTok := age.Humanize(s.Created, m.now)

	// First line: the ref + age; second line: a truncated subject. Two lines
	// per item keeps long subjects readable in a narrow column.
	head := fmt.Sprintf("%s  %s", s.Ref(), dimStyle.Render(ageTok))
	subj := truncate(s.Subject, m.listInnerW)
	br := branchStyle.Render(truncate("⎇ "+branch, m.listInnerW))

	return head + "\n" + subj + "\n" + br
}

// renderDiff produces the preview pane content for the currently-loaded stash:
// either an error, an empty-diff note, or the patch itself.
func (m Model) renderDiff() string {
	if m.cursor < 0 || m.cursor >= len(m.stashes) {
		return ""
	}
	if m.loadErr != nil {
		return errStyle.Render("Failed to load diff:\n" + m.loadErr.Error())
	}
	loaded := ""
	if m.loadedFor == m.cursor {
		loaded = m.currentDiff
	}
	if strings.TrimSpace(loaded) == "" {
		return dimStyle.Render("(empty diff — nothing to preview)")
	}
	return colorizeDiff(loaded)
}

// plural picks the singular or plural noun for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
