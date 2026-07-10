package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNoteEditSavesAndShowsIndicator(t *testing.T) {
	store := newFakeStore()
	m := New(sampleStashes(), stubShow, store, Actions{}, fixedNow, 0, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)

	// Open the note editor on stash@{0} (SHA "aaa") with 'n'.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if !m.noting {
		t.Fatal("'n' did not open the note editor")
	}

	m = typeRunes(t, m, "blocked on API change")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.noting {
		t.Error("note editor still open after Enter")
	}
	if store.notes["aaa"] != "blocked on API change" {
		t.Errorf("store note for aaa = %q, want the new note", store.notes["aaa"])
	}
	if store.saves != 1 {
		t.Errorf("Save called %d times, want 1", store.saves)
	}
	if m.stashes[0].Note != "blocked on API change" {
		t.Errorf("in-memory note = %q, want the new note", m.stashes[0].Note)
	}
	// The 📝 indicator should appear in the list.
	view := stripANSI(m.View())
	if !strings.Contains(view, "📝") {
		t.Errorf("View() missing note indicator:\n%s", view)
	}
	// The note renders at the top of the preview pane.
	if diff := stripANSI(m.renderDiff()); !strings.Contains(diff, "blocked on API change") {
		t.Errorf("renderDiff() missing note text:\n%s", diff)
	}
}

func TestNoteEditCancelDiscards(t *testing.T) {
	store := newFakeStore()
	m := New(sampleStashes(), stubShow, store, Actions{}, fixedNow, 0, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m = typeRunes(t, m, "discard me")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.noting {
		t.Error("note editor still open after Esc")
	}
	if _, ok := store.notes["aaa"]; ok {
		t.Errorf("cancelled note was persisted: %q", store.notes["aaa"])
	}
	if store.saves != 0 {
		t.Errorf("Save called %d times on cancel, want 0", store.saves)
	}
}
