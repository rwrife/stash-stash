package git

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mkRepo creates a fake repo dir (a directory with a .git subdir) under root.
func mkRepo(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkRepo %s: %v", rel, err)
	}
	return dir
}

func TestDiscoverReposFindsAndSkips(t *testing.T) {
	root := t.TempDir()
	a := mkRepo(t, root, "a")
	b := mkRepo(t, root, "nested/b")
	// A repo buried inside node_modules must be skipped.
	mkRepo(t, root, "node_modules/pkg")
	// A plain directory (no .git) is not a repo.
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A repo nested *inside* another repo should not be descended into.
	mkRepo(t, root, "a/inner")

	got, err := DiscoverRepos(root)
	if err != nil {
		t.Fatalf("DiscoverRepos: %v", err)
	}
	want := []string{a, b}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v repos, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("repo[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDiscoverReposRootNotADir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverRepos(f); err == nil {
		t.Fatal("expected error for non-directory root")
	}
	if _, err := DiscoverRepos(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing root")
	}
}

// stubRunnerIn swaps runnerIn to return canned per-repo output.
func stubRunnerIn(t *testing.T, fn func(dir string, args ...string) ([]byte, []byte, error)) {
	t.Helper()
	orig := runnerIn
	runnerIn = func(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
		return fn(dir, args...)
	}
	t.Cleanup(func() { runnerIn = orig })
}

func TestListInParses(t *testing.T) {
	stubRunnerIn(t, func(dir string, args ...string) ([]byte, []byte, error) {
		return []byte("deadbeef\x1f1718560800\x1fWIP on main: x\n"), nil, nil
	})
	got, err := ListIn(context.Background(), "/some/repo")
	if err != nil {
		t.Fatalf("ListIn: %v", err)
	}
	if len(got) != 1 || got[0].SHA != "deadbeef" || got[0].Branch != "main" {
		t.Fatalf("ListIn parsed wrong: %+v", got)
	}
}

func TestListInNotARepo(t *testing.T) {
	stubRunnerIn(t, func(dir string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("fatal: not a git repository"), errStub{}
	})
	if _, err := ListIn(context.Background(), "/x"); err != ErrNotARepo {
		t.Fatalf("ListIn err = %v, want ErrNotARepo", err)
	}
}

func TestScanReposSkipsEmptyAndBroken(t *testing.T) {
	root := t.TempDir()
	withStash := mkRepo(t, root, "hasstash")
	mkRepo(t, root, "empty")
	mkRepo(t, root, "broken")

	stubRunnerIn(t, func(dir string, args ...string) ([]byte, []byte, error) {
		switch dir {
		case withStash:
			return []byte("deadbeef\x1f1718560800\x1fWIP on main: x\n"), nil, nil
		case filepath.Join(root, "broken"):
			return nil, []byte("fatal: something"), errStub{}
		default: // empty repo: no stashes
			return []byte(""), nil, nil
		}
	})

	got, err := ScanRepos(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanRepos: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d repo groups, want 1: %+v", len(got), got)
	}
	if got[0].Repo != withStash || len(got[0].Stashes) != 1 {
		t.Fatalf("unexpected group: %+v", got[0])
	}
}

// errStub is a non-nil error whose text doesn't matter for these tests.
type errStub struct{}

func (errStub) Error() string { return "stub error" }
