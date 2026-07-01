package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

func TestRunHelpListsTagFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"-h"}, &stdout, &stderr)
	help := stderr.String()
	if !strings.Contains(help, "-tag") {
		t.Errorf("help missing -tag flag:\n%s", help)
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

// --- issue #10: doctor subcommand ----------------------------------------

// stubDangling swaps the indirected gitDangling to return a fixed set.
func stubDangling(t *testing.T, ds []git.DanglingStash, err error) {
	t.Helper()
	orig := gitDangling
	gitDangling = func(ctx context.Context) ([]git.DanglingStash, error) {
		return ds, err
	}
	t.Cleanup(func() { gitDangling = orig })
}

// stubStoreStash swaps the indirected gitStoreStash, recording each restore.
func stubStoreStash(t *testing.T, err error) *[]string {
	t.Helper()
	var restored []string
	orig := gitStoreStash
	gitStoreStash = func(ctx context.Context, sha, message string) error {
		restored = append(restored, sha)
		return err
	}
	t.Cleanup(func() { gitStoreStash = orig })
	return &restored
}

// forceNonTTYStdin pins stdinIsTTY to false so runDoctor takes the read-only
// report path deterministically in tests (no real terminal involved).
func forceNonTTYStdin(t *testing.T) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = orig })
}

func TestDoctorCleanBillOfHealth(t *testing.T) {
	stubDangling(t, nil, nil)
	stubList(t, nil, nil)
	tempRepoSidecar(t) // empty sidecar -> no orphans
	forceNonTTYStdin(t)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Clean bill of health") {
		t.Errorf("stdout = %q, want a clean-bill message", stdout.String())
	}
}

func TestDoctorNotARepo(t *testing.T) {
	stubDangling(t, nil, git.ErrNotARepo)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 outside a repo", code)
	}
	if !strings.Contains(stderr.String(), "not a git repository") {
		t.Errorf("stderr = %q, want a not-a-repo message", stderr.String())
	}
}

func TestDoctorDryRunReportsButDoesNotChange(t *testing.T) {
	stubDangling(t, []git.DanglingStash{
		{SHA: "deadbeefcafe00", Subject: "WIP on main: lost work", Branch: "main", Source: "reflog"},
	}, nil)
	stubList(t, nil, nil)
	restored := stubStoreStash(t, nil)
	dir := tempRepoSidecar(t)
	// Seed an orphaned label so the report shows it too.
	seedSidecar(t, dir, map[string]string{"00orphansha00": "label for gone work"})

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Recoverable stashes") || !strings.Contains(out, "deadbeefcafe") {
		t.Errorf("stdout = %q, want the recoverable stash listed", out)
	}
	if !strings.Contains(out, "Orphaned sidecar labels") || !strings.Contains(out, "label for gone work") {
		t.Errorf("stdout = %q, want the orphaned label listed", out)
	}
	if !strings.Contains(out, "Dry run") {
		t.Errorf("stdout = %q, want a dry-run notice", out)
	}
	if len(*restored) != 0 {
		t.Errorf("dry run restored stashes: %v", *restored)
	}
	// The sidecar must be untouched (orphan still present).
	if s := readSidecar(t, dir); !strings.Contains(s, "label for gone work") {
		t.Errorf("dry run modified the sidecar: %q", s)
	}
}

func TestDoctorNonTTYIsReadOnly(t *testing.T) {
	// Non-TTY stdin (the default in tests) must not prompt or change anything,
	// even without --dry-run.
	stubDangling(t, []git.DanglingStash{
		{SHA: "abc123abc123", Subject: "WIP on main: x", Branch: "main", Source: "fsck"},
	}, nil)
	stubList(t, nil, nil)
	restored := stubStoreStash(t, nil)
	tempRepoSidecar(t)
	forceNonTTYStdin(t)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader("r\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(*restored) != 0 {
		t.Errorf("non-TTY run restored stashes despite no prompt: %v", *restored)
	}
	if !strings.Contains(stdout.String(), "interactive terminal") {
		t.Errorf("stdout = %q, want a hint to run interactively", stdout.String())
	}
}

func TestDoctorUnexpectedArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"stray"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a stray arg", code)
	}
}

// --- doctorTriage: the interactive engine, driven by scripted input ------

