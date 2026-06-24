package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/model"
)

// stubRunner swaps the package runner for the duration of a test.
func stubRunner(t *testing.T, stdout, stderr string, err error) {
	t.Helper()
	orig := runner
	runner = func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		return []byte(stdout), []byte(stderr), err
	}
	t.Cleanup(func() { runner = orig })
}

func TestListParsesStashes(t *testing.T) {
	// Two stashes: an auto WIP stash and an explicit push, US-separated.
	out := "deadbeef\x1f1718560800\x1fWIP on main: a1b2c3d Tidy things\n" +
		"cafebabe\x1f1718000000\x1fOn feature/x: half-done modal\n"
	stubRunner(t, out, "", nil)

	got, err := List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	if got[0].Index != 0 || got[0].SHA != "deadbeef" {
		t.Errorf("stash[0] = %+v, want index 0 / sha deadbeef", got[0])
	}
	if got[0].Branch != "main" {
		t.Errorf("stash[0].Branch = %q, want %q", got[0].Branch, "main")
	}
	if got[0].Created.Unix() != 1718560800 {
		t.Errorf("stash[0].Created unix = %d, want 1718560800", got[0].Created.Unix())
	}

	if got[1].Index != 1 || got[1].Branch != "feature/x" {
		t.Errorf("stash[1] = %+v, want index 1 / branch feature/x", got[1])
	}
	if got[1].Ref() != "stash@{1}" {
		t.Errorf("stash[1].Ref() = %q, want stash@{1}", got[1].Ref())
	}
}

func TestListEmpty(t *testing.T) {
	stubRunner(t, "", "", nil)
	got, err := List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestListNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	_, err := List(context.Background())
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestListGitError(t *testing.T) {
	stubRunner(t, "", "fatal: something else broke", errors.New("exit status 1"))
	_, err := List(context.Background())
	if err == nil {
		t.Fatal("err = nil, want a git error")
	}
	if errors.Is(err, ErrNotARepo) {
		t.Errorf("err = %v, want a generic git error, not ErrNotARepo", err)
	}
}

func TestParseListSkipsMalformed(t *testing.T) {
	out := "\n" + // blank
		"only-one-field\n" + // too few fields
		"sha1\x1f1700000000\x1fWIP on main: real\n" // valid
	got := parseList([]byte(out))
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (malformed lines skipped)", len(got))
	}
	if got[0].Index != 0 || got[0].SHA != "sha1" {
		t.Errorf("got %+v, want index 0 / sha1", got[0])
	}
}

