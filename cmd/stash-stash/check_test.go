package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/git"
)

func stubCheck(t *testing.T, res git.PreflightResult, err error) {
	t.Helper()
	orig := gitCheck
	gitCheck = func(ctx context.Context, ref string) (git.PreflightResult, error) {
		return res, err
	}
	t.Cleanup(func() { gitCheck = orig })
}

func TestCheckClean(t *testing.T) {
	stubCheck(t, git.PreflightResult{}, nil)
	var stdout, stderr bytes.Buffer
	code := run([]string{"check"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (clean)", code)
	}
	if !strings.Contains(stdout.String(), "cleanly") {
		t.Errorf("stdout = %q, want a clean message", stdout.String())
	}
}

func TestCheckConflictExit3(t *testing.T) {
	stubCheck(t, git.PreflightResult{Conflicts: true, Files: []string{"a.go", "b.go"}}, nil)
	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "stash@{1}"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (conflict)", code)
	}
	// Conflicting paths go to stdout (pipe-friendly), note to stderr.
	if !strings.Contains(stdout.String(), "a.go") || !strings.Contains(stdout.String(), "b.go") {
		t.Errorf("stdout = %q, want conflicting paths", stdout.String())
	}
	if !strings.Contains(stderr.String(), "conflict") {
		t.Errorf("stderr = %q, want a conflict note", stderr.String())
	}
}

func TestCheckUndeterminedExit4(t *testing.T) {
	stubCheck(t, git.PreflightResult{Undetermined: true, Reason: "old git"}, nil)
	var stdout, stderr bytes.Buffer
	code := run([]string{"check"}, &stdout, &stderr)
	if code != 4 {
		t.Fatalf("exit = %d, want 4 (undetermined)", code)
	}
	if !strings.Contains(stderr.String(), "pre-check") {
		t.Errorf("stderr = %q, want a couldn't-pre-check note", stderr.String())
	}
}

func TestCheckNotARepo(t *testing.T) {
	stubCheck(t, git.PreflightResult{}, git.ErrNotARepo)
	var stdout, stderr bytes.Buffer
	code := run([]string{"check"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (not a repo)", code)
	}
}

func TestCheckError(t *testing.T) {
	stubCheck(t, git.PreflightResult{}, errors.New("boom"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"check"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCheckTooManyArgs(t *testing.T) {
	stubCheck(t, git.PreflightResult{}, nil)
	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "stash@{0}", "stash@{1}"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
}
