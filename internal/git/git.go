// Package git is a thin, pure-Go wrapper around the user's real `git` binary.
//
// stash-stash never links libgit2 or reimplements git semantics; it shells out
// to `git` (inheriting the user's config and credentials) and parses the
// output. M2 implements List(); M3 adds Show() (read-only); M4 adds Diffstat()
// plus EnrichDiffstats(). M5 adds the mutating operations Apply, Pop, Drop, and
// Push — each kept thin (shell out, parse, surface git's own errors) and, for
// the TUI's sake, returning enough information (e.g. the new stash SHA on Push)
// to keep the sidecar metadata coherent.
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

// Diffstat returns the added/deleted/files summary for a single stash via
// `git stash show --numstat <ref>` (M4). --numstat is stable, machine-parsable
// output ("<added>\t<deleted>\t<path>" per file; binary files report "-\t-").
//
// Like Show it returns ErrNotARepo outside a work tree and surfaces git's own
// error text otherwise. A stash that touches nothing yields a zero Diffstat.
func Diffstat(ctx context.Context, ref string) (model.Diffstat, error) {
	ds, _, err := diffstatAndTopFile(ctx, ref)
	return ds, err
}

// diffstatAndTopFile runs `git stash show --numstat <ref>` once and returns
// both the aggregate Diffstat and the stash's most significant changed file
// (the "top file", used to derive an auto-label — issue #7). Sharing the single
// git call keeps enrichment to one subprocess per stash.
func diffstatAndTopFile(ctx context.Context, ref string) (model.Diffstat, string, error) {
	stdout, stderr, err := runner(ctx, "stash", "show", "--numstat", ref)
	if err != nil {
		if isNotARepo(stderr) {
			return model.Diffstat{}, "", ErrNotARepo
		}
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return model.Diffstat{}, "", fmt.Errorf("git stash show --numstat %s: %s", ref, msg)
		}
		return model.Diffstat{}, "", fmt.Errorf("git stash show --numstat %s: %w", ref, err)
	}
	ds, top := parseNumstat(stdout)
	return ds, top, nil
}

// parseNumstat turns `git diff --numstat` style output into a Diffstat plus the
// path of the "top" (most significant) changed file. Each non-blank line is
// "<added>\t<deleted>\t<path>"; binary files use "-" for the counts. Malformed
// lines are skipped defensively.
//
// The top file is the one with the greatest added+deleted churn; ties keep the
// first one seen (git lists files in a stable order). A binary-only change has
// no line counts, so a binary file only becomes "top" when no text file has any
// churn — preferring a meaningful text edit as the auto-label hint.
func parseNumstat(out []byte) (model.Diffstat, string) {
	var ds model.Diffstat
	var topFile string
	bestChurn := -1
	sawTextFile := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		ds.Files++
		path := strings.TrimSpace(parts[2])
		addTok, delTok := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if addTok == "-" || delTok == "-" {
			ds.Binary = true
			// Binary file: no churn signal. Only adopt it as the top file if we
			// have not seen any text file at all yet, so a real edit always wins.
			if !sawTextFile && topFile == "" {
				topFile = path
			}
			continue
		}
		added, deleted := 0, 0
		if n, perr := strconv.Atoi(addTok); perr == nil {
			added = n
		}
		if n, perr := strconv.Atoi(delTok); perr == nil {
			deleted = n
		}
		ds.Added += added
		ds.Deleted += deleted
		churn := added + deleted
		if !sawTextFile || churn > bestChurn {
			sawTextFile = true
			bestChurn = churn
			topFile = path
		}
	}
	return ds, topFile
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

// Apply applies a stash onto the working tree via `git stash apply <ref>`,
// leaving the stash in the stack (M5). It is the non-destructive cousin of
// Pop: callers that want the entry gone afterwards use Pop instead.
//
// It returns ErrNotARepo outside a work tree and surfaces git's own error text
// otherwise — most importantly merge conflicts, which exit non-zero. Callers
// should present that message verbatim so the user can resolve it.
func Apply(ctx context.Context, ref string) error {
	return runMutation(ctx, fmt.Sprintf("git stash apply %s", ref), "stash", "apply", ref)
}

// Pop applies a stash and then drops it via `git stash pop <ref>` (M5). This is
// destructive in the sense that the stash entry is removed — but only once git
// successfully applies it. If the apply conflicts, git leaves the stash in
// place and exits non-zero; we surface that error so the caller can warn and
// the sidecar entry is preserved.
func Pop(ctx context.Context, ref string) error {
	return runMutation(ctx, fmt.Sprintf("git stash pop %s", ref), "stash", "pop", ref)
}

