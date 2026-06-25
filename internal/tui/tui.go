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
// label survives stash reordering.
//
// M5 adds mutating actions: `a` applies the selected stash (non-destructive),
// `p` pops it, and `d` drops it. Because pop and drop destroy or move work,
// they are gated behind a y/n confirm prompt; apply runs immediately. After a
// successful mutation the list is reloaded from git (indices shift) and the
// sidecar entry for a removed stash is pruned, so the metadata never drifts
// from reality. Every action lands a transient success/error toast.
//
// Issue #9 adds `b`: promote the selected stash to a real branch. It opens an
// inline editor pre-filled with a slugified branch name suggested from the
// stash's label, then runs `git stash branch <name> <ref>`. A successful branch
// applies and removes the stash, so (like pop/drop) the list is reloaded and the
// sidecar entry pruned.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

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

// ActionFunc performs a mutating git operation on a stash ref (apply/pop/drop).
// It mirrors git.Apply/Pop/Drop and is injected so the model is testable
// without a real repo.
type ActionFunc func(ctx context.Context, ref string) error

// BranchFunc promotes a stash ref to a new branch (issue #9). It mirrors
// git.Branch (name + ref) and is injected so the model is testable without a
// real repo. A successful call removes the stash from the stack, so the caller
// reloads and prunes afterwards as it does for pop/drop.
type BranchFunc func(ctx context.Context, name, ref string) error

// ReloadFunc returns the current, freshly-enriched stash list (labels +
// diffstats applied), used after a pop/drop to resync the view with git since
// stash@{N} indices shift. It mirrors the load path in main and is injected so
// the model can be tested deterministically.
type ReloadFunc func(ctx context.Context) ([]model.Stash, error)

// Actions bundles the mutating operations the TUI can perform. A zero Actions
// (all-nil funcs) cleanly disables the action keys (they report "unavailable"),
// which is handy in tests and non-mutating contexts.
type Actions struct {
	Apply  ActionFunc
	Pop    ActionFunc
	Drop   ActionFunc
	Branch BranchFunc
	Reload ReloadFunc
}

// labeler is the subset of *meta.Store the TUI needs to persist relabels and
// prune removed stashes. An interface keeps the model testable with an
// in-memory fake and lets a nil store disable labeling gracefully.
type labeler interface {
	SetLabel(sha, label string)
	Save() error
}

// pruner is the optional subset of *meta.Store used to drop the sidecar entry
// for a stash removed by pop/drop, keyed by content SHA. *meta.Store satisfies
// it via Prune; the TUI feeds Prune the set of still-live SHAs.
type pruner interface {
	Prune(liveSHAs map[string]struct{}) int
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

	// autoLabelStyle renders an auto-derived label (issue #7) dim + italic so it
	// is visibly a guess, not a name the user typed (which uses labelStyle).
	autoLabelStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))

	// statStyle dims the diffstat token in the list.
	statStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))

	// editPromptStyle frames the inline label editor row.
	editPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Padding(0, 1)

	// confirmStyle frames the destructive-action y/N prompt so it reads as a
	// warning, not a casual hint.
	confirmStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")).Padding(0, 1)

	// --- M6 staleness ---
	// bannerStyle frames the "gathering dust" nag in the title row: amber on
	// nothing, bold, so it reads as a gentle alert.
	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Padding(0, 1)

	// Age token colors by staleness bucket, applied to the age in each row.
	// Fresh stays dim (no special attention), aging goes amber, stale orange,
	// ancient red — a clear "the older, the hotter" ramp.
	ageFreshStyle   = dimStyle
	ageAgingStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))            // soft amber
	ageStaleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))            // orange
	ageAncientStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")) // red
)

// diffLoadedMsg carries the result of an asynchronous git.Show for a stash.
// The index ties the result back to a row so a stale, slow load for a
// previously-selected stash can be discarded.
type diffLoadedMsg struct {
	index int
	diff  string
	err   error
}

// actionKind identifies which mutating action an actionDoneMsg refers to, so
// the toast can name it ("applied"/"popped"/"dropped"/"branched").
type actionKind int

const (
	actionApply actionKind = iota
	actionPop
	actionDrop
	actionBranch
)

func (a actionKind) verb() string {
	switch a {
	case actionApply:
		return "applied"
	case actionPop:
		return "popped"
	case actionDrop:
		return "dropped"
	case actionBranch:
		return "branched"
	default:
		return "done"
	}
}

