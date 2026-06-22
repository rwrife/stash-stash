package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/meta"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "stash-stash") {
		t.Errorf("version output = %q, want it to contain %q", stdout.String(), "stash-stash")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--definitely-not-a-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr is empty, want a flag-parse error message")
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for -h", code)
	}
}

func TestRunHelpListsM6Flags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"-h"}, &stdout, &stderr)
	help := stderr.String()
	for _, want := range []string{"-stale-days", "-json"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing flag %q:\n%s", want, help)
		}
	}
}

func TestRunRejectsNegativeStaleDays(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Negative --stale-days is rejected before any git work, so this is
	// deterministic regardless of the working directory.
	code := run([]string{"--stale-days=-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 for negative --stale-days", code)
	}
	if !strings.Contains(stderr.String(), "stale-days") {
		t.Errorf("stderr = %q, want it to mention stale-days", stderr.String())
	}
}

// --- M5: push subcommand -------------------------------------------------

// stubPush swaps the indirected gitPush for the duration of a test.
func stubPush(t *testing.T, sha string, err error) *[]string {
	t.Helper()
	var gotMsgs []string
	orig := gitPush
	gitPush = func(ctx context.Context, msg string) (string, error) {
		gotMsgs = append(gotMsgs, msg)
		return sha, err
	}
	t.Cleanup(func() { gitPush = orig })
	return &gotMsgs
}

// tempRepoSidecar makes metaLoad resolve to a fresh temp git repo so the push
// label can actually be written and read back. Returns the repo dir.
func tempRepoSidecar(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	gitDir := filepath.Join(dir, ".git")
	orig := metaLoad
	metaLoad = func(ctx context.Context) (*meta.Store, error) {
		// Load against the temp repo's git dir by chdir'ing for the resolve.
		// meta.Load uses `git rev-parse --absolute-git-dir`, so run it there.
		prev, _ := os.Getwd()
		_ = os.Chdir(dir)
		defer os.Chdir(prev)
		return meta.Load(ctx)
	}
	t.Cleanup(func() { metaLoad = orig })
	_ = gitDir
	return dir
}

func readSidecar(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".git", "stash-stash.json"))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestPushRecordsLabel(t *testing.T) {
	msgs := stubPush(t, "cafef00d", nil)
	dir := tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"push", "-m", "payments: retry fix"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(*msgs) != 1 || (*msgs)[0] != "payments: retry fix" {
		t.Fatalf("git push got msgs %v, want one with the label", *msgs)
	}
	if !strings.Contains(stdout.String(), "labeled") {
		t.Errorf("stdout = %q, want a labeled confirmation", stdout.String())
	}
	side := readSidecar(t, dir)
	if !strings.Contains(side, "payments: retry fix") || !strings.Contains(side, "cafef00d") {
		t.Errorf("sidecar = %q, want it to map cafef00d -> the label", side)
	}
}

func TestPushNoLabelSkipsSidecar(t *testing.T) {
	stubPush(t, "deadbeef", nil)
	dir := tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"push"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Stashed") {
		t.Errorf("stdout = %q, want a stashed message", stdout.String())
	}
	if s := readSidecar(t, dir); s != "" {
		t.Errorf("sidecar written for an unlabeled push: %q", s)
	}
}

func TestPushNothingToStash(t *testing.T) {
	stubPush(t, "", git.ErrNothingToStash)
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"push", "-m", "label"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a clean tree", code)
	}
	if !strings.Contains(stdout.String(), "Nothing to stash") {
		t.Errorf("stdout = %q, want a 'nothing to stash' note", stdout.String())
	}
}

func TestPushNotARepo(t *testing.T) {
	stubPush(t, "", git.ErrNotARepo)
	// metaLoad won't be reached (push fails first), but stub it for isolation.
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"push", "-m", "x"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 outside a repo", code)
	}
	if !strings.Contains(stderr.String(), "not a git repository") {
		t.Errorf("stderr = %q, want a not-a-repo message", stderr.String())
	}
}

func TestPushUnexpectedArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"push", "-m", "label", "stray"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a stray positional arg", code)
	}
}