// Drop deletes a stash without applying it via `git stash drop <ref>` (M5).
// This permanently removes the entry (recoverable only via reflog), so the TUI
// must gate it behind an explicit confirm before calling here.
//
// It returns ErrNotARepo outside a work tree and surfaces git's own error text
// otherwise.
func Drop(ctx context.Context, ref string) error {
	return runMutation(ctx, fmt.Sprintf("git stash drop %s", ref), "stash", "drop", ref)
}

// Push stashes the current working-tree changes via `git stash push -m <msg>`
// and returns the content SHA of the newly-created stash (always stash@{0}
// immediately after a successful push), so the caller can record the label in
// the sidecar keyed by that SHA (M5).
//
// A message is optional: an empty msg runs a plain `git stash push`, letting
// git pick its default "WIP on <branch>" subject. When there is nothing to
// stash git prints "No local changes to save" and exits zero with no new
// stash; Push detects that and returns ErrNothingToStash so callers can report
// it cleanly rather than recording a label against a stash that doesn't exist.
func Push(ctx context.Context, msg string) (string, error) {
	args := []string{"stash", "push"}
	if strings.TrimSpace(msg) != "" {
		args = append(args, "-m", msg)
	}
	stdout, stderr, err := runner(ctx, args...)
	if err != nil {
		if isNotARepo(stderr) {
			return "", ErrNotARepo
		}
		if m := strings.TrimSpace(string(stderr)); m != "" {
			return "", fmt.Errorf("git stash push: %s", m)
		}
		return "", fmt.Errorf("git stash push: %w", err)
	}

	// git prints "No local changes to save" (to stdout) and creates nothing.
	if noChangesToStash(stdout) {
		return "", ErrNothingToStash
	}

	// Read back the SHA of the stash we just created. It is always stash@{0}
	// right after a successful push.
	sha, serr := resolveStashSHA(ctx, "stash@{0}")
	if serr != nil {
		// The stash exists; we just couldn't resolve its SHA. Treat as a soft
		// failure so the caller knows the push happened but labeling can't be
		// keyed reliably.
		return "", fmt.Errorf("stash created but could not resolve its SHA: %w", serr)
	}
	return sha, nil
}

// ErrNothingToStash is returned by Push when the working tree is clean and git
// creates no stash. It is an expected, friendly condition.
var ErrNothingToStash = errors.New("no local changes to stash")

// runMutation runs a mutating git invocation, mapping a not-a-repo stderr to
// ErrNotARepo and otherwise wrapping git's own error text under the supplied
// human label (e.g. "git stash drop stash@{1}"). Stdout is ignored: these
// operations are run for effect.
func runMutation(ctx context.Context, label string, args ...string) error {
	_, stderr, err := runner(ctx, args...)
	if err != nil {
		if isNotARepo(stderr) {
			return ErrNotARepo
		}
		if m := strings.TrimSpace(string(stderr)); m != "" {
			return fmt.Errorf("%s: %s", label, m)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// resolveStashSHA returns the full commit SHA for a stash ref via
// `git rev-parse <ref>`. Used by Push to key sidecar labels by content SHA.
func resolveStashSHA(ctx context.Context, ref string) (string, error) {
	stdout, stderr, err := runner(ctx, "rev-parse", ref)
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return "", fmt.Errorf("git rev-parse %s: %s", ref, msg)
		}
		return "", fmt.Errorf("git rev-parse %s: %w", ref, err)
	}
	sha := strings.TrimSpace(string(stdout))
	if sha == "" {
		return "", fmt.Errorf("git rev-parse %s: empty output", ref)
	}
	return sha, nil
}

// noChangesToStash reports whether `git stash push` declined to create a stash
// because the working tree was clean. git emits this on stdout.
func noChangesToStash(stdout []byte) bool {
	return strings.Contains(strings.ToLower(string(stdout)), "no local changes to save")
}

// EnrichDiffstats computes and attaches a Diffstat and the top changed file to
// each stash in place (M4; TopFile added for auto-labels in issue #7).
//
// It runs one `git stash show --numstat` per stash. A per-stash failure is
// non-fatal: that entry simply keeps a zero Diffstat (and empty TopFile) so a
// single odd stash can't blank out the whole list. An ErrNotARepo from the
// first call is surfaced (the caller is outside a work tree); other errors are
// swallowed per entry. Returns the (same, mutated) slice for convenience.
func EnrichDiffstats(ctx context.Context, stashes []model.Stash) []model.Stash {
	for i := range stashes {
		ds, top, err := diffstatAndTopFile(ctx, stashes[i].Ref())
		if err != nil {
			continue
		}
		stashes[i].Diffstat = ds
		stashes[i].TopFile = top
	}
	return stashes
}