// imperative returns the present-tense command word for prompts ("apply",
// "pop", "drop", "branch").
func (a actionKind) imperative() string {
	switch a {
	case actionApply:
		return "apply"
	case actionPop:
		return "pop"
	case actionDrop:
		return "drop"
	case actionBranch:
		return "branch"
	default:
		return "act on"
	}
}

// consequence is a short, honest description of what a destructive action does,
// shown in the confirm prompt so the user knows the stakes.
func (a actionKind) consequence() string {
	switch a {
	case actionPop:
		return "applies then removes it from the stack"
	case actionDrop:
		return "deletes it (recoverable only via reflog)"
	default:
		return ""
	}
}

// needsConfirm reports whether the action opens a y/N confirm prompt before
// running (pop and drop). Apply runs immediately; branch instead gates on the
// inline branch-name editor, so neither needs the y/N prompt.
func (a actionKind) needsConfirm() bool { return a == actionPop || a == actionDrop }

// removesStash reports whether a successful action removes the stash from the
// stack and therefore requires a post-op reload + sidecar prune (pop, drop, and
// branch — which applies-then-drops). Apply leaves the stack unchanged.
func (a actionKind) removesStash() bool {
	return a == actionPop || a == actionDrop || a == actionBranch
}

// actionDoneMsg carries the result of a mutating git op plus the context
// needed to update the view: which stash (by SHA, since the index it had may
// now be invalid) and a freshly-reloaded stash list when the mutation
// succeeded (nil on error). reloadErr is set if the post-mutation reload
// itself failed even though the mutation succeeded.
type actionDoneMsg struct {
	kind      actionKind
	sha       string
	ref       string
	err       error
	reloaded  []model.Stash
	reloadErr error
	didMutate bool // the git op succeeded (so the stash set changed)
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

	// --- issue #9: stash -> branch ---
	// branching is true while the inline branch-name editor is open. It shares
	// the `input` field with the label editor but commits to a different action
	// (`git stash branch`) rather than a sidecar relabel, so the two modes are
	// tracked separately and are mutually exclusive.
	branching bool

	// --- M5 actions ---
	actions    Actions    // mutating ops (apply/pop/drop) + reload; zero disables
	confirming bool       // true while a y/n destructive-action prompt is open
	pending    actionKind // which destructive action awaits confirmation
	busy       bool       // true while a mutating op is in flight (input locked)

	// --- M6 staleness ---
	staleDays int // age threshold (days) for "gathering dust"; 0 disables
}

