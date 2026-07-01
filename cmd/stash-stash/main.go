// Command stash-stash is a concierge for your git stash graveyard.
//
// M3: when stdout is a TTY it reads real stashes and opens an interactive
// Bubble Tea browser (scrollable list + diff preview); otherwise it prints the
// plain, non-interactive table from M2 so pipes and CI stay script-friendly.
// M4 enriches each stash with sidecar labels (matched by content SHA) and a
// diffstat, and lets the TUI (re)label a stash with `l`. M5 adds mutating
// actions in the TUI (`a` apply, `p` pop, `d` drop — the latter two gated by a
// confirm) and a `stash-stash push -m "label"` subcommand that stashes the
// working tree and records the label in the sidecar immediately.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/jsonout"
	"github.com/rwrife/stash-stash/internal/meta"
	"github.com/rwrife/stash-stash/internal/model"
	"github.com/rwrife/stash-stash/internal/render"
	"github.com/rwrife/stash-stash/internal/search"
	"github.com/rwrife/stash-stash/internal/tui"
)

// version is the build version. It is overridable at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/stash-stash
var version = "dev"

// stringSlice is a flag.Value that accumulates a repeatable string flag, so
// `--tag wip --tag hotfix` collects both values (AND-filtered downstream). Each
// Set appends; the zero value is an empty slice.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// now is indirected so tests can pin the clock when checking age output.
var now = time.Now

// stdoutIsTTY reports whether standard output is an interactive terminal. It
// is a package var so tests can force the plain-table path. The real check
// uses the actual os.Stdout file descriptor.
var stdoutIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stdinIsTTY reports whether standard input is an interactive terminal. The
// doctor command uses it to decide whether it can safely prompt for
// restore/delete choices; when stdin is a pipe or redirected file it falls back
// to a read-only report instead. It is a package var so tests can force either
// path without a real TTY.
var stdinIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// noTUI lets `--no-tui` (or a non-TTY stdout) force the plain table even in an
// interactive shell, which is handy for screenshots, logs, and scripting.
var noTUIFlag bool

// defaultStaleDays is the age (in days) at or beyond which a stash is
// considered to be "gathering dust". Overridable via --stale-days.
const defaultStaleDays = 14

