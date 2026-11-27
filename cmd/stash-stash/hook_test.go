package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/model"
)

func TestRunHookInitGeneratesBashAndZshIntegration(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHook([]string{"init", "--shell", shell}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			for _, want := range []string{"checkout|switch", "command stash-stash hook check", "command git \"$@\""} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("integration missing %q:\n%s", want, stdout.String())
				}
			}
		})
	}
}

func TestRunHookInitRejectsUnsupportedShell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runHook([]string{"init", "--shell", "fish"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bash, zsh") {
		t.Fatalf("stderr = %q, want valid shell names", stderr.String())
	}
}

func TestRunHookCheckReportsOnlyDustyStashes(t *testing.T) {
	oldNow := now
	now = func() time.Time { return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) }
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return []model.Stash{
			{Created: now().Add(-15 * 24 * time.Hour)},
			{Created: now().Add(-2 * 24 * time.Hour)},
		}, nil
	}
	t.Cleanup(func() {
		now = oldNow
		gitList = originalList
	})

	var stdout, stderr bytes.Buffer
	if code := runHookCheck(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 stale stash") {
		t.Fatalf("stdout = %q, want one stale-stash warning", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHookCheckIgnoresNonRepository(t *testing.T) {
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return nil, git.ErrNotARepo
	}
	t.Cleanup(func() { gitList = originalList })
	var stdout, stderr bytes.Buffer
	if code := runHookCheck(nil, &stdout, &stderr); code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q; want quiet success", code, stdout.String(), stderr.String())
	}
}

func TestRunHookCheckSurfacesGitErrors(t *testing.T) {
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return nil, errors.New("git unavailable")
	}
	t.Cleanup(func() { gitList = originalList })
	var stdout, stderr bytes.Buffer
	if code := runHookCheck(nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "git unavailable") {
		t.Fatalf("exit = %d, stderr = %q; want reported git failure", code, stderr.String())
	}
}
