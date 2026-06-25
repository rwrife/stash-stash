package git

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// us joins fields with the package's unit separator, matching the --format
// output git produces for our reflog/log calls.
func us(fields ...string) string { return strings.Join(fields, fieldSep) }

func TestDanglingStashesFromReflog(t *testing.T) {
	// Live stack has one stash (sha-live). The reflog also remembers a dropped
	// stash (sha-dropped) plus the live one — only the dropped one is dangling.
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		switch {
		case args[0] == "stash" && args[1] == "list":
			return []byte(us("sha-live", "1718560800", "WIP on main: live one") + "\n"), nil, nil
		case args[0] == "reflog":
			out := us("sha-dropped", "1700000000", "WIP on feature/x: dropped one") + "\n" +
				us("sha-live", "1718560800", "WIP on main: live one") + "\n"
			return []byte(out), nil, nil
		case args[0] == "fsck":
			return nil, nil, nil // no fsck candidates in this test
		}
		return nil, nil, errors.New("unexpected git call: " + strings.Join(args, " "))
	})

	got, err := DanglingStashes(context.Background())
	if err != nil {
		t.Fatalf("DanglingStashes() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only the dropped stash) — got %+v", len(got), got)
	}
	d := got[0]
	if d.SHA != "sha-dropped" {
		t.Errorf("SHA = %q, want sha-dropped", d.SHA)
	}
	if d.Branch != "feature/x" {
		t.Errorf("Branch = %q, want feature/x", d.Branch)
	}
	if d.Source != "reflog" {
		t.Errorf("Source = %q, want reflog", d.Source)
	}
	if d.Created.Unix() != 1700000000 {
		t.Errorf("Created unix = %d, want 1700000000", d.Created.Unix())
	}
	if d.Ref() != "sha-dropped" {
		t.Errorf("Ref() = %q, want the SHA", d.Ref())
	}
}

func TestDanglingStashesFromFsck(t *testing.T) {
	// Empty reflog; fsck surfaces two unreachable commits, only one of which is
	// stash-shaped (the other is a random orphaned commit and must be ignored).
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		switch {
		case args[0] == "stash" && args[1] == "list":
			return nil, nil, nil // no live stashes
		case args[0] == "reflog":
			return nil, nil, errors.New("no stash reflog") // never had a stash ref
		case args[0] == "fsck":
			out := "unreachable commit deadbeefcafe\n" +
				"unreachable commit 0badc0de0bad\n" +
				"unreachable blob 1234\n" // non-commit line ignored
			return []byte(out), nil, nil
		case args[0] == "log":
			// Batched describe: deadbeef is a stash; 0badc0de is not.
			out := us("deadbeefcafe", "1699000000", "WIP on main: orphaned by reset") + "\n" +
				us("0badc0de0bad", "1699500000", "regular commit, not a stash") + "\n"
			return []byte(out), nil, nil
		}
		return nil, nil, errors.New("unexpected git call: " + strings.Join(args, " "))
	})

	got, err := DanglingStashes(context.Background())
	if err != nil {
		t.Fatalf("DanglingStashes() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only the stash-shaped commit) — got %+v", len(got), got)
	}
	if got[0].SHA != "deadbeefcafe" {
		t.Errorf("SHA = %q, want deadbeefcafe", got[0].SHA)
	}
	if got[0].Source != "fsck" {
		t.Errorf("Source = %q, want fsck", got[0].Source)
	}
}

func TestDanglingStashesUnionsAndExcludesLive(t *testing.T) {
	// Both sources report candidates; a live stash appears in the reflog and
	// must be excluded; a SHA appears in both sources and must be de-duped
	// (reflog wins its Source). Results come back newest-first.
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		switch {
		case args[0] == "stash" && args[1] == "list":
			return []byte(us("sha-live", "1718560800", "WIP on main: live") + "\n"), nil, nil
		case args[0] == "reflog":
			out := us("sha-old", "1600000000", "WIP on a: old dropped") + "\n" +
				us("sha-both", "1700000000", "WIP on b: in both sources") + "\n" +
				us("sha-live", "1718560800", "WIP on main: live") + "\n"
			return []byte(out), nil, nil
		case args[0] == "fsck":
			return []byte("unreachable commit sha-both\nunreachable commit sha-new\n"), nil, nil
		case args[0] == "log":
			out := us("sha-both", "1700000000", "WIP on b: in both sources") + "\n" +
				us("sha-new", "1710000000", "WIP on c: newest dangling") + "\n"
			return []byte(out), nil, nil
		}
		return nil, nil, errors.New("unexpected git call: " + strings.Join(args, " "))
	})

	got, err := DanglingStashes(context.Background())
	if err != nil {
		t.Fatalf("DanglingStashes() error = %v", err)
	}
	// Expect sha-old, sha-both, sha-new (3 distinct, no live) newest-first:
	// sha-new (1710000000) > sha-both (1700000000) > sha-old (1600000000).
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 — got %+v", len(got), got)
	}
	order := []string{got[0].SHA, got[1].SHA, got[2].SHA}
	want := []string{"sha-new", "sha-both", "sha-old"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v (newest first)", order, want)
		}
	}
	// sha-both was seen first in the reflog, so it keeps the reflog source.
	for _, d := range got {
		if d.SHA == "sha-both" && d.Source != "reflog" {
			t.Errorf("sha-both Source = %q, want reflog (first source wins)", d.Source)
		}
		if d.SHA == "sha-live" {
			t.Error("a live stash leaked into the dangling set")
		}
	}
}

