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

// stubArchive swaps the indirected gitArchive, recording the refs it was asked
// to archive and returning a per-ref SHA (or error).
func stubArchive(t *testing.T, fn func(ctx context.Context, ref string) (string, error)) {
	t.Helper()
	orig := gitArchive
	gitArchive = fn
	t.Cleanup(func() { gitArchive = orig })
}

func stubArchivedStashes(t *testing.T, out []git.ArchivedStash, err error) {
	t.Helper()
	orig := gitArchivedStashes
	gitArchivedStashes = func(ctx context.Context) ([]git.ArchivedStash, error) { return out, err }
	t.Cleanup(func() { gitArchivedStashes = orig })
}

func stubRestore(t *testing.T, fn func(ctx context.Context, id string) (string, error)) {
	t.Helper()
	orig := gitRestore
	gitRestore = fn
	t.Cleanup(func() { gitRestore = orig })
}

func TestCompostArchivesStaleOnly(t *testing.T) {
	freshSHA := "aaaa000000000000000000000000000000000000"
	staleSHA := "bbbb000000000000000000000000000000000000"
	stashes := []model.Stash{
		{Index: 0, SHA: freshSHA, Subject: "On main: fresh", Created: time.Now().Add(-1 * time.Hour)},
		{Index: 1, SHA: staleSHA, Subject: "On main: stale", Created: time.Now().Add(-40 * 24 * time.Hour)},
	}
	stubList(t, stashes, nil)

	var archivedRefs []string
	stubArchive(t, func(ctx context.Context, ref string) (string, error) {
		archivedRefs = append(archivedRefs, ref)
		// Map ref back to a SHA for the return; not used further here.
		return staleSHA, nil
	})

	var out, errb bytes.Buffer
	code := runCompost([]string{"--stale-days", "14"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if len(archivedRefs) != 1 {
		t.Fatalf("archived %d stashes, want 1 (only the stale one)", len(archivedRefs))
	}
	if !strings.Contains(out.String(), "composted") {
		t.Errorf("output should confirm a compost, got %q", out.String())
	}
}

func TestCompostAllWithZeroStaleDays(t *testing.T) {
	stashes := []model.Stash{
		{Index: 0, SHA: "1111111111111111111111111111111111111111", Subject: "On main: a", Created: time.Now()},
		{Index: 1, SHA: "2222222222222222222222222222222222222222", Subject: "On main: b", Created: time.Now()},
	}
	stubList(t, stashes, nil)
	count := 0
	stubArchive(t, func(ctx context.Context, ref string) (string, error) {
		count++
		return "", nil
	})
	var out, errb bytes.Buffer
	code := runCompost([]string{"--stale-days", "0"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if count != 2 {
		t.Errorf("archived %d, want 2 (stale-days 0 composts all)", count)
	}
}

func TestCompostNothingStale(t *testing.T) {
	stashes := []model.Stash{
		{Index: 0, SHA: "1111111111111111111111111111111111111111", Subject: "On main: a", Created: time.Now()},
	}
	stubList(t, stashes, nil)
	stubArchive(t, func(ctx context.Context, ref string) (string, error) {
		t.Error("should not archive a fresh stash")
		return "", nil
	})
	var out, errb bytes.Buffer
	code := runCompost([]string{"--stale-days", "14"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "fresh") && !strings.Contains(out.String(), "Nothing") {
		t.Errorf("expected a 'nothing to compost' message, got %q", out.String())
	}
}

func TestCompostEmptyGraveyard(t *testing.T) {
	stubList(t, nil, nil)
	var out, errb bytes.Buffer
	code := runCompost(nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "empty") {
		t.Errorf("expected empty-graveyard message, got %q", out.String())
	}
}

func TestCompostList(t *testing.T) {
	stubArchivedStashes(t, []git.ArchivedStash{
		{
			SHA:        "abcdef1234567890abcdef1234567890abcdef12",
			Subject:    "On feature/x: half-done modal",
			Branch:     "feature/x",
			Created:    time.Now().Add(-30 * 24 * time.Hour),
			ArchivedAt: time.Now().Add(-2 * 24 * time.Hour),
		},
	}, nil)
	var out, errb bytes.Buffer
	code := runCompost([]string{"--list"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "abcdef123456") {
		t.Errorf("list should show the short SHA, got %q", s)
	}
	if !strings.Contains(s, "archived") {
		t.Errorf("list should show archived-at age, got %q", s)
	}
}

func TestCompostListEmpty(t *testing.T) {
	stubArchivedStashes(t, nil, nil)
	var out, errb bytes.Buffer
	code := runCompost([]string{"--list"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "empty") {
		t.Errorf("expected empty-archive message, got %q", out.String())
	}
}

func TestCompostRestore(t *testing.T) {
	const sha = "abcdef1234567890abcdef1234567890abcdef12"
	var gotID string
	stubRestore(t, func(ctx context.Context, id string) (string, error) {
		gotID = id
		return sha, nil
	})
	var out, errb bytes.Buffer
	code := runCompost([]string{"--restore", "abcdef12"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if gotID != "abcdef12" {
		t.Errorf("restore id = %q, want abcdef12", gotID)
	}
	if !strings.Contains(out.String(), "restored") {
		t.Errorf("expected restore confirmation, got %q", out.String())
	}
}

func TestCompostRestoreNotFound(t *testing.T) {
	stubRestore(t, func(ctx context.Context, id string) (string, error) {
		return "", git.ErrNotArchived
	})
	var out, errb bytes.Buffer
	code := runCompost([]string{"--restore", "deadbeef"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "no composted stash") {
		t.Errorf("expected not-found message, got %q", errb.String())
	}
}

func TestCompostListAndRestoreMutuallyExclusive(t *testing.T) {
	var out, errb bytes.Buffer
	code := runCompost([]string{"--list", "--restore", "x"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for conflicting flags", code)
	}
}

func TestCompostNegativeStaleDays(t *testing.T) {
	var out, errb bytes.Buffer
	code := runCompost([]string{"--stale-days", "-1"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestCompostNotARepo(t *testing.T) {
	stubList(t, nil, git.ErrNotARepo)
	var out, errb bytes.Buffer
	code := runCompost(nil, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not a git repository") {
		t.Errorf("expected not-a-repo message, got %q", errb.String())
	}
}

// Guard: the archive layer's already-archived signal is treated as a skip,
// not a failure, during bulk compost.
func TestCompostSkipsAlreadyArchived(t *testing.T) {
	stashes := []model.Stash{
		{Index: 0, SHA: "1111111111111111111111111111111111111111", Subject: "On main: a", Created: time.Now().Add(-40 * 24 * time.Hour)},
	}
	stubList(t, stashes, nil)
	stubArchive(t, func(ctx context.Context, ref string) (string, error) {
		return "", git.ErrAlreadyArchived
	})
	var out, errb bytes.Buffer
	code := runCompost([]string{"--stale-days", "14"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !errors.Is(git.ErrAlreadyArchived, git.ErrAlreadyArchived) {
		t.Fatal("sanity")
	}
}