func TestBranchFromSubject(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"WIP on main: a1b2c3d Tidy", "main"},
		{"On feature/x: my message", "feature/x"},
		{"WIP on release/2.0: deadbee fix", "release/2.0"},
		{"garbage with no prefix", ""},
		{"On detached-head", "detached-head"},
	}
	for _, c := range cases {
		if got := branchFromSubject(c.in); got != c.want {
			t.Errorf("branchFromSubject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShowReturnsPatch(t *testing.T) {
	patch := "diff --git a/foo.txt b/foo.txt\n@@ -1 +1 @@\n-old\n+new\n"
	stubRunner(t, patch, "", nil)

	got, err := Show(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if got != patch {
		t.Errorf("Show() = %q, want %q", got, patch)
	}
}

func TestShowNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	_, err := Show(context.Background(), "stash@{0}")
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestShowGitError(t *testing.T) {
	stubRunner(t, "", "fatal: bad revision 'stash@{9}'", errors.New("exit status 128"))
	_, err := Show(context.Background(), "stash@{9}")
	if err == nil {
		t.Fatal("err = nil, want a git error")
	}
	if errors.Is(err, ErrNotARepo) {
		t.Errorf("err = %v, want a generic git error, not ErrNotARepo", err)
	}
}

func TestDiffstatParsesNumstat(t *testing.T) {
	// Two text files plus a binary one ("-\t-").
	out := "12\t3\tcmd/main.go\n" +
		"4\t0\tREADME.md\n" +
		"-\t-\tlogo.png\n"
	stubRunner(t, out, "", nil)

	ds, err := Diffstat(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("Diffstat() error = %v", err)
	}
	if ds.Added != 16 || ds.Deleted != 3 {
		t.Errorf("added/deleted = %d/%d, want 16/3", ds.Added, ds.Deleted)
	}
	if ds.Files != 3 {
		t.Errorf("files = %d, want 3", ds.Files)
	}
	if !ds.Binary {
		t.Error("Binary = false, want true (logo.png is binary)")
	}
}

func TestDiffstatEmpty(t *testing.T) {
	stubRunner(t, "", "", nil)
	ds, err := Diffstat(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("Diffstat() error = %v", err)
	}
	if !ds.IsZero() {
		t.Errorf("empty numstat = %+v, want zero diffstat", ds)
	}
}

// --- issue #7: top-file detection feeding auto-labels --------------------

func TestParseNumstatPicksHighestChurnTopFile(t *testing.T) {
	// docs/README.md has the most churn (40) so it wins over the .go files,
	// even though it isn't first in the list.
	out := "12\t3\tcmd/main.go\n" +
		"30\t10\tdocs/README.md\n" +
		"4\t0\tinternal/x/x.go\n"
	ds, top := parseNumstat([]byte(out))
	if ds.Files != 3 {
		t.Fatalf("files = %d, want 3", ds.Files)
	}
	if top != "docs/README.md" {
		t.Errorf("top file = %q, want docs/README.md (highest churn)", top)
	}
}

func TestParseNumstatTopFileTieKeepsFirst(t *testing.T) {
	// Equal churn (7) for both: the first-listed file wins (stable order).
	out := "5\t2\tfirst.go\n" +
		"3\t4\tsecond.go\n"
	_, top := parseNumstat([]byte(out))
	if top != "first.go" {
		t.Errorf("top file = %q, want first.go (tie keeps first)", top)
	}
}

func TestParseNumstatBinaryOnlyTopFile(t *testing.T) {
	// Only a binary change: it becomes the top file since there's no text edit.
	_, top := parseNumstat([]byte("-\t-\tlogo.png\n"))
	if top != "logo.png" {
		t.Errorf("top file = %q, want logo.png (only change)", top)
	}
}

func TestParseNumstatTextBeatsBinaryForTopFile(t *testing.T) {
	// A binary file listed first must not beat a real text edit as the hint.
	out := "-\t-\tlogo.png\n" +
		"1\t0\tnote.txt\n"
	_, top := parseNumstat([]byte(out))
	if top != "note.txt" {
		t.Errorf("top file = %q, want note.txt (text beats binary)", top)
	}
}

func TestParseNumstatSkipsMalformed(t *testing.T) {
	out := "\n" + // blank
		"only-two\tfields\n" + // too few tabs
		"7\t2\tgood.go\n" // valid
	ds, top := parseNumstat([]byte(out))
	if ds.Files != 1 || ds.Added != 7 || ds.Deleted != 2 {
		t.Errorf("parseNumstat skipped wrong lines: %+v", ds)
	}
	if top != "good.go" {
		t.Errorf("top file = %q, want good.go (only valid line)", top)
	}
}

func TestDiffstatNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	_, err := Diffstat(context.Background(), "stash@{0}")
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestEnrichDiffstatsAttachesStats(t *testing.T) {
	// One file, +5 -1, returned for every stash via the stub.
	stubRunner(t, "5\t1\tfile.go\n", "", nil)
	stashes := []model.Stash{
		{Index: 0, SHA: "a"},
		{Index: 1, SHA: "b"},
	}
	EnrichDiffstats(context.Background(), stashes)
	for i, s := range stashes {
		if s.Diffstat.Added != 5 || s.Diffstat.Deleted != 1 || s.Diffstat.Files != 1 {
			t.Errorf("stash[%d].Diffstat = %+v, want +5 -1 / 1 file", i, s.Diffstat)
		}
		if s.TopFile != "file.go" {
			t.Errorf("stash[%d].TopFile = %q, want file.go", i, s.TopFile)
		}
	}
}

func TestEnrichDiffstatsToleratesError(t *testing.T) {
	// A failing git call must leave a zero diffstat, not blow up the slice.
	stubRunner(t, "", "fatal: bad revision", errors.New("exit status 128"))
	stashes := []model.Stash{{Index: 0, SHA: "a"}}
	EnrichDiffstats(context.Background(), stashes)
	if !stashes[0].Diffstat.IsZero() {
		t.Errorf("errored diffstat = %+v, want zero", stashes[0].Diffstat)
	}
}

// --- M5: mutating operations ---------------------------------------------

// stubRunnerFunc swaps the package runner for a fully custom function so tests
// can vary behavior by git args (e.g. Push runs "stash push" then "rev-parse").
func stubRunnerFunc(t *testing.T, fn func(args ...string) ([]byte, []byte, error)) {
	t.Helper()
	orig := runner
	runner = func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		return fn(args...)
	}
	t.Cleanup(func() { runner = orig })
}

func TestApplyOK(t *testing.T) {
	var gotArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := Apply(context.Background(), "stash@{1}"); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []string{"stash", "apply", "stash@{1}"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("Apply args = %v, want %v", gotArgs, want)
	}
}

func TestApplyConflictSurfacesGitError(t *testing.T) {
	stubRunner(t, "", "CONFLICT (content): Merge conflict in foo.txt", errors.New("exit status 1"))
	err := Apply(context.Background(), "stash@{0}")
	if err == nil {
		t.Fatal("Apply() = nil, want a conflict error")
	}
	if !strings.Contains(err.Error(), "CONFLICT") {
		t.Errorf("Apply() error = %q, want it to mention the conflict", err)
	}
	if errors.Is(err, ErrNotARepo) {
		t.Errorf("Apply() conflict misreported as ErrNotARepo")
	}
}

func TestPopAndDropArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, string) error
		verb string
	}{
		{"pop", Pop, "pop"},
		{"drop", Drop, "drop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
				gotArgs = args
				return nil, nil, nil
			})
			if err := tc.call(context.Background(), "stash@{2}"); err != nil {
				t.Fatalf("%s() error = %v", tc.name, err)
			}
			want := "stash " + tc.verb + " stash@{2}"
			if strings.Join(gotArgs, " ") != want {
				t.Errorf("%s args = %v, want %q", tc.name, gotArgs, want)
			}
		})
	}
}

func TestDropNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	if err := Drop(context.Background(), "stash@{0}"); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("Drop() err = %v, want ErrNotARepo", err)
	}
}

func TestBranchArgs(t *testing.T) {
	var gotArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := Branch(context.Background(), "payments-retry", "stash@{1}"); err != nil {
		t.Fatalf("Branch() error = %v", err)
	}
	want := "stash branch payments-retry stash@{1}"
	if strings.Join(gotArgs, " ") != want {
		t.Errorf("Branch args = %v, want %q", gotArgs, want)
	}
}

func TestBranchTrimsName(t *testing.T) {
	var gotArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := Branch(context.Background(), "  spaced  ", "stash@{0}"); err != nil {
		t.Fatalf("Branch() error = %v", err)
	}
	// Surrounding whitespace is trimmed; the name token must not carry spaces.
	if strings.Join(gotArgs, " ") != "stash branch spaced stash@{0}" {
		t.Errorf("Branch args = %v, want a trimmed name", gotArgs)
	}
}

func TestBranchEmptyNameRejected(t *testing.T) {
	called := false
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		called = true
		return nil, nil, nil
	})
	err := Branch(context.Background(), "   ", "stash@{0}")
	if !errors.Is(err, ErrEmptyBranchName) {
		t.Fatalf("Branch() err = %v, want ErrEmptyBranchName", err)
	}
	if called {
		t.Error("Branch() shelled out to git despite an empty name")
	}
}

func TestBranchSurfacesGitError(t *testing.T) {
	stubRunner(t, "", "fatal: a branch named 'payments' already exists", errors.New("exit status 128"))
	err := Branch(context.Background(), "payments", "stash@{0}")
	if err == nil {
		t.Fatal("Branch() = nil, want a git error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Branch() error = %q, want it to surface git's message", err)
	}
	if errors.Is(err, ErrNotARepo) {
		t.Errorf("Branch() existing-branch error misreported as ErrNotARepo")
	}
}

func TestBranchNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	if err := Branch(context.Background(), "feature", "stash@{0}"); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("Branch() err = %v, want ErrNotARepo", err)
	}
}

func TestPushRecordsSHA(t *testing.T) {
	var calls [][]string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		calls = append(calls, args)
		switch {
		case len(args) >= 2 && args[0] == "stash" && args[1] == "push":
			return []byte("Saved working directory and index state On main: my label\n"), nil, nil
		case len(args) >= 1 && args[0] == "rev-parse":
			return []byte("abc123def456\n"), nil, nil
		}
		return nil, nil, errors.New("unexpected git call")
	})

	sha, err := Push(context.Background(), "my label")
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if sha != "abc123def456" {
		t.Errorf("Push() sha = %q, want abc123def456", sha)
	}
	// First call must include the -m label; second must resolve stash@{0}.
	if len(calls) != 2 {
		t.Fatalf("Push() made %d git calls, want 2 (push + rev-parse)", len(calls))
	}
	if strings.Join(calls[0], " ") != "stash push -m my label" {
		t.Errorf("push call = %v, want it to carry -m my label", calls[0])
	}
	if calls[1][0] != "rev-parse" || calls[1][1] != "stash@{0}" {
		t.Errorf("readback call = %v, want rev-parse stash@{0}", calls[1])
	}
}

func TestPushNoMessageSkipsDashM(t *testing.T) {
	var pushArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		if args[0] == "stash" {
			pushArgs = args
			return []byte("Saved working directory\n"), nil, nil
		}
		return []byte("deadbeef\n"), nil, nil
	})
	if _, err := Push(context.Background(), "   "); err != nil { // whitespace == no msg
		t.Fatalf("Push() error = %v", err)
	}
	if strings.Join(pushArgs, " ") != "stash push" {
		t.Errorf("push args = %v, want a bare 'stash push' (no -m)", pushArgs)
	}
}

func TestPushNothingToStash(t *testing.T) {
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		// git prints this to stdout and exits zero, creating no stash.
		return []byte("No local changes to save\n"), nil, nil
	})
	_, err := Push(context.Background(), "label")
	if !errors.Is(err, ErrNothingToStash) {
		t.Fatalf("Push() err = %v, want ErrNothingToStash", err)
	}
}

func TestPushNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	_, err := Push(context.Background(), "label")
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("Push() err = %v, want ErrNotARepo", err)
	}
}