func TestDanglingStashesNotARepo(t *testing.T) {
	// List fails outside a work tree; DanglingStashes surfaces ErrNotARepo.
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	_, err := DanglingStashes(context.Background())
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("err = %v, want ErrNotARepo", err)
	}
}

func TestDanglingStashesBothSourcesEmpty(t *testing.T) {
	// A healthy repo: a live stash but nothing dangling. Reflog only has the
	// live one; fsck finds nothing. Result is an empty slice, no error.
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		switch {
		case args[0] == "stash" && args[1] == "list":
			return []byte(us("sha-live", "1718560800", "WIP on main: live") + "\n"), nil, nil
		case args[0] == "reflog":
			return []byte(us("sha-live", "1718560800", "WIP on main: live") + "\n"), nil, nil
		case args[0] == "fsck":
			return nil, nil, nil
		}
		return nil, nil, errors.New("unexpected git call: " + strings.Join(args, " "))
	})
	got, err := DanglingStashes(context.Background())
	if err != nil {
		t.Fatalf("DanglingStashes() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (nothing dangling) — got %+v", len(got), got)
	}
}

func TestLooksLikeStashSubject(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"WIP on main: a1b2c3d Tidy", true},
		{"On feature/x: my message", true},
		{"  WIP on main: padded", true},
		{"regular commit subject", false},
		{"Wipe on main: not it", false},
		{"", false},
	}
	for _, c := range cases {
		if got := looksLikeStashSubject(c.in); got != c.want {
			t.Errorf("looksLikeStashSubject(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseUnix(t *testing.T) {
	if got := parseUnix("1700000000"); got.Unix() != 1700000000 {
		t.Errorf("parseUnix valid = %v, want unix 1700000000", got)
	}
	if got := parseUnix("not-a-number"); !got.IsZero() {
		t.Errorf("parseUnix garbage = %v, want zero time", got)
	}
	if got := parseUnix("  1699999999  "); got.Unix() != 1699999999 {
		t.Errorf("parseUnix trims whitespace = %v, want unix 1699999999", got)
	}
	_ = time.Now // keep time import meaningful if the above changes
}

func TestStoreStashArgs(t *testing.T) {
	var gotArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := StoreStash(context.Background(), "deadbeef", "WIP on main: recovered"); err != nil {
		t.Fatalf("StoreStash() error = %v", err)
	}
	want := "stash store -m WIP on main: recovered deadbeef"
	if strings.Join(gotArgs, " ") != want {
		t.Errorf("StoreStash args = %v, want %q", gotArgs, want)
	}
}

func TestStoreStashNoMessageSkipsDashM(t *testing.T) {
	var gotArgs []string
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := StoreStash(context.Background(), "deadbeef", "   "); err != nil {
		t.Fatalf("StoreStash() error = %v", err)
	}
	if strings.Join(gotArgs, " ") != "stash store deadbeef" {
		t.Errorf("StoreStash args = %v, want a bare 'stash store <sha>' (no -m)", gotArgs)
	}
}

func TestStoreStashEmptySHARejected(t *testing.T) {
	called := false
	stubRunnerFunc(t, func(args ...string) ([]byte, []byte, error) {
		called = true
		return nil, nil, nil
	})
	if err := StoreStash(context.Background(), "   ", "msg"); err == nil {
		t.Fatal("StoreStash with empty SHA should error")
	}
	if called {
		t.Error("StoreStash shelled out to git despite an empty SHA")
	}
}

func TestStoreStashSurfacesGitError(t *testing.T) {
	stubRunner(t, "", "fatal: not a valid object name deadbeef", errors.New("exit status 128"))
	err := StoreStash(context.Background(), "deadbeef", "msg")
	if err == nil {
		t.Fatal("StoreStash() = nil, want a git error")
	}
	if !strings.Contains(err.Error(), "not a valid object name") {
		t.Errorf("StoreStash() error = %q, want git's message surfaced", err)
	}
}

func TestStoreStashNotARepo(t *testing.T) {
	stubRunner(t, "", "fatal: not a git repository (or any of the parent directories): .git", errors.New("exit status 128"))
	if err := StoreStash(context.Background(), "deadbeef", "msg"); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("StoreStash() err = %v, want ErrNotARepo", err)
	}
}