// New builds a Model over the given stashes, using show to fetch diffs, store
// to persist relabels (may be nil to disable labeling), actions to perform the
// mutating apply/pop/drop operations (a zero Actions disables them), now to
// compute ages, and staleDays as the staleness threshold (0 disables the nag
// and stale coloring). The caller is responsible for the empty-stash and
// not-a-repo cases before reaching the TUI.
func New(stashes []model.Stash, show ShowFunc, store labeler, actions Actions, now time.Time, staleDays int) Model {
	return Model{
		stashes:   stashes,
		show:      show,
		store:     store,
		actions:   actions,
		now:       now,
		staleDays: staleDays,
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

	case actionDoneMsg:
		return m.handleActionDone(msg)
	}

	// Forward anything else (e.g. mouse) to the viewport for scrolling.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// handleKey processes key presses: navigation, scrolling, labeling, actions,
// and quit. While the inline label/branch editor or a confirm prompt is open,
// keys are routed there instead; while a mutation is in flight the UI is
// input-locked except for quit.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.handleEditKey(msg)
	}
	if m.branching {
		return m.handleBranchKey(msg)
	}
	if m.confirming {
		return m.handleConfirmKey(msg)
	}
	if m.busy {
		// Allow only a hard quit while an action is running.
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
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
	case "b":
		return m.beginBranch()

	case "a":
		return m.startAction(actionApply)
	case "p":
		return m.startAction(actionPop)
	case "d":
		return m.startAction(actionDrop)

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

// beginBranch opens the inline branch-name editor for the selected stash,
// seeding it with a slugified suggestion derived from the stash's label
// (issue #9). Committing it runs `git stash branch`. It is a no-op (with a
// toast) when the branch action is unavailable (no injected func) or there is
// no selection. Unlike labeling it does not require a sidecar store — promoting
// a stash to a branch is a pure git operation.
func (m Model) beginBranch() (tea.Model, tea.Cmd) {
	if len(m.stashes) == 0 {
		return m, nil
	}
	if m.actions.Branch == nil {
		m.notice = "branch: unavailable"
		return m, nil
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 120
	ti.SetValue(m.stashes[m.cursor].BranchSuggestion())
	ti.CursorEnd()
	ti.Width = m.listInnerW
	if ti.Width < 8 {
		ti.Width = 8
	}
	ti.Focus()
	m.input = ti
	m.branching = true
	m.notice = ""
	return m, textinput.Blink
}

// handleBranchKey drives the inline branch-name editor: Enter commits (creates
// the branch), Esc cancels, everything else edits the text field.
func (m Model) handleBranchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.commitBranch()
	case tea.KeyEsc, tea.KeyCtrlC:
		m.branching = false
		m.notice = "branch cancelled"
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// commitBranch closes the branch-name editor and fires `git stash branch` with
// the entered name against the selected stash. An empty name is rejected with a
// toast (git would otherwise misparse the ref as the branch name) and the
// editor simply closes so the user can retry. On success the stash is applied
// to the new branch and removed, so the result is handled like a pop/drop.
func (m Model) commitBranch() (tea.Model, tea.Cmd) {
	m.branching = false
	if len(m.stashes) == 0 || m.actions.Branch == nil {
		return m, nil
	}
	name := strings.TrimSpace(m.input.Value())
	if name == "" {
		m.notice = "branch name required"
		return m, nil
	}
	return m.fireBranch(name)
}

// moveCursor moves the selection by delta and (if it changed) loads the new
// stash's diff.
func (m Model) moveCursor(delta int) (tea.Model, tea.Cmd) {

	return m.moveCursorTo(m.cursor + delta)
}

// startAction begins a mutating action on the selected stash. Apply runs
// immediately (non-destructive); pop and drop open a y/n confirm prompt first.
// (Branch is not routed here — it has its own inline name editor.) It is a
// no-op (with an explanatory toast) when the action is unavailable (no injected
// func) or there is no selection.
func (m Model) startAction(kind actionKind) (tea.Model, tea.Cmd) {
	if len(m.stashes) == 0 {
		return m, nil
	}
	if m.actionFunc(kind) == nil {
		m.notice = kind.verb() + ": unavailable"
		return m, nil
	}
	m.notice = ""
	if kind.needsConfirm() {
		m.confirming = true
		m.pending = kind
		return m, nil
	}
	return m.fireAction(kind)
}

// actionFunc returns the injected func for a kind, or nil if unavailable.
func (m Model) actionFunc(kind actionKind) ActionFunc {
	switch kind {
	case actionApply:
		return m.actions.Apply
	case actionPop:
		return m.actions.Pop
	case actionDrop:
		return m.actions.Drop
	default:
		return nil
	}
}

// handleConfirmKey drives the destructive-action confirm prompt: 'y' fires the
// pending action, anything else (n/esc/q/…) cancels it.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		kind := m.pending
		m.confirming = false
		return m.fireAction(kind)
	default:
		m.confirming = false
		m.notice = m.pending.verb() + " cancelled"
		return m, nil
	}
}

// fireAction locks the UI and dispatches the async command that runs the git
// op against the selected stash. The captured ref/SHA travel with the result
// so the post-mutation handler can act even though indices may shift.
func (m Model) fireAction(kind actionKind) (tea.Model, tea.Cmd) {
	if len(m.stashes) == 0 {
		return m, nil
	}
	s := m.stashes[m.cursor]
	m.busy = true
	m.notice = capitalize(m.gerund(kind)) + " " + s.Ref() + "…"
	return m, m.runActionCmd(kind, s.Ref(), s.SHA)
}

// fireBranch locks the UI and dispatches the async `git stash branch` op,
// creating branch name from the selected stash. Like fireAction it captures the
// ref/SHA so the post-op handler can prune + resync even as indices shift; the
// branch name travels in the closure. A successful branch removes the stash, so
// the result is handled exactly like a pop/drop (reload + prune).
func (m Model) fireBranch(name string) (tea.Model, tea.Cmd) {
	if len(m.stashes) == 0 {
		return m, nil
	}
	s := m.stashes[m.cursor]
	m.busy = true
	m.notice = "Branching " + s.Ref() + " → " + name + "…"
	return m, m.runBranchCmd(name, s.Ref(), s.SHA)
}