func TestDoctorTriageRestoresOnR(t *testing.T) {
	restored := stubStoreStash(t, nil)
	store := newTempStore(t)
	dangling := []git.DanglingStash{
		{SHA: "deadbeefcafe11", Subject: "WIP on main: rescue me", Branch: "main", Source: "reflog"},
	}

	var stdout, stderr bytes.Buffer
	in := bufio.NewReader(strings.NewReader("r\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, dangling, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if len(*restored) != 1 || (*restored)[0] != "deadbeefcafe11" {
		t.Fatalf("restored = %v, want the one SHA", *restored)
	}
	if !strings.Contains(stdout.String(), "restored as stash@{0}") {
		t.Errorf("stdout = %q, want a restore confirmation", stdout.String())
	}
}

func TestDoctorTriageDiffThenRestore(t *testing.T) {
	restored := stubStoreStash(t, nil)
	// gitShow backs the [d]iff action; return a small patch for the SHA.
	stubShow(t, map[string]string{
		"deadbeefcafe22": "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-old\n+new\n",
	}, nil)
	store := newTempStore(t)
	dangling := []git.DanglingStash{
		{SHA: "deadbeefcafe22", Subject: "WIP on main: peek then rescue", Source: "fsck"},
	}

	var stdout, stderr bytes.Buffer
	// First show the diff, then restore.
	in := bufio.NewReader(strings.NewReader("d\nr\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, dangling, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "+new") {
		t.Errorf("stdout = %q, want the diff shown after [d]", out)
	}
	if len(*restored) != 1 {
		t.Errorf("restored = %v, want exactly one after d then r", *restored)
	}
}

func TestDoctorTriageSkipLeavesEverything(t *testing.T) {
	restored := stubStoreStash(t, nil)
	store := newTempStore(t)
	store.SetLabel("orphan1", "keep on skip")
	dangling := []git.DanglingStash{{SHA: "sha1", Subject: "WIP on main: x", Source: "reflog"}}
	orphans := store.Orphans(map[string]struct{}{}) // orphan1 is unknown

	var stdout, stderr bytes.Buffer
	// Skip the dangling stash, then skip the orphan.
	in := bufio.NewReader(strings.NewReader("s\ns\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, dangling, orphans)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(*restored) != 0 {
		t.Errorf("skip restored a stash: %v", *restored)
	}
	if _, ok := store.Label("orphan1"); !ok {
		t.Error("skip removed the orphaned label")
	}
}

func TestDoctorTriageDeletesOrphanAndSaves(t *testing.T) {
	stubStoreStash(t, nil)
	dir := tempRepoSidecar(t)
	store := loadSidecarStore(t)
	store.SetLabel("orphanX", "delete me")
	if err := store.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	orphans := store.Orphans(map[string]struct{}{})

	var stdout, stderr bytes.Buffer
	in := bufio.NewReader(strings.NewReader("d\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, nil, orphans)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, ok := store.Label("orphanX"); ok {
		t.Error("orphan label still present in memory after delete")
	}
	// Persisted: the saved sidecar must no longer mention the label.
	if s := readSidecar(t, dir); strings.Contains(s, "delete me") {
		t.Errorf("sidecar still contains the deleted label: %q", s)
	}
}

func TestDoctorTriageQuitStopsEarly(t *testing.T) {
	restored := stubStoreStash(t, nil)
	store := newTempStore(t)
	dangling := []git.DanglingStash{
		{SHA: "first0", Subject: "WIP on main: first", Source: "reflog"},
		{SHA: "second", Subject: "WIP on main: second", Source: "reflog"},
	}

	var stdout, stderr bytes.Buffer
	// Quit immediately at the first prompt — neither should be restored.
	in := bufio.NewReader(strings.NewReader("q\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, dangling, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(*restored) != 0 {
		t.Errorf("quit still restored: %v", *restored)
	}
}

func TestDoctorTriageRestoreErrorIsReported(t *testing.T) {
	restored := stubStoreStash(t, errors.New("boom: bad object"))
	store := newTempStore(t)
	dangling := []git.DanglingStash{{SHA: "sha1", Subject: "WIP on main: x", Source: "reflog"}}

	var stdout, stderr bytes.Buffer
	in := bufio.NewReader(strings.NewReader("r\n"))
	code := doctorTriage(context.Background(), in, &stdout, &stderr, store, dangling, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a per-item restore error is non-fatal)", code)
	}
	// StoreStash was attempted, and the error surfaced on stderr.
	if len(*restored) != 1 {
		t.Errorf("restore not attempted: %v", *restored)
	}
	if !strings.Contains(stderr.String(), "could not restore") {
		t.Errorf("stderr = %q, want a restore-failure note", stderr.String())
	}
}

// --- small helpers for the doctor tests ----------------------------------

// newTempStore returns a *meta.Store bound to a fresh temp git repo so Save
// works, without seeding any entries. Tests add labels as needed.
func newTempStore(t *testing.T) *meta.Store {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	prev, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(prev)
	s, err := meta.Load(context.Background())
	if err != nil {
		t.Fatalf("meta.Load: %v", err)
	}
	return s
}

// loadSidecarStore loads the store for the temp repo most recently bound by
// tempRepoSidecar (via the indirected metaLoad), so a test can seed entries
// through the same path runDoctor will read.
func loadSidecarStore(t *testing.T) *meta.Store {
	t.Helper()
	s, err := metaLoad(context.Background())
	if err != nil {
		t.Fatalf("metaLoad: %v", err)
	}
	return s
}

// seedSidecar writes the given sha->label entries into the temp repo's sidecar
// via the indirected metaLoad path, so runDoctor sees them as orphans.
func seedSidecar(t *testing.T, dir string, labels map[string]string) {
	t.Helper()
	s, err := metaLoad(context.Background())
	if err != nil {
		t.Fatalf("seed metaLoad: %v", err)
	}
	for sha, label := range labels {
		s.SetLabel(sha, label)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
}

// writeSidecar writes a sidecar JSON with the given SHA->tags into the temp
// repo's git dir so --tag filtering has metadata to read.
func writeSidecarTags(t *testing.T, dir string, tagsBySHA map[string][]string) {
	t.Helper()
	type entry struct {
		Tags []string `json:"tags,omitempty"`
	}
	payload := struct {
		Version int              `json:"version"`
		Entries map[string]entry `json:"entries"`
	}{Version: 1, Entries: map[string]entry{}}
	for sha, tags := range tagsBySHA {
		payload.Entries[sha] = entry{Tags: tags}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "stash-stash.json"), data, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func TestTagFilterNarrowsTable(t *testing.T) {
	stubList(t, []model.Stash{
		{Index: 0, SHA: "aaa", Subject: "WIP on main: alpha", Branch: "main"},
		{Index: 1, SHA: "bbb", Subject: "On feature/x: beta", Branch: "feature/x"},
		{Index: 2, SHA: "ccc", Subject: "WIP on main: gamma", Branch: "main"},
	}, nil)
	dir := tempRepoSidecar(t)
	writeSidecarTags(t, dir, map[string][]string{
		"aaa": {"wip"},
		"ccc": {"wip", "hotfix"},
	})

	// --tag wip → aaa and ccc, not bbb.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--no-tui", "--tag", "wip"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "stash@{0}") || !strings.Contains(out, "stash@{2}") {
		t.Errorf("--tag wip should include aaa+ccc rows:\n%s", out)
	}
	if strings.Contains(out, "stash@{1}") {
		t.Errorf("--tag wip should exclude bbb row:\n%s", out)
	}
	if !strings.Contains(out, "#wip") {
		t.Errorf("table should show #wip tag token:\n%s", out)
	}

	// --tag wip --tag hotfix (AND) → only ccc.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--no-tui", "--tag", "wip", "--tag", "hotfix"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	out = stdout.String()
	if !strings.Contains(out, "stash@{2}") {
		t.Errorf("AND filter should include ccc:\n%s", out)
	}
	if strings.Contains(out, "stash@{0}") || strings.Contains(out, "stash@{1}") {
		t.Errorf("AND filter should exclude aaa+bbb:\n%s", out)
	}
}

func TestTagFilterAppliesToJSON(t *testing.T) {
	stubList(t, []model.Stash{
		{Index: 0, SHA: "aaa", Subject: "WIP on main: alpha", Branch: "main"},
		{Index: 1, SHA: "bbb", Subject: "On feature/x: beta", Branch: "feature/x"},
	}, nil)
	dir := tempRepoSidecar(t)
	writeSidecarTags(t, dir, map[string][]string{"aaa": {"wip"}})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--json", "--tag", "wip"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	var got struct {
		Count   int `json:"count"`
		Stashes []struct {
			SHA  string   `json:"sha"`
			Tags []string `json:"tags"`
		} `json:"stashes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.Count != 1 || len(got.Stashes) != 1 || got.Stashes[0].SHA != "aaa" {
		t.Errorf("--json --tag wip = %+v, want only aaa", got)
	}
	if len(got.Stashes[0].Tags) != 1 || got.Stashes[0].Tags[0] != "wip" {
		t.Errorf("json row tags = %v, want [wip]", got.Stashes[0].Tags)
	}
}