// gitPush and metaLoad are indirected so the push subcommand can be unit-tested
// without a real repo or working-tree changes. They default to the real
// implementations. gitList and gitShow are indirected likewise for the search
// subcommand (which lists stashes and reads each one's patch).
var (
	gitPush  = git.Push
	metaLoad = meta.Load
	gitList  = git.List
	gitShow  = git.Show

	// Doctor (issue #10) indirections, so runDoctor can be unit-tested without a
	// real repo, dangling commits, or sidecar file.
	gitDangling   = git.DanglingStashes
	gitStoreStash = git.StoreStash
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entrypoint. It returns the process exit code so that
// tests can exercise flag handling without calling os.Exit.
func run(args []string, stdout, stderr io.Writer) int {
	// Subcommand dispatch happens before flag parsing so subcommands own their
	// own flags. `push` records a labeled stash; `search` greps stash contents;
	// everything else is the default list/browse behavior (optionally with
	// top-level flags).
	if len(args) > 0 && args[0] == "push" {
		return runPush(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "search" {
		return runSearch(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctor(args[1:], os.Stdin, stdout, stderr)
	}

	fs := flag.NewFlagSet("stash-stash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash %s — a concierge for your git stash graveyard.\n\n", version)
		fmt.Fprintf(stderr, "Usage:\n  stash-stash [flags]\n  stash-stash push [-m \"label\"]\n  stash-stash search <term> [--regex]\n  stash-stash doctor [--dry-run]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(&noTUIFlag, "no-tui", false, "force plain table output even on a TTY")
	staleDays := fs.Int("stale-days", defaultStaleDays, "flag stashes older than this many days as gathering dust (0 disables)")
	jsonOut := fs.Bool("json", false, "print the stash list as JSON for scripting (implies --no-tui)")
	var tagFilter stringSlice
	fs.Var(&tagFilter, "tag", "only show stashes carrying this tag; repeatable for AND (e.g. --tag wip --tag hotfix)")

	if err := fs.Parse(args); err != nil {
		// flag already printed the error + usage; -h/--help returns ErrHelp.
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "stash-stash %s\n", version)
		return 0
	}

	if *staleDays < 0 {
		fmt.Fprintln(stderr, "stash-stash: --stale-days must be >= 0 (0 disables the staleness nag).")
		return 2
	}

	return listOrBrowse(stdout, stderr, *staleDays, *jsonOut, tagFilter)
}

// listOrBrowse loads the current repo's stashes, enriches them with sidecar
// labels (matched by content SHA), tags, and diffstats, and either emits JSON
// (--json), opens the interactive TUI (TTY stdout, not --no-tui/--json), or
// prints the plain table. staleDays drives the "gathering dust" nag and the
// stale highlighting/markers (0 disables). tagFilter (repeatable --tag) narrows
// the plain table and JSON to stashes carrying *all* the given tags (AND) and
// pre-seeds the TUI's live tag filter. It returns the process exit code: 0
// on success (including the no-stash case), 1 on a real error talking to git,
// 1 on a TUI runtime failure.
func listOrBrowse(stdout, stderr io.Writer, staleDays int, jsonOut bool, tagFilter []string) int {
	ctx := context.Background()

	// Normalize the requested tags once so filtering matches the slugified form
	// stored on each stash ("Hot Fix" --tag and a "hot-fix" stored tag agree).
	wantTags := meta.NormalizeTags(tagFilter)

	stashes, err := gitList(ctx)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		}
		fmt.Fprintf(stderr, "stash-stash: %v\n", err)
		return 1
	}

	if len(stashes) == 0 {
		// Still clean up sidecar entries for stashes that were dropped/popped
		// outside stash-stash (loadAndApplyMeta prunes against the live set,
		// which is empty here) so the metadata file doesn't accumulate cruft.
		loadAndApplyMeta(ctx, stashes, stderr)
		if jsonOut {
			if err := jsonout.Write(stdout, stashes, now(), staleDays); err != nil {
				fmt.Fprintf(stderr, "stash-stash: encoding json: %v\n", err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(stdout, "No stashes found. Your graveyard is empty — nice. 🪦")
		return 0
	}

	// Enrich with sidecar labels + tags + diffstats. Metadata is best-effort: a
	// sidecar problem must never stop us listing stashes, so a load error is
	// reported to stderr but we continue label-less.
	store := loadAndApplyMeta(ctx, stashes, stderr)
	git.EnrichDiffstats(ctx, stashes)

	// Apply the --tag filter (AND across tags). Filtering happens after
	// enrichment so each stash's Tags are populated; the TUI path filters
	// interactively instead (it is seeded with wantTags below), so we only
	// narrow the slice here for the non-interactive table/JSON outputs.
	interactive := !noTUIFlag && !jsonOut && stdout == os.Stdout && stdoutIsTTY()
	if len(wantTags) > 0 && !interactive {
		stashes = filterByTags(stashes, wantTags)
	}

	// JSON output is for scripting: deterministic, never interactive, and
	// independent of TTY detection.
	if jsonOut {
		if err := jsonout.Write(stdout, stashes, now(), staleDays); err != nil {
			fmt.Fprintf(stderr, "stash-stash: encoding json: %v\n", err)
			return 1
		}
		return 0
	}

	// Interactive path: only when we have a real terminal and the user didn't
	// opt out. Everything else (pipes, CI, --no-tui) gets the plain table.
	if interactive {
		// Wire the mutating actions. Reload re-lists and re-enriches so the TUI
		// can resync after a pop/drop (indices shift). It is best-effort: a
		// reload error is surfaced in-TUI, not fatal.
		actions := tui.Actions{
			Apply:  git.Apply,
			Pop:    git.Pop,
			Drop:   git.Drop,
			Branch: git.Branch,
			Reload: func(ctx context.Context) ([]model.Stash, error) {
				fresh, err := gitList(ctx)
				if err != nil {
					return nil, err
				}
				if store != nil {
					for i := range fresh {
						if label, ok := store.Label(fresh[i].SHA); ok {
							fresh[i].Label = label
						}
						if tags := store.Tags(fresh[i].SHA); len(tags) > 0 {
							fresh[i].Tags = tags
						}
					}
				}
				git.EnrichDiffstats(ctx, fresh)
				return fresh, nil
			},
		}
		if err := tui.Run(stashes, git.Show, store, actions, now(), staleDays, wantTags, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(stderr, "stash-stash: tui: %v\n", err)
			return 1
		}
		return 0
	}

	if err := render.Table(stdout, stashes, now(), staleDays); err != nil {
		fmt.Fprintf(stderr, "stash-stash: rendering table: %v\n", err)
		return 1
	}
	return 0
}

// loadAndApplyMeta loads the sidecar store, applies any stored labels onto the
// stashes by content SHA, and prunes metadata for stashes that no longer
// exist. It returns the store (possibly with pruned entries) so the TUI can
// persist relabels; on any load error it reports to stderr and returns nil,
// leaving the stashes label-less but fully usable.
func loadAndApplyMeta(ctx context.Context, stashes []model.Stash, stderr io.Writer) *meta.Store {
	store, err := metaLoad(ctx)
	if err != nil {
		// ErrNotARepo "can't happen" here (git.List already succeeded), but if
		// the sidecar is unreadable, degrade gracefully rather than fail.
		fmt.Fprintf(stderr, "stash-stash: sidecar metadata unavailable (%v); continuing without labels.\n", err)
		return nil
	}

	live := make(map[string]struct{}, len(stashes))
	for i := range stashes {
		live[stashes[i].SHA] = struct{}{}
		if label, ok := store.Label(stashes[i].SHA); ok {
			stashes[i].Label = label
		}
		if tags := store.Tags(stashes[i].SHA); len(tags) > 0 {
			stashes[i].Tags = tags
		}
	}

	// Garbage-collect labels for stashes dropped/popped outside stash-stash.
	// Persist only if something actually changed, to avoid needless writes.
	if n := store.Prune(live); n > 0 {
		if err := store.Save(); err != nil {
			fmt.Fprintf(stderr, "stash-stash: could not prune stale sidecar entries (%v).\n", err)
		}
	}
	return store
}

// filterByTags returns the subset of stashes carrying every tag in wantTags
// (AND semantics). wantTags must already be normalized (slugified). An empty
// wantTags returns the input unchanged. The result preserves stash order and
// never shares backing storage that would surprise the caller.
func filterByTags(stashes []model.Stash, wantTags []string) []model.Stash {
	if len(wantTags) == 0 {
		return stashes
	}
	out := make([]model.Stash, 0, len(stashes))
	for i := range stashes {
		if stashes[i].HasAllTags(wantTags) {
			out = append(out, stashes[i])
		}
	}
	return out
}

// runPush implements `stash-stash push [-m "label"]`: a thin wrapper around
// `git stash push` that records the label in the sidecar (keyed by the new
// stash's content SHA) the instant the stash exists, so a freshly-stashed
// change is named from the start instead of relying on git's "WIP on …"
// subject.
//
// It returns the process exit code: 0 on success (including a clean tree with
// nothing to stash, which is reported but not an error), 2 on bad flags, and
// 1 on a git or sidecar failure.
func runPush(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash push", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash push — stash your changes with a label that sticks.\n\n")
		fmt.Fprintf(stderr, "Usage:\n  stash-stash push [-m \"label\"]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	msg := fs.String("m", "", "label to record for the new stash (also used as the git stash message)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "stash-stash push: unexpected argument %q (did you forget to quote the label after -m?)\n", fs.Arg(0))
		return 2
	}

	ctx := context.Background()

	sha, err := gitPush(ctx, *msg)
	if err != nil {
		if errors.Is(err, git.ErrNothingToStash) {
			fmt.Fprintln(stdout, "Nothing to stash — working tree is clean. ✨")
			return 0
		}
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		}
		fmt.Fprintf(stderr, "stash-stash: %v\n", err)
		return 1
	}

	label := strings.TrimSpace(*msg)
	if label == "" {
		// No label requested: the stash exists (git picked a default subject),
		// nothing to record in the sidecar.
		fmt.Fprintln(stdout, "Stashed. (No label given — run with -m to name it.)")
		return 0
	}

	// Record the label against the new stash's content SHA. A sidecar failure
	// here is non-fatal to the stash itself (the work is safely stashed), but
	// we report it and exit non-zero so the user knows the label didn't stick.
	store, lerr := metaLoad(ctx)
	if lerr != nil {
		fmt.Fprintf(stderr, "stash-stash: stashed, but could not open sidecar to record the label (%v).\n", lerr)
		return 1
	}
	store.SetLabel(sha, label)
	if serr := store.Save(); serr != nil {
		fmt.Fprintf(stderr, "stash-stash: stashed, but could not save the label (%v).\n", serr)
		return 1
	}

	fmt.Fprintf(stdout, "Stashed and labeled %q. 🏷️\n", label)
	return 0
}

// runSearch implements `stash-stash search <term> [--regex]`: it greps across
// every stash's *contents* (the unified diff `git stash show -p` would apply)
// so you can find "which stash had that retry change?" without applying each
// one. Matching is a case-insensitive substring by default; --regex switches to
// a (still case-insensitive) regular expression. Each stash with a hit is
// printed with its label, age, and branch, followed by the matching line
// snippets (file:line and the ± diff text).
//
// It returns the process exit code: 0 when the search ran (including the
// no-matches case, which is reported but not an error), 2 on bad flags or a
// missing/empty term (or an un-compilable --regex pattern), and 1 on a git or
// repo failure.
func runSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash search \u2014 grep across every stash's contents at once.\n\n")
		fmt.Fprintf(stderr, "Usage:\n  stash-stash search <term> [--regex]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	useRegex := fs.Bool("regex", false, "treat <term> as a regular expression (still case-insensitive by default)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	// Join the remaining args so an unquoted multi-word term still works
	// (`search foo bar` searches for "foo bar"); an empty term is a usage error.
	term := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if term == "" {
		fmt.Fprintln(stderr, "stash-stash search: missing search term.\n\nUsage:\n  stash-stash search <term> [--regex]")
		return 2
	}

	// Build the matcher. A bad regex is a user error (exit 2), reported with
	// regexp's own message so it's actionable.
	var matcher search.Matcher
	if *useRegex {
		rm, err := search.Regexp(term)
		if err != nil {
			fmt.Fprintf(stderr, "stash-stash search: invalid --regex pattern: %v\n", err)
			return 2
		}
		matcher = rm
	} else {
		matcher = search.Literal(term)
	}

	ctx := context.Background()

	stashes, err := gitList(ctx)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		}
		fmt.Fprintf(stderr, "stash-stash: %v\n", err)
		return 1
	}

	if len(stashes) == 0 {
		fmt.Fprintln(stdout, "No stashes found. Your graveyard is empty \u2014 nothing to search. \U0001FAA6")
		return 0
	}

	// Enrich with sidecar labels so each hit shows a human name in its header.
	// Metadata is best-effort: a sidecar problem must never stop a search, so a
	// load error is reported to stderr and we continue with raw subjects.
	loadAndApplyMeta(ctx, stashes, stderr)

	// Scan each stash's patch. A per-stash Show failure is non-fatal: that stash
	// is skipped (with a stderr note) rather than aborting the whole search, so
	// one odd entry can't hide matches in the others.
	var hits []render.SearchHit
	for i := range stashes {
		patch, serr := gitShow(ctx, stashes[i].Ref())
		if serr != nil {
			if errors.Is(serr, git.ErrNotARepo) {
				// The repo vanished mid-search; surface it as a real failure.
				fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
				return 1
			}
			fmt.Fprintf(stderr, "stash-stash: skipping %s (%v).\n", stashes[i].Ref(), serr)
			continue
		}
		if ms := search.Scan(patch, matcher); len(ms) > 0 {
			hits = append(hits, render.SearchHit{Stash: stashes[i], Matches: ms})
		}
	}

	if _, err := render.SearchResults(stdout, hits, term, now()); err != nil {
		fmt.Fprintf(stderr, "stash-stash: rendering search results: %v\n", err)
		return 1
	}
	return 0
}