// runBranchCmd returns a command that runs `git stash branch <name> <ref>` and,
// on success, reloads the stash list (branch applies-then-drops, so the stack
// shrank). It mirrors runActionCmd but binds the branch name and uses the
// injected BranchFunc.
func (m Model) runBranchCmd(name, ref, sha string) tea.Cmd {
	fn := m.actions.Branch
	reload := m.actions.Reload
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fn(ctx, name, ref); err != nil {
			return actionDoneMsg{kind: actionBranch, sha: sha, ref: ref, err: err}
		}
		res := actionDoneMsg{kind: actionBranch, sha: sha, ref: ref, didMutate: true}
		if reload != nil {
			stashes, rerr := reload(ctx)
			res.reloaded = stashes
			res.reloadErr = rerr
		}
		return res
	}
}

// gerund returns the "-ing" form of an action for the in-flight toast.
func (m Model) gerund(kind actionKind) string {
	switch kind {
	case actionApply:
		return "applying"
	case actionPop:
		return "popping"
	case actionDrop:
		return "dropping"
	case actionBranch:
		return "branching"
	default:
		return "working"
	}
}

// runActionCmd returns a command that performs the mutating git op and, on
// success for a pop/drop/branch, reloads the stash list so indices resync.
// Apply does not change the stash set, so it skips the reload.
func (m Model) runActionCmd(kind actionKind, ref, sha string) tea.Cmd {
	fn := m.actionFunc(kind)
	reload := m.actions.Reload
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fn(ctx, ref); err != nil {
			return actionDoneMsg{kind: kind, sha: sha, ref: ref, err: err}
		}
		res := actionDoneMsg{kind: kind, sha: sha, ref: ref, didMutate: true}
		// Apply leaves the stack unchanged; pop/drop/branch need a resync.
		if kind.removesStash() && reload != nil {
			stashes, rerr := reload(ctx)
			res.reloaded = stashes
			res.reloadErr = rerr
		}
		return res
	}
}

