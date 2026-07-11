package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rwrife/stash-stash/internal/git"
)

func sizeMsg() tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: 100, Height: 30} }

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// preflightReturning builds a PreflightFunc that always returns the given
// result, recording the refs it was asked about.
func preflightReturning(into *[]string, res git.PreflightResult) PreflightFunc {
	return func(ctx context.Context, ref string) (git.PreflightResult, error) {
		if into != nil {
			*into = append(*into, ref)
		}
		return res, nil
	}
}

func TestApplyCleanPreflightFiresImmediately(t *testing.T) {
	var applied, checked []string
	actions := Actions{
		Apply:     recordingAction(&applied, nil),
		Preflight: preflightReturning(&checked, git.PreflightResult{}), // clean
	}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	// 'a' kicks off the async preflight (model goes busy, no confirm yet).
	updated, cmd := m.Update(keyRune('a'))
	m = updated.(Model)
	if m.conflictPrompt {
		t.Fatal("conflict prompt opened before preflight result")
	}
	if len(applied) != 0 {
		t.Fatal("apply fired before the preflight completed")
	}
	// Deliver the preflight result; handlePreflightDone returns the follow-on
	// apply cmd, which we then run to complete the action.
	prefMsg := cmd()
	updated, cmd = m.Update(prefMsg)
	m = updated.(Model)
	m = runCmd(t, m, cmd)
	if len(applied) == 0 {
		t.Fatalf("apply did not fire on a clean preflight (checked=%v)", checked)
	}
	if len(checked) != 1 || checked[0] != "stash@{0}" {
		t.Fatalf("preflight checked %v, want [stash@{0}]", checked)
	}
}

func TestApplyConflictOpensExtraConfirm(t *testing.T) {
	var applied []string
	actions := Actions{
		Apply: recordingAction(&applied, nil),
		Preflight: preflightReturning(nil, git.PreflightResult{
			Conflicts: true,
			Files:     []string{"foo.go", "bar.go"},
		}),
	}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	updated, cmd := m.Update(keyRune('a'))
	m = updated.(Model)
	m = runCmd(t, m, cmd) // deliver preflight result

	if !m.conflictPrompt {
		t.Fatal("predicted conflict did not open the extra confirm")
	}
	if len(applied) != 0 {
		t.Fatal("apply fired despite predicted conflict and no confirmation")
	}
	foot := stripANSI(m.footer())
	if !strings.Contains(foot, "conflict") || !strings.Contains(foot, "foo.go") {
		t.Errorf("conflict prompt footer = %q, want conflict + files", foot)
	}

	// 'y' proceeds with the apply.
	updated, cmd = m.Update(keyRune('y'))
	m = updated.(Model)
	if m.conflictPrompt {
		t.Error("conflict prompt still open after 'y'")
	}
	m = runCmd(t, m, cmd)
	if len(applied) != 1 {
		t.Fatalf("apply did not fire after confirming the conflict: %v", applied)
	}
}

func TestApplyConflictCancelledByN(t *testing.T) {
	var applied []string
	actions := Actions{
		Apply: recordingAction(&applied, nil),
		Preflight: preflightReturning(nil, git.PreflightResult{
			Conflicts: true, Files: []string{"foo.go"},
		}),
	}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	updated, cmd := m.Update(keyRune('a'))
	m = updated.(Model)
	m = runCmd(t, m, cmd)

	updated, cmd = m.Update(keyRune('n'))
	m = updated.(Model)
	if m.conflictPrompt {
		t.Error("still in conflict prompt after 'n'")
	}
	if cmd != nil {
		t.Error("'n' returned a cmd, want nil")
	}
	if len(applied) != 0 {
		t.Fatalf("apply ran despite 'n': %v", applied)
	}
	if !strings.Contains(stripANSI(m.footer()), "cancelled") {
		t.Errorf("footer = %q, want cancelled", stripANSI(m.footer()))
	}
}

func TestPopConflictThenDestructiveConfirm(t *testing.T) {
	// Pop with a predicted conflict: 'y' on the conflict prompt must still lead
	// to pop's own destructive y/N (pop removes the entry).
	var popped []string
	actions := Actions{
		Pop:    recordingAction(&popped, nil),
		Reload: reloadReturning(nil, nil),
		Preflight: preflightReturning(nil, git.PreflightResult{
			Conflicts: true, Files: []string{"x.go"},
		}),
	}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	updated, cmd := m.Update(keyRune('p'))
	m = updated.(Model)
	m = runCmd(t, m, cmd) // preflight -> conflict prompt
	if !m.conflictPrompt {
		t.Fatal("pop did not open conflict prompt")
	}
	// Accept the conflict risk.
	updated, cmd = m.Update(keyRune('y'))
	m = updated.(Model)
	if !m.confirming {
		t.Fatal("pop did not fall through to its destructive confirm after conflict-y")
	}
	if len(popped) != 0 {
		t.Fatal("pop fired before its destructive confirm")
	}
	// Confirm the destructive pop.
	updated, cmd = m.Update(keyRune('y'))
	m = updated.(Model)
	m = runCmd(t, m, cmd)
	if len(popped) != 1 {
		t.Fatalf("pop did not fire after both confirms: %v", popped)
	}
}

func TestUndeterminedPreflightProceedsWithNote(t *testing.T) {
	var applied []string
	actions := Actions{
		Apply: recordingAction(&applied, nil),
		Preflight: preflightReturning(nil, git.PreflightResult{
			Undetermined: true, Reason: "old git",
		}),
	}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	updated, cmd := m.Update(keyRune('a'))
	m = updated.(Model)
	prefMsg := cmd()
	updated, cmd = m.Update(prefMsg)
	m = updated.(Model)
	if m.conflictPrompt {
		t.Fatal("undetermined must not open the conflict prompt")
	}
	// The in-flight toast should carry the couldn't-pre-check note before the
	// action result overwrites it.
	if !strings.Contains(stripANSI(m.footer()), "couldn't pre-check") {
		t.Errorf("footer = %q, want a couldn't-pre-check note", stripANSI(m.footer()))
	}
	m = runCmd(t, m, cmd) // run the follow-on apply
	if len(applied) != 1 {
		t.Fatalf("apply should fire (fail open) on undetermined: %v", applied)
	}
}

func TestNilPreflightPreservesOldBehavior(t *testing.T) {
	// No Preflight func: apply fires immediately, exactly as before issue #23.
	var applied []string
	actions := Actions{Apply: recordingAction(&applied, nil)}
	m := New(sampleStashes(), stubShow, newFakeStore(), actions, fixedNow, 0, nil)
	updated, _ := m.Update(sizeMsg())
	m = updated.(Model)

	updated, cmd := m.Update(keyRune('a'))
	m = updated.(Model)
	if m.conflictPrompt {
		t.Fatal("nil preflight should not introduce a conflict-probe step")
	}
	m = runCmd(t, m, cmd)
	if len(applied) != 1 {
		t.Fatalf("apply did not fire immediately with nil preflight: %v", applied)
	}
}
