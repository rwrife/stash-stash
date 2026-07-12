package git

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// fakeExit is a runner error that carries an exit code, mimicking
// *exec.ExitError for the merge-tree probe without spawning a process.
type fakeExit int

func (f fakeExit) Error() string { return "exit status " + strconv.Itoa(int(f)) }
func (f fakeExit) ExitCode() int { return int(f) }

// preflightStub routes git subcommands to canned responses so WouldConflict can
// be exercised deterministically. The key is the first arg (subcommand); for
// rev-parse we also match the rev.
func preflightStub(t *testing.T, fn func(args ...string) ([]byte, []byte, error)) {
	t.Helper()
	orig := runner
	runner = func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		return fn(args...)
	}
	t.Cleanup(func() { runner = orig })
}

func TestWouldConflictClean(t *testing.T) {
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		switch args[0] {
		case "rev-parse":
			// resolve base, stash, HEAD to distinct fake shas
			return []byte("abc123\n"), nil, nil
		case "merge-tree":
			// clean merge: just the tree oid, exit 0
			return []byte("deadbeeftree\n"), nil, nil
		}
		t.Fatalf("unexpected git call: %v", args)
		return nil, nil, nil
	})

	res, err := WouldConflict(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("WouldConflict error = %v", err)
	}
	if !res.Clean() {
		t.Fatalf("expected clean, got %+v", res)
	}
	if res.Conflicts || res.Undetermined {
		t.Errorf("clean result should not flag conflicts/undetermined: %+v", res)
	}
}

func TestWouldConflictDetectsConflicts(t *testing.T) {
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		switch args[0] {
		case "rev-parse":
			return []byte("abc123\n"), nil, nil
		case "merge-tree":
			// conflict: tree oid then conflicting paths, exit 1
			out := "mergedtreeoid\nfoo.go\nbar/baz.txt\n"
			return []byte(out), nil, fakeExit(1)
		}
		t.Fatalf("unexpected git call: %v", args)
		return nil, nil, nil
	})

	res, err := WouldConflict(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("WouldConflict error = %v", err)
	}
	if !res.Conflicts {
		t.Fatalf("expected conflicts, got %+v", res)
	}
	if res.Undetermined {
		t.Errorf("conflict result must not be undetermined")
	}
	want := []string{"foo.go", "bar/baz.txt"}
	if len(res.Files) != len(want) {
		t.Fatalf("files = %v, want %v", res.Files, want)
	}
	for i := range want {
		if res.Files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q", i, res.Files[i], want[i])
		}
	}
}

func TestWouldConflictStopsAtBlankLine(t *testing.T) {
	// Realistic merge-tree output: oid, conflicted paths, blank, then git's
	// informational "Auto-merging / CONFLICT" narration that must be ignored.
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		if args[0] == "rev-parse" {
			return []byte("abc123\n"), nil, nil
		}
		out := "treeoid\nf.txt\n\nAuto-merging f.txt\nCONFLICT (content): Merge conflict in f.txt\n"
		return []byte(out), nil, fakeExit(1)
	})
	res, _ := WouldConflict(context.Background(), "stash@{0}")
	if !res.Conflicts {
		t.Fatalf("expected conflict, got %+v", res)
	}
	if len(res.Files) != 1 || res.Files[0] != "f.txt" {
		t.Fatalf("files = %v, want [f.txt] (must ignore the message block)", res.Files)
	}
}

func TestWouldConflictDedupesPaths(t *testing.T) {
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		if args[0] == "rev-parse" {
			return []byte("abc123\n"), nil, nil
		}
		out := "treeoid\nfoo.go\nfoo.go\nbar.go\n"
		return []byte(out), nil, fakeExit(1)
	})
	res, _ := WouldConflict(context.Background(), "stash@{0}")
	if len(res.Files) != 2 {
		t.Fatalf("expected dedup to 2 files, got %v", res.Files)
	}
}

func TestWouldConflictFailsOpenOnProbeError(t *testing.T) {
	// merge-tree exits >1 (e.g. unknown flag on old git) -> undetermined, no error.
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		if args[0] == "rev-parse" {
			return []byte("abc123\n"), nil, nil
		}
		return nil, []byte("error: unknown option `--write-tree'\n"), fakeExit(129)
	})
	res, err := WouldConflict(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("should not error, got %v", err)
	}
	if !res.Undetermined {
		t.Fatalf("expected undetermined (fail open), got %+v", res)
	}
	if res.Conflicts {
		t.Error("must not report conflicts when the probe itself failed")
	}
	if res.Reason == "" {
		t.Error("undetermined result should carry a reason")
	}
}

func TestWouldConflictFailsOpenOnBadBase(t *testing.T) {
	// rev-parse of the base fails (empty stdout, non-zero) -> undetermined.
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		if args[0] == "rev-parse" {
			return nil, nil, fakeExit(1) // --quiet: bad rev
		}
		t.Fatalf("merge-tree should not run when base can't resolve")
		return nil, nil, nil
	})
	res, err := WouldConflict(context.Background(), "stash@{0}")
	if err != nil {
		t.Fatalf("should not error, got %v", err)
	}
	if !res.Undetermined {
		t.Fatalf("expected undetermined, got %+v", res)
	}
}

func TestWouldConflictNotARepo(t *testing.T) {
	preflightStub(t, func(args ...string) ([]byte, []byte, error) {
		return nil, []byte("fatal: not a git repository"), errors.New("exit status 128")
	})
	_, err := WouldConflict(context.Background(), "stash@{0}")
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestPreflightNote(t *testing.T) {
	if got := PreflightNote(PreflightResult{}); got != "" {
		t.Errorf("clean note = %q, want empty", got)
	}
	if got := PreflightNote(PreflightResult{Undetermined: true, Reason: "old git"}); !strings.Contains(got, "couldn't pre-check") {
		t.Errorf("undetermined note = %q", got)
	}
	c := PreflightNote(PreflightResult{Conflicts: true, Files: []string{"a.go", "b.go"}})
	if !strings.Contains(c, "2 files") || !strings.Contains(c, "a.go") {
		t.Errorf("conflict note = %q", c)
	}
	one := PreflightNote(PreflightResult{Conflicts: true, Files: []string{"a.go"}})
	if !strings.Contains(one, "1 file:") {
		t.Errorf("singular note = %q", one)
	}
}

func TestPreflightResultClean(t *testing.T) {
	if !(PreflightResult{}).Clean() {
		t.Error("zero result should be clean")
	}
	if (PreflightResult{Undetermined: true}).Clean() {
		t.Error("undetermined is not clean")
	}
	if (PreflightResult{Conflicts: true}).Clean() {
		t.Error("conflicts is not clean")
	}
}
