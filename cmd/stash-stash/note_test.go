package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
)

// forceNonTTY makes stdoutIsTTY report false for the duration of a test so the
// note command takes its script-friendly (print/no-editor) path.
func forceNonTTY(t *testing.T) {
	t.Helper()
	orig := stdoutIsTTY
	stdoutIsTTY = func() bool { return false }
	t.Cleanup(func() { stdoutIsTTY = orig })
}

func noteFixtureStashes() []model.Stash {
	now := time.Now()
	return []model.Stash{
		{Index: 0, SHA: "sha-zero", Subject: "WIP on main: a", Branch: "main", Created: now},
		{Index: 1, SHA: "sha-one", Subject: "WIP on main: b", Branch: "main", Created: now},
	}
}

func TestNoteSetAndReadBack(t *testing.T) {
	forceNonTTY(t)
	stubList(t, noteFixtureStashes(), nil)
	dir := tempRepoSidecar(t)

	// Set a note on stash@{1} by bare index.
	var out, errb bytes.Buffer
	if code := run([]string{"note", "1", "-m", "blocked on #42"}, &out, &errb); code != 0 {
		t.Fatalf("set exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Note saved") {
		t.Errorf("set output = %q, want confirmation", out.String())
	}
	if s := readSidecar(t, dir); !strings.Contains(s, "blocked on #42") {
		t.Errorf("sidecar missing note; got %q", s)
	}

	// Read it back (non-TTY, no -m) -> prints the note.
	out.Reset()
	errb.Reset()
	if code := run([]string{"note", "stash@{1}"}, &out, &errb); code != 0 {
		t.Fatalf("read exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "blocked on #42" {
		t.Errorf("read output = %q, want the note", got)
	}
}

func TestNoteClear(t *testing.T) {
	forceNonTTY(t)
	stubList(t, noteFixtureStashes(), nil)
	dir := tempRepoSidecar(t)

	var out, errb bytes.Buffer
	run([]string{"note", "0", "-m", "temp"}, &out, &errb)
	out.Reset()
	errb.Reset()
	if code := run([]string{"note", "0", "--clear"}, &out, &errb); code != 0 {
		t.Fatalf("clear exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "cleared") {
		t.Errorf("clear output = %q, want cleared confirmation", out.String())
	}
	if s := readSidecar(t, dir); strings.Contains(s, "temp") {
		t.Errorf("note not cleared from sidecar; got %q", s)
	}
}

func TestNoteMissingRef(t *testing.T) {
	forceNonTTY(t)
	stubList(t, noteFixtureStashes(), nil)
	tempRepoSidecar(t)

	var out, errb bytes.Buffer
	if code := run([]string{"note"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2 for missing ref", code)
	}
}

func TestNoteUnknownRef(t *testing.T) {
	forceNonTTY(t)
	stubList(t, noteFixtureStashes(), nil)
	tempRepoSidecar(t)

	var out, errb bytes.Buffer
	if code := run([]string{"note", "9", "-m", "x"}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1 for unknown ref; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "no stash matches") {
		t.Errorf("stderr = %q, want no-match message", errb.String())
	}
}

func TestNoteMAndClearConflict(t *testing.T) {
	forceNonTTY(t)
	stubList(t, noteFixtureStashes(), nil)
	tempRepoSidecar(t)

	var out, errb bytes.Buffer
	if code := run([]string{"note", "0", "-m", "x", "--clear"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2 for -m + --clear", code)
	}
}
