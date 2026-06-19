// Package tui implements stash-stash's interactive Bubble Tea interface: a
// scrollable list of stashes on the left and a diff preview pane on the right
// that renders `git stash show -p` for the selected stash.
//
// The model owns no git logic itself; it is handed a list of stashes and a
// "show" function (so it stays unit-testable without a real repo). Selecting a
// different stash kicks off an asynchronous load of its patch, keeping the UI
// responsive on large diffs.
//
// M4 adds sidecar labels: each stash shows its stored label (or the raw git
// subject) plus a diffstat, and `l` opens an inline editor to (re)label the
// selected stash, persisted to `.git/stash-stash.json` by content SHA so the
// label survives stash reordering. Mutating actions (M5) remain out of scope.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/model"
)

// ShowFunc loads the patch for a stash ref (e.g. "stash@{0}"). It mirrors
// git.Show but is injected so the model can be tested with a stub.
type ShowFunc func(ctx context.Context, ref string) (string, error)

// labeler is the subset of *meta.Store the TUI needs to persist relabels. An
// interface keeps the model testable with an in-memory fake and lets a nil
// store disable labeling gracefully.
type labeler interface {
	SetLabel(sha, label string)
	Save() error
}

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

	// labelStyle highlights a stored sidecar label so it reads as a real name
	// rather than the raw git subject.
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))

	// statStyle dims the diffstat token in the list.
	statStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))

	// editPromptStyle frames the inline label editor row.
	editPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Padding(0, 1)
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

	// --- M4 labeling ---
	store   labeler         // sidecar persistence; nil disables labeling
	editing bool            // true while the inline label editor is open
	input   textinput.Model // the label text field (valid while editing)
	notice  string          // transient status line (e.g. "saved"/error)
}

// New builds a Model over the given stashes, using show to fetch diffs, store
// to persist relabels (may be nil to disable labeling), and now to compute
// ages. The caller is responsible for the empty-stash and not-a-repo cases
// before reaching the TUI.
func New(stashes []model.Stash, show ShowFunc, store labeler, now time.Time) Model {
	return Model{
		stashes:   stashes,
		show:      show,
		store:     store,
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

// handleKey processes key presses: navigation, scrolling, labeling, and quit.
// While the inline label editor is open, keys are routed to it instead.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.handleEditKey(msg)
	}

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

	case "l":
		return m.beginEdit()

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

// beginEdit opens the inline label editor for the selected stash, seeding it
// with the current label (empty for an unlabeled stash). It is a no-op when
// labeling is disabled (no store) or there is no selection.
func (m Model) beginEdit() (tea.Model, tea.Cmd) {
	if m.store == nil || len(m.stashes) == 0 {
		m.notice = "labeling unavailable (no sidecar)"
		return m, nil
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 120
	ti.SetValue(m.stashes[m.cursor].Label)
	ti.CursorEnd()
	// Width is set to the list inner width minus the prompt label in View;
	// a sane default keeps it usable before the first resize.
	ti.Width = m.listInnerW
	if ti.Width < 8 {
		ti.Width = 8
	}
	ti.Focus()
	m.input = ti
	m.editing = true
	m.notice = ""
	return m, textinput.Blink
}

// handleEditKey drives the inline label editor: Enter commits, Esc cancels,
// everything else edits the text field.
func (m Model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.commitEdit()
	case tea.KeyEsc, tea.KeyCtrlC:
		m.editing = false
		m.notice = "label edit cancelled"
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// commitEdit persists the edited label to the sidecar (keyed by the stash's
// content SHA) and updates the in-memory stash so the list reflects it
// immediately. Trimming to empty clears the label. Save failures surface in
// the notice line but never crash the TUI.
func (m Model) commitEdit() (tea.Model, tea.Cmd) {
	m.editing = false
	if len(m.stashes) == 0 || m.store == nil {
		return m, nil
	}
	label := strings.TrimSpace(m.input.Value())
	sha := m.stashes[m.cursor].SHA
	m.store.SetLabel(sha, label)
	m.stashes[m.cursor].Label = label
	if err := m.store.Save(); err != nil {
		m.notice = "save failed: " + err.Error()
		return m, nil
	}
	if label == "" {
		m.notice = "label cleared"
	} else {
		m.notice = "label saved"
	}
	return m, nil
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

	return lipgloss.JoinVertical(lipgloss.Left, title, panes, m.footer())
}

// footer renders the bottom line of the UI: either the inline label editor
// (while editing), a transient notice, or the key help.
func (m Model) footer() string {
	if m.editing {
		label := "label"
		if m.cursor >= 0 && m.cursor < len(m.stashes) {
			label = "label " + m.stashes[m.cursor].Ref()
		}
		return editPromptStyle.Render(label+": ") + m.input.View() +
			helpStyle.Render("  ⏎ save · esc cancel")
	}
	if m.notice != "" {
		return helpStyle.Render(m.notice)
	}
	return helpStyle.Render("↑/↓ select · l label · ⏎/space scroll · g/G top/bottom · q quit")
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
// Line 1: ref + age + diffstat. Line 2: the label (highlighted) or raw
// subject. Line 3: the origin branch.
func (m Model) formatRow(s model.Stash) string {
	branch := s.Branch
	if branch == "" {
		branch = "-"
	}
	ageTok := age.Humanize(s.Created, m.now)

	// First line: ref + age, plus the diffstat when there is one. Keeps the
	// most-skimmable facts together.
	head := fmt.Sprintf("%s  %s", s.Ref(), dimStyle.Render(ageTok))
	if stat := s.Diffstat.String(); stat != "" {
		head += "  " + statStyle.Render(stat)
	}

	// Second line: the human label if present (highlighted), else the raw
	// git subject.
	var subj string
	if s.Label != "" {
		subj = labelStyle.Render(truncate(s.Label, m.listInnerW))
	} else {
		subj = truncate(s.Subject, m.listInnerW)
	}
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