// handleActionDone applies the result of a mutating op: it sets the toast,
// and for a successful pop/drop/branch swaps in the reloaded stash list, prunes
// the sidecar entry for the removed stash, clamps the cursor, and reloads the
// preview for the newly-selected stash.
func (m Model) handleActionDone(msg actionDoneMsg) (tea.Model, tea.Cmd) {
	m.busy = false

	if msg.err != nil {
		m.notice = msg.ref + ": " + condense(msg.err.Error())
		return m, nil
	}

	// Apply succeeded: the stack is unchanged, just confirm via toast.
	if !msg.kind.removesStash() {
		m.notice = msg.ref + " " + msg.kind.verb()
		return m, nil
	}

	// Pop/drop/branch succeeded. Prune the sidecar entry for the removed stash
	// so its label doesn't linger, then resync the list from the reload.
	if p, ok := m.store.(pruner); ok && msg.sha != "" {
		live := map[string]struct{}{}
		for _, s := range msg.reloaded {
			live[s.SHA] = struct{}{}
		}
		if n := p.Prune(live); n > 0 {
			_ = m.store.Save() // best-effort; a sidecar write error must not crash
		}
	}

	if msg.reloadErr != nil {
		// The git op worked but we couldn't re-list. Tell the user; the next
		// launch will resync. Keep the (now stale) list rather than blanking.
		m.notice = msg.ref + " " + msg.kind.verb() + " (reload failed: " + condense(msg.reloadErr.Error()) + ")"
		return m, nil
	}

	m.stashes = msg.reloaded
	m.notice = msg.ref + " " + msg.kind.verb()

	// No stashes left: nothing more to preview. The View renders an empty
	// state; quitting is up to the user.
	if len(m.stashes) == 0 {
		m.cursor = 0
		m.loadedFor = -1
		m.currentDiff = ""
		m.viewport.SetContent(dimStyle.Render("No stashes left — graveyard cleared. 🪦"))
		return m, nil
	}

	// Clamp the cursor (the list likely shrank) and reload that stash's diff.
	if m.cursor > len(m.stashes)-1 {
		m.cursor = len(m.stashes) - 1
	}
	m.loadedFor = -1
	m.loading = true
	m.viewport.SetContent(dimStyle.Render("Loading diff…"))
	m.viewport.GotoTop()
	return m, m.loadDiffCmd(m.cursor)
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

	// Append the "gathering dust" nag to the title row when any stash is dusty.
	if banner := m.dustBanner(); banner != "" {
		title = lipgloss.JoinHorizontal(lipgloss.Top, title, bannerStyle.Render(banner))
	}

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

// footer renders the bottom line of the UI: the inline label editor (while
// editing), a destructive-action confirm prompt (while confirming), a transient
// notice, or the key help.
func (m Model) footer() string {
	if m.editing {
		label := "label"
		if m.cursor >= 0 && m.cursor < len(m.stashes) {
			label = "label " + m.stashes[m.cursor].Ref()
		}
		return editPromptStyle.Render(label+": ") + m.input.View() +
			helpStyle.Render("  ⏎ save · esc cancel")
	}
	if m.branching {
		label := "branch"
		if m.cursor >= 0 && m.cursor < len(m.stashes) {
			label = "branch " + m.stashes[m.cursor].Ref() + " →"
		}
		return editPromptStyle.Render(label+": ") + m.input.View() +
			helpStyle.Render("  ⏎ create · esc cancel")
	}
	if m.confirming {
		ref := ""
		if m.cursor >= 0 && m.cursor < len(m.stashes) {
			ref = " " + m.stashes[m.cursor].Ref()
		}
		return confirmStyle.Render(fmt.Sprintf("%s%s? %s — (y/N)",
			capitalize(m.pending.imperative()), ref, m.pending.consequence()))
	}
	if m.notice != "" {
		return helpStyle.Render(m.notice)
	}
	return helpStyle.Render("↑/↓ select · l label · b branch · a apply · p pop · d drop · ⏎/space scroll · q quit")
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
	// most-skimmable facts together. The age is colored by staleness bucket and
	// a dusty stash gets a trailing "*" so it reads even without color.
	if age.Classify(s.Created, m.now, m.staleDays).Dusty() {
		ageTok += "*"
	}
	head := fmt.Sprintf("%s  %s", s.Ref(), m.ageStyleFor(s).Render(ageTok))
	if stat := s.Diffstat.String(); stat != "" {
		head += "  " + statStyle.Render(stat)
	}

	// Second line: the human label if present (highlighted), an auto-derived
	// label (dim + italic, so it reads as a guess), else the raw git subject.
	labelText, src := s.DisplaySource()
	var subj string
	switch src {
	case model.LabelUser:
		subj = labelStyle.Render(truncate(labelText, m.listInnerW))
	case model.LabelAuto:
		subj = autoLabelStyle.Render(truncate(labelText, m.listInnerW))
	default:
		subj = truncate(labelText, m.listInnerW)
	}
	br := branchStyle.Render(truncate("⎇ "+branch, m.listInnerW))

	return head + "\n" + subj + "\n" + br
}

// plural picks the singular or plural noun for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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

// dustBanner returns the short "gathering dust" nag for the title row, or ""
// when nothing is dusty (or staleness is disabled). It counts stashes whose
// staleness bucket is Dusty under the active threshold.
func (m Model) dustBanner() string {
	if m.staleDays <= 0 {
		return ""
	}
	dusty := 0
	for _, s := range m.stashes {
		if age.Classify(s.Created, m.now, m.staleDays).Dusty() {
			dusty++
		}
	}
	if dusty == 0 {
		return ""
	}
	verb := "is"
	if dusty != 1 {
		verb = "are"
	}
	return fmt.Sprintf("🧹 %d %s gathering dust", dusty, verb)
}

// ageStyleFor returns the lipgloss style for a stash's age token based on its
// staleness bucket, so the list shades older stashes hotter.
func (m Model) ageStyleFor(s model.Stash) lipgloss.Style {
	switch age.Classify(s.Created, m.now, m.staleDays) {
	case age.Ancient:
		return ageAncientStyle
	case age.Stale:
		return ageStaleStyle
	case age.Aging:
		return ageAgingStyle
	default:
		return ageFreshStyle
	}
}

// condense flattens a multi-line error message (git loves these) into a single
// space-joined line and trims it so a toast stays on one row. It keeps the
// message honest — we don't drop the text, just reflow it.
func condense(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// capitalize upper-cases the first rune of s (ASCII-focused; our callers pass
// plain lowercase verbs). Avoids the deprecated strings.Title.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
