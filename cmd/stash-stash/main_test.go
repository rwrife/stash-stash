package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/meta"
	"github.com/rwrife/stash-stash/internal/model"
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

// --- issue #8: search subcommand ----------------------------------------

// stubList swaps the indirected gitList to return a fixed stash set.
func stubList(t *testing.T, stashes []model.Stash, err error) {
	t.Helper()
	orig := gitList
	gitList = func(ctx context.Context) ([]model.Stash, error) {
		return stashes, err
	}
	t.Cleanup(func() { gitList = orig })
}

// stubShow swaps the indirected gitShow to return a patch per stash ref.
func stubShow(t *testing.T, patches map[string]string, err error) {
	t.Helper()
	orig := gitShow
	gitShow = func(ctx context.Context, ref string) (string, error) {
		if err != nil {
			return "", err
		}
		return patches[ref], nil
	}
	t.Cleanup(func() { gitShow = orig })
}

func TestSearchFindsMatchesAcrossStashes(t *testing.T) {
	stubList(t, []model.Stash{
		{Index: 0, SHA: "aaa", Subject: "WIP on main: a", Branch: "main"},
		{Index: 1, SHA: "bbb", Subject: "On feature/x: b", Branch: "feature/x"},
	}, nil)
	stubShow(t, map[string]string{
		"stash@{0}": "--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,2 @@\n other\n+added retry budget\n",
		"stash@{1}": "--- a/b.txt\n+++ b/b.txt\n@@ -1,1 +1,1 @@\n-no relevant change\n+still nothing\n",
	}, nil)
	tempRepoSidecar(t) // metaLoad -> empty store (no labels), best-effort.

	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "retry"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "stash@{0}") || !strings.Contains(out, "added retry budget") {
		t.Errorf("stdout = %q, want the matching stash + snippet", out)
	}
	if strings.Contains(out, "stash@{1}") {
		t.Errorf("stdout = %q, want stash@{1} (no match) omitted", out)
	}
}

func TestSearchCaseInsensitiveByDefault(t *testing.T) {
	stubList(t, []model.Stash{{Index: 0, SHA: "aaa", Subject: "WIP on main: a", Branch: "main"}}, nil)
	stubShow(t, map[string]string{
		"stash@{0}": "--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-old\n+RETRY in caps\n",
	}, nil)
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"search", "retry"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "RETRY in caps") {
		t.Errorf("case-insensitive search missed an uppercase hit:\n%s", stdout.String())
	}
}

func TestSearchRegexFlag(t *testing.T) {
	stubList(t, []model.Stash{{Index: 0, SHA: "aaa", Subject: "WIP on main: a", Branch: "main"}}, nil)
	stubShow(t, map[string]string{
		"stash@{0}": "--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n+retry budget = 5\n+totally unrelated\n",
	}, nil)
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"search", "--regex", `retry.*=.*[0-9]`}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "retry budget = 5") {
		t.Errorf("regex search missed the budget line:\n%s", out)
	}
	if strings.Contains(out, "totally unrelated") {
		t.Errorf("regex search matched an unrelated line:\n%s", out)
	}
}

func TestSearchNoMatchesIsExitZero(t *testing.T) {
	stubList(t, []model.Stash{{Index: 0, SHA: "aaa", Subject: "WIP on main: a", Branch: "main"}}, nil)
	stubShow(t, map[string]string{
		"stash@{0}": "--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-x\n+y\n",
	}, nil)
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "definitely-absent"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for no matches", code)
	}
	if !strings.Contains(stdout.String(), "No stash contents matched") {
		t.Errorf("stdout = %q, want a friendly no-match line", stdout.String())
	}
}

func TestSearchEmptyStackIsExitZero(t *testing.T) {
	stubList(t, nil, nil)
	// gitShow must not be called; stub it to fail loudly if it is.
	stubShow(t, nil, errors.New("show should not be called for an empty stack"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "anything"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for empty stack", code)
	}
	if !strings.Contains(stdout.String(), "No stashes found") {
		t.Errorf("stdout = %q, want an empty-graveyard note", stdout.String())
	}
}

func TestSearchMissingTerm(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"search"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a missing term", code)
	}
	if !strings.Contains(stderr.String(), "missing search term") {
		t.Errorf("stderr = %q, want a missing-term usage error", stderr.String())
	}
}

func TestSearchInvalidRegex(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "--regex", "("}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a bad regex", code)
	}
	if !strings.Contains(stderr.String(), "invalid --regex pattern") {
		t.Errorf("stderr = %q, want a regex compile error", stderr.String())
	}
}

func TestSearchNotARepo(t *testing.T) {
	stubList(t, nil, git.ErrNotARepo)

	var stdout, stderr bytes.Buffer
	code := run([]string{"search", "x"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 outside a repo", code)
	}
	if !strings.Contains(stderr.String(), "not a git repository") {
		t.Errorf("stderr = %q, want a not-a-repo message", stderr.String())
	}
}

func TestSearchMultiWordTerm(t *testing.T) {
	// Unquoted multi-word terms join into a single phrase search.
	stubList(t, []model.Stash{{Index: 0, SHA: "aaa", Subject: "WIP on main: a", Branch: "main"}}, nil)
	stubShow(t, map[string]string{
		"stash@{0}": "--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-x\n+retry budget grew\n",
	}, nil)
	tempRepoSidecar(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"search", "retry", "budget"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "retry budget grew") {
		t.Errorf("multi-word term search missed the line:\n%s", stdout.String())
	}
}
