package git

import (
	"context"
	"errors"
	"testing"
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