// runDoctor implements `stash-stash doctor`: find work you thought you lost
// (issue #10). It scans the reflog and `git fsck` for stash-like commits that
// have fallen off `git stash list` but are still recoverable, and reports
// sidecar entries whose stash is gone for good (orphaned metadata).
//
// Interactivity: when stdin is a TTY and --dry-run is not set, it walks each
// finding and prompts — restore/diff/skip for a dangling stash, delete/skip for
// an orphaned label. Restoring re-attaches the commit to the stack with
// `git stash store` (non-destructive: it only adds a ref). Without a TTY (pipe,
// CI) or with --dry-run, it prints the same report read-only and changes
// nothing, so it stays script-safe.
//
// It returns the process exit code: 0 on a clean bill of health or a completed
// triage, 2 on bad flags, and 1 on a git/sidecar failure.
func runDoctor(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash doctor \u2014 recover work you thought you lost.\n\n")
		fmt.Fprintf(stderr, "Scans the reflog and git fsck for dangling stash commits no longer in\n")
		fmt.Fprintf(stderr, "`git stash list`, offers to restore them, and reports orphaned sidecar\n")
		fmt.Fprintf(stderr, "labels whose stash is gone.\n\n")
		fmt.Fprintf(stderr, "Usage:\n  stash-stash doctor [--dry-run]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	dryRun := fs.Bool("dry-run", false, "report findings without prompting or changing anything")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "stash-stash doctor: unexpected argument %q.\n", fs.Arg(0))
		return 2
	}

	ctx := context.Background()

	// 1. Recoverable dangling stashes (reflog + fsck), excluding live ones.
	dangling, err := gitDangling(ctx)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		}
		fmt.Fprintf(stderr, "stash-stash: %v\n", err)
		return 1
	}

	// 2. Orphaned sidecar metadata: labels whose SHA is neither a live stash nor
	// a recoverable dangling one. Best-effort — a sidecar problem must not stop
	// the recovery report, so we note it and continue with no orphans.
	var orphans []meta.Orphan
	store, lerr := metaLoad(ctx)
	if lerr != nil {
		fmt.Fprintf(stderr, "stash-stash: sidecar metadata unavailable (%v); skipping orphan check.\n", lerr)
	} else {
		known := knownSHAs(ctx, dangling)
		orphans = store.Orphans(known)
	}

	// Nothing wrong? Say so and leave.
	if len(dangling) == 0 && len(orphans) == 0 {
		fmt.Fprintln(stdout, "\U0001FA7A Clean bill of health: no lost stashes and no orphaned labels. \u2728")
		return 0
	}

	// Decide whether we can prompt. Without a real terminal (or with --dry-run)
	// we print a read-only report and change nothing.
	interactive := !*dryRun && stdin == os.Stdin && stdinIsTTY()

	fmt.Fprintf(stdout, "\U0001FA7A stash-stash doctor\n\n")

	if !interactive {
		return doctorReport(stdout, dangling, orphans, *dryRun)
	}
	return doctorTriage(ctx, bufio.NewReader(stdin), stdout, stderr, store, dangling, orphans)
}

