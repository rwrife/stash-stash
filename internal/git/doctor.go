// Doctor support (issue #10): recover work you thought you lost.
//
// `git stash drop`, a hard reset, or an aborted rebase can leave a stash commit
// *unreachable* — gone from `git stash list` but still in the object database
// until git eventually garbage-collects it. These helpers find those dangling
// stash-like commits (via reflog + `git fsck`), describe them enough to triage,
// and re-attach a chosen one to the stash stack with `git stash store` so the
// work shows up in `git stash list` again.
//
// Everything here is read-only except StoreStash, which only *adds* a ref (it
// never drops or rewrites anything), keeping with stash-stash's "never silently
// destroy work" rule.

package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DanglingStash is a stash-like commit that is no longer reachable from the
// stash stack (`git stash list`) but is still present in the object database,
// so it can be recovered. It carries just enough to triage and restore: the
// commit SHA, the parsed origin branch + subject, when it was created, and how
// it was found (reflog vs. fsck), for a friendly report.
type DanglingStash struct {
	// SHA is the full commit SHA of the dangling stash commit. StoreStash uses
	// it to re-attach the entry to the stash stack.
	SHA string

	// Subject is the stash commit's reflog-style subject, e.g.
	// "WIP on main: a1b2c3d Tidy things" or "On feature/x: half-done modal".
	Subject string

	// Branch is the origin branch parsed from Subject (empty if unknown).
	Branch string

	// Created is the commit's author time, used to show age in the report.
	Created time.Time

	// Source notes how the commit was discovered ("reflog" or "fsck"), purely
	// for the diagnostic report so users understand where the recovery came
	// from.
	Source string
}

// Ref returns the commit SHA as the reference to use with read-only inspection
// (e.g. Show) and StoreStash. Unlike a live stash there is no stash@{N} index —
// the SHA *is* the handle.
func (d DanglingStash) Ref() string { return d.SHA }

// DanglingStashes returns stash-like commits that are recoverable but no longer
// in `git stash list`: the heart of `stash-stash doctor`.
//
// It gathers candidates from two complementary sources and unions them:
//
//   - the stash reflog (`git reflog show --format ... stash`), which still lists
//     entries for stashes dropped in this repo even after they leave the stack;
//   - `git fsck --unreachable --no-reflog`, which surfaces dangling commits the
//     reflog has already expired or that were orphaned by a reset/rebase.
//
// Each candidate is confirmed to be stash-shaped (a "WIP on …"/"On …" subject)
// and filtered against the *live* stash SHAs so entries still on the stack are
// never reported as lost. Results are sorted newest-first for a readable report.
//
// It returns ErrNotARepo outside a work tree. Per-source failures are tolerated
// (e.g. a repo with no stash reflog yet) so one missing source can't blank out
// the other; only a hard not-a-repo condition is surfaced.
func DanglingStashes(ctx context.Context) ([]DanglingStash, error) {
	// The set of stashes still on the stack — we must never report these as
	// "lost". List already returns ErrNotARepo cleanly outside a work tree.
	live, err := List(ctx)
	if err != nil {
		return nil, err
	}
	liveSHAs := make(map[string]struct{}, len(live))
	for i := range live {
		liveSHAs[live[i].SHA] = struct{}{}
	}

	// Collect candidates from both sources, de-duplicated by SHA. The first
	// source to mention a SHA wins its Source label (reflog is checked first as
	// it carries the richer, original subject).
	found := map[string]DanglingStash{}

	for _, d := range danglingFromReflog(ctx) {
		if _, ok := liveSHAs[d.SHA]; ok {
			continue
		}
		if _, seen := found[d.SHA]; !seen {
			found[d.SHA] = d
		}
	}
	for _, d := range danglingFromFsck(ctx) {
		if _, ok := liveSHAs[d.SHA]; ok {
			continue
		}
		if _, seen := found[d.SHA]; !seen {
			found[d.SHA] = d
		}
	}

	out := make([]DanglingStash, 0, len(found))
	for _, d := range found {
		out = append(out, d)
	}
	// Newest first; ties broken by SHA for a stable, testable order.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].SHA < out[j].SHA
	})
	return out, nil
}

// danglingFromReflog reads the stash reflog and returns every entry as a
// candidate DanglingStash. The stash reflog (`refs/stash`) retains entries for
// dropped stashes until the reflog itself expires, so this recovers the common
// "I dropped the wrong stash" case with the original subject intact.
//
// It is best-effort: a repo that has never had a stash has no `refs/stash`
// reflog and git exits non-zero — we treat that (and any other failure) as "no
// candidates here" rather than an error, leaving fsck to cover the gap.
func danglingFromReflog(ctx context.Context) []DanglingStash {
	// %H sha, %at author unix time, %gs reflog subject — same fields as List,
	// but read from the reflog so dropped entries are still visible.
	format := "%H" + fieldSep + "%at" + fieldSep + "%gs"
	stdout, _, err := runner(ctx, "reflog", "show", "--format="+format, "stash")
	if err != nil {
		return nil
	}
	var out []DanglingStash
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}
		subject := strings.TrimSpace(parts[2])
		if !looksLikeStashSubject(subject) {
			continue
		}
		out = append(out, DanglingStash{
			SHA:     strings.TrimSpace(parts[0]),
			Subject: subject,
			Branch:  branchFromSubject(subject),
			Created: parseUnix(parts[1]),
			Source:  "reflog",
		})
	}
	return out
}

