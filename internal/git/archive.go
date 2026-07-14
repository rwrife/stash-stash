// Compost / archive support (issue #27): retire a stash without losing it.
//
// Dropping a stale stash is destructive and scary — recoverable only via the
// reflog until git garbage-collects it. Composting is the gentle alternative:
// the stash commit is pinned under a durable ref namespace,
// `refs/stash-stash/archive/<sha>`, and then removed from the active stash
// stack. Because a real ref keeps the commit reachable, the work is 100%
// recoverable indefinitely (git will never gc it), yet it no longer clutters
// `git stash list`.
//
// The lifecycle mirrors doctor's "never silently destroy work" rule:
//
//   - Archive(ref):   pin refs/stash-stash/archive/<sha> at the stash commit,
//     then `git stash drop` the entry. The pin happens first so a failed drop
//     can never lose the commit.
//   - ArchivedStashes: enumerate the archive namespace for `compost --list`.
//   - Restore(sha):   re-attach the commit to the stack via `git stash store`
//     and delete the archive ref, bringing it back as a live stash.
//
// Sidecar metadata (label/tags/notes) is keyed by content SHA and so follows
// the stash automatically into and out of the archive — no metadata move is
// needed here.

package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// archiveRefPrefix is the ref namespace stash-stash pins composted stashes
// under. Each archived stash lives at archiveRefPrefix + <full-sha>, so the
// content SHA is both the ref name and the recovery handle.
const archiveRefPrefix = "refs/stash-stash/archive/"

// ArchivedStash is a stash that has been composted: pinned under
// refs/stash-stash/archive/<sha> and removed from `git stash list`. It carries
// enough to list and triage: the commit SHA (the recovery handle), its
// stash-style subject and origin branch, when the underlying work was created,
// and when it was archived.
type ArchivedStash struct {
	// SHA is the full commit SHA of the archived stash commit. It is both the
	// archive ref's leaf name and the handle Restore takes.
	SHA string

	// Subject is the stash commit's subject, e.g. "WIP on main: a1b2c3d …".
	Subject string

	// Branch is the origin branch parsed from Subject (empty if unknown).
	Branch string

	// Created is the stash commit's author time (when the work happened).
	Created time.Time

	// ArchivedAt is the archive ref's own commit/creation time — approximately
	// when the stash was composted. It falls back to Created's zero value when
	// unavailable.
	ArchivedAt time.Time
}

// Ref returns the commit SHA as the handle for read-only inspection (e.g. Show)
// and Restore. Like a dangling stash, an archived stash has no stash@{N} index —
// the SHA is the reference.
func (a ArchivedStash) Ref() string { return a.SHA }

// ArchiveRef returns the full ref path this archived stash is pinned under.
func (a ArchivedStash) ArchiveRef() string { return archiveRefPrefix + a.SHA }

// ErrEmptyArchiveSHA is returned when Archive/Restore are handed a stash that
// resolves to no commit SHA — a defensive guard so we never create a malformed
// ref or store an empty handle.
var ErrEmptyArchiveSHA = errors.New("archive: empty stash commit SHA")

// ErrAlreadyArchived is returned by Archive when the target stash's commit is
// already pinned in the archive namespace, so callers can report it as a
// friendly no-op rather than a failure.
var ErrAlreadyArchived = errors.New("archive: stash is already composted")

// ErrNotArchived is returned by Restore when no archive ref exists for the
// given SHA, so callers can tell "unknown handle" apart from a git failure.
var ErrNotArchived = errors.New("archive: no composted stash with that id")