// knownSHAs returns the set of stash content SHAs that are accounted for: the
// live stack plus every recoverable dangling commit. A sidecar entry keyed by
// any of these is *not* an orphan (the work still exists somewhere), so the
// doctor must not offer to delete its label. A List failure here degrades to
// "only the dangling set is known", which at worst over-reports an orphan we
// then let the user skip — never deletes a live stash's label silently.
func knownSHAs(ctx context.Context, dangling []git.DanglingStash) map[string]struct{} {
	known := make(map[string]struct{})
	if live, err := gitList(ctx); err == nil {
		for i := range live {
			known[live[i].SHA] = struct{}{}
		}
	}
	for i := range dangling {
		known[dangling[i].SHA] = struct{}{}
	}
	return known
}

// doctorReport prints the read-only findings (non-TTY or --dry-run path) and
// returns exit 0. It mirrors the interactive view's wording so the two modes
// read the same, and ends with a hint about how to act on the findings.
func doctorReport(stdout io.Writer, dangling []git.DanglingStash, orphans []meta.Orphan, dryRun bool) int {
	if len(dangling) > 0 {
		fmt.Fprintf(stdout, "Recoverable stashes (not in `git stash list`): %d\n", len(dangling))
		for _, d := range dangling {
			fmt.Fprintf(stdout, "  %s\n", doctorDanglingLine(d))
		}
		fmt.Fprintln(stdout)
	}
	if len(orphans) > 0 {
		fmt.Fprintf(stdout, "Orphaned sidecar labels (stash is gone): %d\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintf(stdout, "  %s\n", doctorOrphanLine(o))
		}
		fmt.Fprintln(stdout)
	}
	if dryRun {
		fmt.Fprintln(stdout, "Dry run \u2014 nothing changed. Re-run `stash-stash doctor` in a terminal to restore or clean up.")
	} else {
		fmt.Fprintln(stdout, "Run `stash-stash doctor` in an interactive terminal to restore stashes or remove orphaned labels.")
	}
	return 0
}

