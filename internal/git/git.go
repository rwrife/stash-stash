// Package git is a thin, pure-Go wrapper around the user's real `git` binary.
//
// stash-stash never links libgit2 or reimplements git semantics; it shells out
// to `git` (inheriting the user's config and credentials) and parses the
// output. M2 implements List() and M3 adds Show() (read-only); mutating
// operations (apply/pop/drop/push) arrive in M5.
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
)

// fieldSep is an ASCII Unit Separator (0x1f). It cannot appear in a SHA, a
// unix timestamp, or — in practice — a stash subject, so it makes a safe,
// unambiguous delimiter for our custom `git stash list --format`.
const fieldSep = "\x1f"

// listFormat asks git for exactly the three fields we need per stash:
//
//	%H  full commit SHA
//	%at author date, unix timestamp
//	%gs reflog subject (the "WIP on main: ..." line)
//
// joined by fieldSep. %gs is the stash-specific subject; %s would give the
// underlying commit's subject instead.
var listFormat = "%H" + fieldSep + "%at" + fieldSep + "%gs"

// ErrNotARepo is returned by List when the current directory is not inside a
// git work tree. Callers should treat this as a friendly, expected condition,
// not a crash.
var ErrNotARepo = errors.New("not a git repository")

// runner executes a git invocation and returns its stdout. It is a package
// variable so tests can stub git without a real binary or repo.
var runner = func(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// List returns all stashes in the current repository, most-recent first
// (matching `git stash list` / stash@{0} ordering).
//
// It returns ErrNotARepo when run outside a git work tree, and an empty slice
// (nil error) when the repo simply has no stashes.
func List(ctx context.Context) ([]model.Stash, error) {
	stdout, stderr, err := runner(ctx, "stash", "list", "--format="+listFormat)
	if err != nil {
		// git prints "not a git repository" to stderr and exits non-zero.
		if isNotARepo(stderr) {
			return nil, ErrNotARepo
		}
		// Surface the real git error message when we have one.
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("git stash list: %s", msg)
		}
		return nil, fmt.Errorf("git stash list: %w", err)
	}

	return parseList(stdout), nil
}

// Show returns the patch for a single stash as produced by
// `git stash show -p <ref>` (e.g. ref "stash@{0}"). It is read-only and used
// by the TUI preview pane (M3).
//
// Like List, it returns ErrNotARepo outside a work tree and surfaces git's own
// error text otherwise. An empty (whitespace-only) diff yields an empty string.
func Show(ctx context.Context, ref string) (string, error) {
	stdout, stderr, err := runner(ctx, "stash", "show", "-p", ref)
	if err != nil {
		if isNotARepo(stderr) {
			return "", ErrNotARepo
		}
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return "", fmt.Errorf("git stash show %s: %s", ref, msg)
		}
		return "", fmt.Errorf("git stash show %s: %w", ref, err)
	}
	return string(stdout), nil
}

// parseList turns the raw `git stash list --format` output into Stash structs.
// Malformed or blank lines are skipped defensively rather than failing the
// whole listing.
func parseList(out []byte) []model.Stash {
	var stashes []model.Stash
	sc := bufio.NewScanner(bytes.NewReader(out))
	// Stash subjects are short; the default 64KiB buffer is plenty, but be
	// explicit about a generous max line length for safety.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	idx := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}

		var created time.Time
		if ts, perr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); perr == nil {
			created = time.Unix(ts, 0)
		}

		subject := strings.TrimSpace(parts[2])
		stashes = append(stashes, model.Stash{
			Index:   idx,
			SHA:     strings.TrimSpace(parts[0]),
			Subject: subject,
			Branch:  branchFromSubject(subject),
			Created: created,
		})
		idx++
	}
	return stashes
}

// branchFromSubject extracts the origin branch from a stash subject.
//
// Git writes stash subjects in a few shapes:
//
//	"WIP on main: a1b2c3d Tidy things"   -> auto stash on `main`
//	"On feature/x: my message"           -> `git stash push -m "my message"`
//
// We pull the branch token between the "(WIP )on " prefix and the first ":".
// Returns "" if the subject doesn't match a known shape.
func branchFromSubject(subject string) string {
	s := subject
	switch {
	case strings.HasPrefix(s, "WIP on "):
		s = strings.TrimPrefix(s, "WIP on ")
	case strings.HasPrefix(s, "On "):
		s = strings.TrimPrefix(s, "On ")
	default:
		return ""
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// isNotARepo reports whether git's stderr indicates we're outside a work tree.
func isNotARepo(stderr []byte) bool {
	msg := strings.ToLower(string(stderr))
	return strings.Contains(msg, "not a git repository") ||
		strings.Contains(msg, "this operation must be run in a work tree")
}