// Archive composts the stash at ref: it pins the stash's commit under
// refs/stash-stash/archive/<sha> and then drops the entry from the stash stack.
// It returns the archived commit SHA so callers can report and re-key sidecar
// metadata if they wish (metadata is SHA-keyed and follows automatically).
//
// Ordering matters for safety: the durable ref is created *first*, so if the
// subsequent `git stash drop` fails the commit is still pinned and nothing is
// lost. If the pin itself fails, no drop is attempted.
//
// It returns ErrNotARepo outside a work tree, ErrAlreadyArchived when the
// commit is already composted, and surfaces git's own error text otherwise.
func Archive(ctx context.Context, ref string) (string, error) {
	sha, err := resolveStashSHA(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrNotARepo) {
			return "", ErrNotARepo
		}
		return "", err
	}
	if sha == "" {
		return "", ErrEmptyArchiveSHA
	}

	archiveRef := archiveRefPrefix + sha

	// Already pinned? Treat as a friendly no-op signal rather than clobbering.
	if exists, eerr := refExists(ctx, archiveRef); eerr == nil && exists {
		return sha, ErrAlreadyArchived
	}

	// 1) Pin the commit under the archive namespace FIRST (durable, gc-proof).
	if err := runMutation(ctx, fmt.Sprintf("git update-ref %s", archiveRef),
		"update-ref", archiveRef, sha); err != nil {
		return "", err
	}

	// 2) Now drop it from the active stack. If this fails, the commit is still
	// safely pinned above, so no work is lost — surface the error and leave the
	// pin in place (a later compost of the same stash is an idempotent no-op).
	if err := runMutation(ctx, fmt.Sprintf("git stash drop %s", ref),
		"stash", "drop", ref); err != nil {
		return "", fmt.Errorf("pinned %s but could not drop it from the stack: %w", archiveRef, err)
	}
	return sha, nil
}

// ArchivedStashes enumerates the compost archive (`compost --list`): every ref
// under refs/stash-stash/archive/, resolved to its subject, branch, and dates,
// sorted newest-archived first. It returns ErrNotARepo outside a work tree; an
// empty archive yields an empty slice and nil error.
func ArchivedStashes(ctx context.Context) ([]ArchivedStash, error) {
	// %(refname) full ref, %(objectname) commit SHA, %(creatordate:unix) the
	// archived-at time, joined by fieldSep. for-each-ref over a missing
	// namespace simply prints nothing (exit 0).
	format := "%(objectname)" + fieldSep + "%(creatordate:unix)"
	stdout, stderr, err := runner(ctx, "for-each-ref",
		"--format="+format, "refs/stash-stash/archive")
	if err != nil {
		if isNotARepo(stderr) {
			return nil, ErrNotARepo
		}
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("git for-each-ref: %s", msg)
		}
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}

	var shas []string
	archivedAt := map[string]time.Time{}
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 2)
		if len(parts) < 1 {
			continue
		}
		sha := strings.TrimSpace(parts[0])
		if sha == "" {
			continue
		}
		shas = append(shas, sha)
		if len(parts) == 2 {
			archivedAt[sha] = parseUnix(parts[1])
		}
	}
	if len(shas) == 0 {
		return nil, nil
	}

	// Describe each pinned commit (subject + author time) in one batched log.
	described := describeArchivedCommits(ctx, shas)
	for i := range described {
		described[i].ArchivedAt = archivedAt[described[i].SHA]
	}

	// Newest archived first; ties broken by created time, then SHA, for a
	// stable, testable order.
	sort.Slice(described, func(i, j int) bool {
		ai, aj := described[i].ArchivedAt, described[j].ArchivedAt
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		if !described[i].Created.Equal(described[j].Created) {
			return described[i].Created.After(described[j].Created)
		}
		return described[i].SHA < described[j].SHA
	})
	return described, nil
}