// doctorTriage walks the findings interactively, prompting per item. Restores
// go through git.StoreStash (add-only); orphan deletions mutate the sidecar in
// memory and are persisted once at the end (a single Save) so a long triage
// doesn't rewrite the file repeatedly. It returns exit 0 once the user has
// worked through every finding (or quit), 1 only if a sidecar Save fails after
// real changes.
func doctorTriage(ctx context.Context, in *bufio.Reader, stdout, stderr io.Writer, store *meta.Store, dangling []git.DanglingStash, orphans []meta.Orphan) int {
	restored, removed := 0, 0

	if len(dangling) > 0 {
		fmt.Fprintf(stdout, "Found %d recoverable stash(es) not in `git stash list`:\n\n", len(dangling))
	dangleLoop:
		for _, d := range dangling {
			fmt.Fprintf(stdout, "  %s\n", doctorDanglingLine(d))
			for {
				choice := prompt(in, stdout, "    [r]estore to stash list · [d]iff · [s]kip · [q]uit? ")
				switch choice {
				case "r", "restore":
					if err := gitStoreStash(ctx, d.SHA, d.Subject); err != nil {
						if errors.Is(err, git.ErrNotARepo) {
							fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
							return 1
						}
						fmt.Fprintf(stderr, "    could not restore: %v\n", err)
					} else {
						restored++
						fmt.Fprintf(stdout, "    \u2705 restored as stash@{0}.\n")
					}
					continue dangleLoop
				case "d", "diff":
					doctorShowDiff(ctx, stdout, stderr, d.SHA)
					// Loop again so the user can act after seeing the diff.
				case "s", "skip", "":
					continue dangleLoop
				case "q", "quit":
					break dangleLoop
				default:
					fmt.Fprintln(stdout, "    (please answer r, d, s, or q)")
				}
			}
		}
		fmt.Fprintln(stdout)
	}

	if len(orphans) > 0 {
		fmt.Fprintf(stdout, "Found %d orphaned sidecar label(s) whose stash is gone:\n\n", len(orphans))
	orphanLoop:
		for _, o := range orphans {
			fmt.Fprintf(stdout, "  %s\n", doctorOrphanLine(o))
			for {
				choice := prompt(in, stdout, "    [d]elete label · [s]kip · [q]uit? ")
				switch choice {
				case "d", "delete":
					if store.Remove(o.SHA) {
						removed++
						fmt.Fprintf(stdout, "    \U0001F5D1\uFE0F  removed.\n")
					}
					continue orphanLoop
				case "s", "skip", "":
					continue orphanLoop
				case "q", "quit":
					break orphanLoop
				default:
					fmt.Fprintln(stdout, "    (please answer d, s, or q)")
				}
			}
		}
		fmt.Fprintln(stdout)
	}

	// Persist sidecar changes once, only if we actually removed something.
	if removed > 0 {
		if err := store.Save(); err != nil {
			fmt.Fprintf(stderr, "stash-stash: removed %d label(s) in memory but could not save the sidecar (%v).\n", removed, err)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Done. Restored %d stash(es), removed %d orphaned label(s).\n", restored, removed)
	return 0
}

// doctorShowDiff prints the patch for a dangling stash commit so the user can
// decide whether to restore it. It reuses gitShow (`git stash show -p` works on
// a raw stash commit SHA too). A read failure is reported but non-fatal: the
// triage continues so one unreadable commit can't derail recovery.
func doctorShowDiff(ctx context.Context, stdout, stderr io.Writer, sha string) {
	patch, err := gitShow(ctx, sha)
	if err != nil {
		fmt.Fprintf(stderr, "    could not show diff: %v\n", err)
		return
	}
	if strings.TrimSpace(patch) == "" {
		fmt.Fprintln(stdout, "    (empty diff)")
		return
	}
	// Indent the patch a touch so it reads as nested under the entry.
	for _, line := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		fmt.Fprintf(stdout, "    \u2502 %s\n", line)
	}
}

// doctorDanglingLine formats a one-line summary of a recoverable stash for the
// report and prompts: short SHA, age, source, and its subject/branch.
func doctorDanglingLine(d git.DanglingStash) string {
	age := "unknown age"
	if !d.Created.IsZero() {
		age = humanizeSince(now(), d.Created)
	}
	subject := d.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	return fmt.Sprintf("%s  %s  [%s]  %s", shortSHA(d.SHA), age, d.Source, subject)
}

// doctorOrphanLine formats a one-line summary of an orphaned sidecar label.
func doctorOrphanLine(o meta.Orphan) string {
	label := o.Entry.Label
	if label == "" {
		label = "(no label)"
	}
	return fmt.Sprintf("%s  \u201C%s\u201D", shortSHA(o.SHA), label)
}

// shortSHA returns the first 12 chars of a SHA (or the whole thing if shorter),
// matching the abbreviated form people are used to from git output.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// humanizeSince renders an approximate, human-friendly age like "3 days" or
// "2 hours" between an earlier and later time. It is intentionally coarse — the
// doctor wants "roughly how old", not a precise duration.
func humanizeSince(nowT, then time.Time) string {
	d := nowT.Sub(then)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return pluralCount(int(d.Hours()), "hour", "hours")
	default:
		return pluralCount(int(d.Hours()/24), "day", "days")
	}
}

// pluralCount renders "<n> <unit>" picking the singular/plural noun for n.
func pluralCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// prompt writes a prompt to stdout and reads one trimmed, lower-cased line from
// in. On EOF (or a read error) it returns "q" so an interrupted/closed input
// cleanly ends the triage rather than looping forever.
func prompt(in *bufio.Reader, stdout io.Writer, label string) string {
	fmt.Fprint(stdout, label)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "q"
	}
	return strings.ToLower(strings.TrimSpace(line))
}
