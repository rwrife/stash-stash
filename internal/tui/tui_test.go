package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/rwrife/stash-stash/internal/model"
)

// fixedNow is a stable clock so age rendering in tests is deterministic.
var fixedNow = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

func sampleStashes() []model.Stash {
	return []model.Stash{
		{Index: 0, SHA: "aaa", Subject: "WIP on main: top", Branch: "main", Created: fixedNow.Add(-2 * time.Hour)},
		{Index: 1, SHA: "bbb", Subject: "On feature/x: middle", Branch: "feature/x", Created: fixedNow.Add(-48 * time.Hour)},
		{Index: 2, SHA: "ccc", Subject: "WIP on main: bottom", Branch: "main", Created: fixedNow.Add(-72 * time.Hour)},
	}
}

// stubShow returns a deterministic patch keyed by ref so we can assert which
// stash's diff was requested.
func stubShow(ctx context.Context, ref string) (string, error) {
	return "diff for " + ref, nil
}

// sized returns a model that has received an initial WindowSizeMsg so the
// viewport and layout fields are populated (View would otherwise short-circuit).
func sized(t *testing.T) Model {
	t.Helper()
	m := New(sampleStashes(), stubShow, fixedNow)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(Model)
}

func TestInitLoadsTopStash(t *testing.T) {
	m := New(sampleStashes(), stubShow, fixedNow)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil cmd, want a diff-load command")
	}
	msg := cmd()
	loaded, ok := msg.(diffLoadedMsg)
	if !ok {
		t.Fatalf("Init cmd produced %T, want diffLoadedMsg", msg)
	}
	if loaded.index != 0 {
		t.Errorf("loaded index = %d, want 0", loaded.index)
	}
	if loaded.diff != "diff for stash@{0}" {
		t.Errorf("loaded diff = %q, want the top stash's diff", loaded.diff)
	}
}

func TestInitEmptyStashesNoCmd(t *testing.T) {
	m := New(nil, stubShow, fixedNow)
	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init() with no stashes = non-nil cmd, want nil")
	}
}

func TestDownMovesCursorAndLoadsDiff(t *testing.T) {
	m := sized(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after 'j'", m.cursor)
	}
	if cmd == nil {
		t.Fatal("moving cursor returned nil cmd, want a diff-load command")
	}
	msg := cmd()
	loaded := msg.(diffLoadedMsg)
	if loaded.index != 1 || loaded.diff != "diff for stash@{1}" {
		t.Errorf("loaded = %+v, want index 1 diff for stash@{1}", loaded)
	}
}

func TestCursorClampsAtBounds(t *testing.T) {
	m := sized(t)

	// Up at the top is a no-op (stays 0, no cmd).
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped at top)", m.cursor)
	}
	if cmd != nil {
		t.Errorf("up at top produced a cmd, want nil (no reload)")
	}

	// Jump to bottom via 'G'.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = updated.(Model)
	if m.cursor != len(m.stashes)-1 {
		t.Errorf("cursor = %d, want %d (bottom)", m.cursor, len(m.stashes)-1)
	}

	// Down at the bottom is a no-op.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.cursor != len(m.stashes)-1 {
		t.Errorf("cursor = %d, want it pinned at the bottom", m.cursor)
	}
	if cmd != nil {
		t.Errorf("down at bottom produced a cmd, want nil")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	} {
		m := sized(t)
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("key %v produced nil cmd, want tea.Quit", k)
		}
		if msg := cmd(); msg == nil {
			t.Errorf("key %v: quit cmd produced nil msg", k)
		} else if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("key %v produced %T, want tea.QuitMsg", k, msg)
		}
	}
}

func TestStaleDiffResultIgnored(t *testing.T) {
	m := sized(t)
	// Cursor is on 0; a late result for index 2 must not overwrite the preview.
	updated, _ := m.Update(diffLoadedMsg{index: 2, diff: "stale", err: nil})
	m = updated.(Model)
	if m.loadedFor == 2 || m.currentDiff == "stale" {
		t.Errorf("stale diff for non-current stash was applied: loadedFor=%d diff=%q", m.loadedFor, m.currentDiff)
	}
}

func TestViewRendersListAndPreview(t *testing.T) {
	m := sized(t)
	// Apply the top stash's diff so the preview has content.
	updated, _ := m.Update(diffLoadedMsg{index: 0, diff: "diff for stash@{0}", err: nil})
	m = updated.(Model)

	out := m.View()
	for _, want := range []string{"stash-stash", "stash@{0}", "stash@{1}", "quit"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("View() missing %q.\n---\n%s", want, stripANSI(out))
		}
	}
}

func TestViewBeforeSizeIsLoadingMessage(t *testing.T) {
	m := New(sampleStashes(), stubShow, fixedNow)
	if got := m.View(); !strings.Contains(got, "Loading") {
		t.Errorf("pre-size View() = %q, want a loading message", got)
	}
}

func TestColorizeDiffClassifiesLines(t *testing.T) {
	// Force a color profile so styling is emitted regardless of whether the
	// test runs under a TTY (lipgloss otherwise strips ANSI for non-terminals).
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	patch := strings.Join([]string{
		"diff --git a/x b/x",
		"@@ -1 +1 @@",
		"-removed",
		"+added",
		" context",
	}, "\n")
	got := colorizeDiff(patch)
	// Coloring wraps lines in ANSI escapes; the underlying text must survive.
	for _, want := range []string{"removed", "added", "context", "@@ -1 +1 @@"} {
		if !strings.Contains(stripANSI(got), want) {
			t.Errorf("colorizeDiff dropped %q", want)
		}
	}
	// At least one ANSI escape should be present (something got colored).
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("colorizeDiff produced no ANSI styling")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"abc", 1, "abc"}, // max<=1 returns unchanged
		{"☃☃☃☃☃", 3, "☃☃…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestRenderDiffEmpty(t *testing.T) {
	m := sized(t)
	updated, _ := m.Update(diffLoadedMsg{index: 0, diff: "   \n  ", err: nil})
	m = updated.(Model)
	if got := stripANSI(m.renderDiff()); !strings.Contains(got, "empty diff") {
		t.Errorf("renderDiff() for blank patch = %q, want an empty-diff note", got)
	}
}

// stripANSI removes ANSI escape sequences so assertions can match plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			// skip until a letter terminator (m, K, H, etc.)
			j := i + 1
			for j < len(s) && !((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