// describeArchivedCommits resolves a batch of commit SHAs to ArchivedStash
// entries (subject, branch, created time) in a single `git log --no-walk`.
// Unlike doctor's variant it does not require a stash-shaped subject: an
// archive ref was created deliberately by us, so we trust it and surface
// whatever subject the commit carries (falling back gracefully).
func describeArchivedCommits(ctx context.Context, shas []string) []ArchivedStash {
	if len(shas) == 0 {
		return nil
	}
	format := "%H" + fieldSep + "%at" + fieldSep + "%s"
	args := append([]string{"log", "--no-walk=unsorted", "--format=" + format}, shas...)
	stdout, _, err := runner(ctx, args...)
	if err != nil {
		return nil
	}
	var out []ArchivedStash
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
		out = append(out, ArchivedStash{
			SHA:     strings.TrimSpace(parts[0]),
			Subject: subject,
			Branch:  branchFromSubject(subject),
			Created: parseUnix(parts[1]),
		})
	}
	return out
}

// Restore brings a composted stash back to life (`compost --restore <sha>`): it
// re-attaches the pinned commit to the stack with `git stash store` and then
// deletes the archive ref, so it reappears in `git stash list` as stash@{0} and
// no longer double-counts in the archive. Sidecar metadata (SHA-keyed) follows
// automatically.
//
// Ordering mirrors Archive's safety: the stash is re-stored FIRST, then the
// archive ref is deleted, so a failure between the two steps leaves the commit
// reachable from at least one of them. sha may be a full SHA or any unique
// prefix that resolves against the archive namespace (see resolveArchivedSHA).
//
// It returns ErrNotARepo outside a work tree, ErrNotArchived when no archive
// ref matches, and surfaces git's own error text otherwise.
func Restore(ctx context.Context, sha string) (string, error) {
	full, err := resolveArchivedSHA(ctx, sha)
	if err != nil {
		return "", err
	}
	archiveRef := archiveRefPrefix + full

	// Preserve the original subject so the restored entry reads sensibly.
	message := ""
	if desc := describeArchivedCommits(ctx, []string{full}); len(desc) == 1 {
		message = desc[0].Subject
	}

	// 1) Re-attach to the stack first (non-destructive; only adds a ref).
	if err := StoreStash(ctx, full, message); err != nil {
		return "", err
	}
	// 2) Now retire the archive ref. If this fails the stash is already back on
	// the stack (the important part); report the leftover ref so a re-run can
	// clean it up (Restore of an already-restored SHA is still safe: store just
	// adds another entry, so we guard against that by deleting best-effort).
	if err := runMutation(ctx, fmt.Sprintf("git update-ref -d %s", archiveRef),
		"update-ref", "-d", archiveRef); err != nil {
		return full, fmt.Errorf("restored %s to the stack but could not delete its archive ref: %w", shortArchiveSHA(full), err)
	}
	return full, nil
}

// resolveArchivedSHA maps a user-supplied id — a full SHA or a unique prefix —
// to the full SHA of a pinned archive ref. It lists the archive namespace and
// matches by exact SHA or unambiguous prefix. It returns ErrNotArchived when
// nothing matches and a clear error when a prefix is ambiguous.
func resolveArchivedSHA(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrEmptyArchiveSHA
	}
	archived, err := ArchivedStashes(ctx)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, a := range archived {
		if a.SHA == id {
			return a.SHA, nil // exact wins immediately
		}
		if strings.HasPrefix(a.SHA, id) {
			matches = append(matches, a.SHA)
		}
	}
	switch len(matches) {
	case 0:
		return "", ErrNotArchived
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("archive: id %q is ambiguous (%d matches); use more characters", id, len(matches))
	}
}

// refExists reports whether a full ref path currently resolves, via
// `git rev-parse --verify --quiet <ref>`. A non-zero exit (ref absent) is not
// an error here — it is the "does not exist" answer.
func refExists(ctx context.Context, ref string) (bool, error) {
	stdout, _, err := runner(ctx, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		// --quiet makes a missing ref exit non-zero with empty output; treat
		// that as "absent", not a hard error.
		return false, nil
	}
	return strings.TrimSpace(string(stdout)) != "", nil
}

// shortArchiveSHA abbreviates a SHA to 12 chars for archive error messages,
// mirroring the command package's shortSHA so output stays consistent.
func shortArchiveSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
