package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rwrife/stash-stash/internal/model"
)

// fuzzyStashes gives distinctive labels/branches/tags/notes so a query can be
// aimed at a single field.
func fuzzyStashes() []model.Stash {
	return []model.Stash{
		{Index: 0, SHA: "aaa", Label: "refactor parser", Branch: "main",
			Tags: []string{"wip"}, Note: "blocked on api", Created: fixedNow.Add(-1 * 3600e9)},
		{Index: 1, SHA: "bbb", Label: "fix login bug", Branch: "feature/login",
			Tags: []string{"hotfix"}, Note: "revisit after review", Created: fixedNow.Add(-2 * 3600e9)},
		{Index: 2, SHA: "ccc", Label: "docs cleanup", Branch: "docs",
			Tags: []string{"chore"}, Created: fixedNow.Add(-3 * 3600e9)},
	}
}

func newFuzzyModel(t *testing.T) Model {
	t.Helper()
	m := New(fuzzyStashes(), stubShow, newFakeStore(), Actions{}, fixedNow, 0, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(Model)
}

func TestFuzzyFilterNarrowsLive(t *testing.T) {
	m := newFuzzyModel(t)
	// Open the live filter with '/'.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	if !m.fuzzing {
		t.Fatal("'/' should open the live fuzzy filter")
	}
	// Type "login" — should match only the login bug (label + branch).
	m = typeRunes(t, m, "login")
	v := m.visible()
	if len(v) != 1 || v[0].SHA != "bbb" {
		t.Fatalf("expected only bbb visible, got %d rows", len(v))
	}
}

func TestFuzzyFilterMatchesEachField(t *testing.T) {
	cases := []struct {
		query   string
		wantSHA string
	}{
		{"parser", "aaa"},  // label
		{"docs", "ccc"},    // branch (and label)
		{"hotfix", "bbb"},  // tag
		{"blocked", "aaa"}, // note
	}
	for _, c := range cases {
		m := newFuzzyModel(t)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		m = updated.(Model)
		m = typeRunes(t, m, c.query)
		v := m.visible()
		if len(v) != 1 || v[0].SHA != c.wantSHA {
			t.Errorf("query %q: expected %s, got %d rows", c.query, c.wantSHA, len(v))
		}
	}
}

func TestFuzzyFilterEscRestoresFullList(t *testing.T) {
	m := newFuzzyModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "login")
	if len(m.visible()) != 1 {
		t.Fatalf("filter should have narrowed to 1")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.fuzzing {
		t.Fatal("esc should close the filter prompt")
	}
	if len(m.visible()) != 3 {
		t.Fatalf("esc should restore full list, got %d", len(m.visible()))
	}
	if strings.TrimSpace(m.filterText) != "" {
		t.Fatal("esc should clear the query")
	}
}

func TestFuzzyFilterEnterKeepsNarrowedView(t *testing.T) {
	m := newFuzzyModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "login")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.fuzzing {
		t.Fatal("enter should close the prompt")
	}
	if len(m.visible()) != 1 {
		t.Fatalf("enter should keep the filter applied, got %d", len(m.visible()))
	}
	// Navigation still works while committed-filtered.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor should stay on the single visible row, got %d", m.cursor)
	}
}

func TestFuzzyFilterCursorStaysValid(t *testing.T) {
	m := newFuzzyModel(t)
	// Move cursor to the last row first.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = updated.(Model)
	if m.cursor != 2 {
		t.Fatalf("precondition: cursor should be 2, got %d", m.cursor)
	}
	// Now filter down to a single row; cursor must clamp to a valid index.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "parser")
	v := m.visible()
	if len(v) != 1 {
		t.Fatalf("expected 1 visible, got %d", len(v))
	}
	if m.cursor < 0 || m.cursor >= len(v) {
		t.Fatalf("cursor %d out of range for %d visible rows", m.cursor, len(v))
	}
	if _, ok := m.selected(); !ok {
		t.Fatal("selected() should resolve to a valid row")
	}
}

func TestFuzzyFilterNoMatchesShowsEmptyState(t *testing.T) {
	m := newFuzzyModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "zzzznope")
	if len(m.visible()) != 0 {
		t.Fatalf("expected zero matches, got %d", len(m.visible()))
	}
	// selected() must not panic and reports no selection.
	if _, ok := m.selected(); ok {
		t.Fatal("selected() should be false with no visible rows")
	}
	// renderList shows the empty-state, not a crash.
	list := m.renderList()
	if !strings.Contains(list, "no stashes match") {
		t.Fatalf("expected empty-state message, got %q", list)
	}
}

func TestFuzzyFilterActionsTargetCorrectStash(t *testing.T) {
	// With a filter narrowing to a single non-top stash, selected() must resolve
	// to that stash's real SHA/ref, so actions operate on the right underlying
	// stash (acceptance: actions operate correctly while filtered).
	m := newFuzzyModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "login")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	sel, ok := m.selected()
	if !ok {
		t.Fatal("expected a selection")
	}
	if sel.SHA != "bbb" {
		t.Fatalf("selected SHA = %q, want bbb", sel.SHA)
	}
	if sel.Ref() != "stash@{1}" {
		t.Fatalf("selected ref = %q, want stash@{1}", sel.Ref())
	}
}

func TestFuzzyFilterTitleAndFooterReflectFilter(t *testing.T) {
	m := newFuzzyModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeRunes(t, m, "login")
	// While typing, footer is the filter prompt.
	if foot := m.footer(); !strings.Contains(foot, "match") {
		t.Fatalf("footer should show live match count, got %q", foot)
	}
	// Title shows "N of M" once narrowed.
	if title := m.titleText(); !strings.Contains(title, "of") {
		t.Fatalf("title should show 'of' count while filtered, got %q", title)
	}
}
