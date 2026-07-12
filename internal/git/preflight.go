package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PreflightResult reports the outcome of a non-mutating conflict pre-flight for
// applying a stash onto the current working state (issue #23; PLAN.md §8 #5).
//
// It answers three distinct questions the caller needs to make a safe choice:
//
//   - Conflicts: git predicts at least one conflicting path if the stash were
//     applied now. Files lists those paths (may be empty if git couldn't name
//     them, but Conflicts is still authoritative).
//   - Undetermined: the probe could not decide cleanliness (old git, an odd
//     stash shape, a probe failure). Per the issue's "fail open" requirement the
//     caller must NOT block on this — treat it as "couldn't pre-check" and let
//     the normal apply/pop proceed with a clear note. Reason carries a short
//     human explanation for the note.
//
// A zero PreflightResult (Conflicts=false, Undetermined=false) means "predicted
// clean": apply/pop should behave exactly as before, with no extra confirm.
type PreflightResult struct {
	Conflicts    bool
	Undetermined bool
	Files        []string
	Reason       string
}

// Clean reports whether the stash is predicted to apply without conflicts. It
// is only meaningful when the probe was conclusive; an Undetermined result is
// never "clean" (callers fail open by proceeding, but should still surface the
// couldn't-pre-check note rather than claiming a clean apply).
func (r PreflightResult) Clean() bool { return !r.Conflicts && !r.Undetermined }

// WouldConflict predicts, WITHOUT mutating the working tree, index, or stash,
// whether applying the stash at ref (e.g. "stash@{0}") would conflict with the
// current HEAD. It returns the conflicting paths when git can name them.
//
// Mechanism: a stash commit records the state it was created from as its first
// parent (its "base"). A read-only three-way test-merge of the stash tree onto
// HEAD, using that base as the merge base, exactly models what `git stash
// apply` would do to the tree — but `git merge-tree --write-tree` only writes
// objects into the object database and prints the result; it never touches the
// working tree, index, or the stash stack. That satisfies the issue's hard
// "never mutate" and "fail open" requirements.
//
// Failure policy (fail OPEN): any inability to run the probe conclusively
// yields Undetermined=true (never an error and never Conflicts=true), so a
// missing/old git or an unexpected stash shape can never wrongly block a user
// from applying their own work. The only hard error returned is ErrNotARepo,
// so callers outside a work tree can report it cleanly.
func WouldConflict(ctx context.Context, ref string) (PreflightResult, error) {
	// Resolve the stash's base commit (first parent). Without it we cannot do a
	// meaningful three-way merge, so we fail open.
	base, ok, err := revParseQuiet(ctx, ref+"^")
	if err != nil {
		return PreflightResult{}, err
	}
	if !ok || base == "" {
		return undetermined("stash has no base commit to compare against"), nil
	}

	// The stash tree itself.
	stash, ok, err := revParseQuiet(ctx, ref)
	if err != nil {
		return PreflightResult{}, err
	}
	if !ok || stash == "" {
		return undetermined("could not resolve the stash commit"), nil
	}

	// HEAD is what `git stash apply` merges the stash onto.
	head, ok, err := revParseQuiet(ctx, "HEAD")
	if err != nil {
		return PreflightResult{}, err
	}
	if !ok || head == "" {
		return undetermined("could not resolve HEAD to compare against"), nil
	}

	// Read-only test-merge. --write-tree writes only to the object DB; the
	// working tree, index, and stash stack are untouched. --name-only makes the
	// conflict section a bare list of paths. --no-messages keeps stdout to just
	// the tree line + conflicted paths.
	stdout, stderr, rerr := runner(ctx,
		"merge-tree", "--write-tree", "--name-only",
		"--merge-base="+base, head, stash,
	)
	if isNotARepo(stderr) {
		return PreflightResult{}, ErrNotARepo
	}

	// Exit status semantics of `git merge-tree --write-tree`:
	//   0 -> clean merge
	//   1 -> conflicts (a normal, expected outcome, not a probe failure)
	//  >1 -> the probe itself failed (e.g. unknown flag on old git) -> fail open
	if rerr == nil {
		return PreflightResult{}, nil // predicted clean
	}
	if code, okc := exitCode(rerr); !okc || code != 1 {
		reason := "conflict pre-check unavailable"
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			reason = "conflict pre-check unavailable: " + firstLine(msg)
		}
		return undetermined(reason), nil
	}

	// Conflicts predicted. Parse the conflicting paths from stdout.
	return PreflightResult{
		Conflicts: true,
		Files:     parseMergeTreeConflicts(stdout),
	}, nil
}