// danglingFromFsck scans `git fsck --unreachable --no-reflog` for dangling
// commits and keeps the ones that are stash-shaped. fsck output is lines like
// "unreachable commit <sha>"; we resolve each commit's subject and author time
// with a single batched `git log --no-walk` so we can confirm it's a stash and
// date it for the report.
//
// `--no-reflog` makes fsck ignore reflog-reachable commits, so this source
// focuses on truly orphaned commits (expired reflog, reset/rebase casualties)
// and complements danglingFromReflog. It is best-effort: any failure yields no
// candidates rather than aborting the whole doctor run.
func danglingFromFsck(ctx context.Context) []DanglingStash {
	stdout, _, err := runner(ctx, "fsck", "--unreachable", "--no-reflog")
	if err != nil {
		return nil
	}

	var shas []string
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// Lines look like: "unreachable commit <sha>". We only want commits.
		if len(fields) == 3 && fields[0] == "unreachable" && fields[1] == "commit" {
			shas = append(shas, fields[2])
		}
	}
	if len(shas) == 0 {
		return nil
	}

	return describeStashCommits(ctx, shas)
}

// describeStashCommits resolves a batch of commit SHAs to DanglingStash entries,
// keeping only the stash-shaped ones. It runs a single
// `git log --no-walk=unsorted --format=... <sha>...` so N candidates cost one
// git call rather than N. Commits whose subject isn't stash-shaped (random
// dangling commits from a reset, say) are dropped.
//
// %gs (reflog subject) is empty for a plain commit lookup, so we ask for %s
// (the commit subject) here: a stash commit's own subject is the same
// "WIP on …"/"On …" line, which is exactly what we match on.
func describeStashCommits(ctx context.Context, shas []string) []DanglingStash {
	if len(shas) == 0 {
		return nil
	}
	format := "%H" + fieldSep + "%at" + fieldSep + "%s"
	args := append([]string{"log", "--no-walk=unsorted", "--format=" + format}, shas...)
	stdout, _, err := runner(ctx, args...)
	if err != nil {
		return nil
	}

	var out []DanglingStash
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}
		subject := strings.TrimSpace(parts[2])
		if !looksLikeStashSubject(subject) {
			continue
		}
		out = append(out, DanglingStash{
			SHA:     strings.TrimSpace(parts[0]),
			Subject: subject,
			Branch:  branchFromSubject(subject),
			Created: parseUnix(parts[1]),
			Source:  "fsck",
		})
	}
	return out
}

// looksLikeStashSubject reports whether a commit/reflog subject has the shape
// git gives a stash commit ("WIP on <branch>: …" or "On <branch>: …"). It is
// how we tell a recoverable stash apart from any other dangling commit so the
// doctor never offers to "restore" a random orphaned commit as a stash.
func looksLikeStashSubject(subject string) bool {
	s := strings.TrimSpace(subject)
	return strings.HasPrefix(s, "WIP on ") || strings.HasPrefix(s, "On ")
}

// parseUnix parses a unix-timestamp field into a time.Time, returning the zero
// time on any parse failure (so a malformed date degrades to "unknown age"
// rather than breaking the listing).
func parseUnix(field string) time.Time {
	if ts, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64); err == nil {
		return time.Unix(ts, 0)
	}
	return time.Time{}
}

// StoreStash re-attaches a recovered stash commit to the stash stack via
// `git stash store -m <message> <sha>`, so it shows up in `git stash list`
// again (and becomes stash@{0}). This is the doctor's restore action.
//
// `git stash store` only *creates* a ref; it never applies the stash to the
// working tree and never drops anything, so it's a safe, non-destructive way to
// rescue work. message is the subject the restored entry carries; a blank
// message lets git use its default. It returns ErrNotARepo outside a work tree
// and surfaces git's own error text otherwise (e.g. a bad SHA).
func StoreStash(ctx context.Context, sha, message string) error {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return fmt.Errorf("git stash store: empty commit SHA")
	}
	args := []string{"stash", "store"}
	if m := strings.TrimSpace(message); m != "" {
		args = append(args, "-m", m)
	}
	args = append(args, sha)
	return runMutation(ctx, fmt.Sprintf("git stash store %s", sha), args...)
}