// undetermined is a small constructor for the fail-open result.
func undetermined(reason string) PreflightResult {
	return PreflightResult{Undetermined: true, Reason: reason}
}

// revParseQuiet resolves a rev to a full object id without failing the caller
// on a "bad revision" style error: it returns ok=false so WouldConflict can
// fail open. ErrNotARepo is still surfaced (via the returned error) so callers
// outside a work tree get the clean signal.
func revParseQuiet(ctx context.Context, rev string) (string, bool, error) {
	stdout, stderr, err := runner(ctx, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		if isNotARepo(stderr) {
			return "", false, ErrNotARepo
		}
		// --quiet makes a bad/unknown rev exit non-zero with empty stdout; treat
		// as "couldn't resolve" rather than a hard error.
		return "", false, nil
	}
	sha := strings.TrimSpace(string(stdout))
	if sha == "" {
		return "", false, nil
	}
	return sha, true, nil
}

// parseMergeTreeConflicts extracts conflicting paths from
// `git merge-tree --write-tree --name-only` output.
//
// The output shape (on conflict) is:
//
//	<merged-tree-oid>\n
//	<conflicted-path>\n   (zero or more)
//	...
//	\n                    (blank separator)
//	<informational messages>  ("Auto-merging …", "CONFLICT …")
//
// The first line is the written tree OID; every subsequent line UP TO the first
// blank line is a conflicting path. The blank line separates the machine-
// readable file list from git's human "Auto-merging/CONFLICT" narration, which
// must NOT be mistaken for file names. Paths are de-duplicated while preserving
// first-seen order.
func parseMergeTreeConflicts(out []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	first := true
	seen := make(map[string]struct{})
	var files []string
	for sc.Scan() {
		line := sc.Text()
		if first {
			// Drop the leading tree-oid line.
			first = false
			continue
		}
		// The blank line ends the conflicted-path section; everything after it is
		// git's informational message block, not file names.
		if strings.TrimSpace(line) == "" {
			break
		}
		path := strings.TrimSpace(line)
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}
	return files
}

// firstLine returns the first line of s, trimmed — used to keep an
// Undetermined Reason short and single-line for a toast/stderr note.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// PreflightNote renders a short, human, single-line summary of a pre-flight
// result suitable for a non-interactive stderr note or a TUI toast. It returns
// "" for a predicted-clean result (nothing worth saying).
func PreflightNote(r PreflightResult) string {
	switch {
	case r.Undetermined:
		if r.Reason != "" {
			return "couldn't pre-check for conflicts (" + r.Reason + "); proceeding"
		}
		return "couldn't pre-check for conflicts; proceeding"
	case r.Conflicts:
		n := len(r.Files)
		if n == 0 {
			return "heads up: this stash is predicted to conflict"
		}
		return fmt.Sprintf("heads up: predicted to conflict in %d file%s: %s",
			n, plural(n), strings.Join(r.Files, ", "))
	default:
		return ""
	}
}

// coder is satisfied by *exec.ExitError (and by test stubs) so exitCode can
// read a process exit status without a real subprocess.
type coder interface{ ExitCode() int }

// exitCode extracts a process exit status from an error returned by the runner.
// It reports ok=false when the error carries no exit status (so callers treat
// it as an indeterminate probe failure and fail open).
func exitCode(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	var c coder
	if errors.As(err, &c) {
		return c.ExitCode(), true
	}
	return 0, false
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
