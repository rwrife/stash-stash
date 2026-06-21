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
	"github.com/rwrife/stash-stash/internal/meta"
	"github.com/rwrife/stash-stash/internal/model"
	"github.com/rwrife/stash-stash/internal/render"
	"github.com/rwrife/stash-stash/internal/tui"
)

// version is the build version. It is overridable at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/stash-stash
var version = "dev"

// now is indirected so tests can pin the clock when checking age output.
var now = time.Now

// stdoutIsTTY reports whether standard output is an interactive terminal. It
// is a package var so tests can force the plain-table path. The real check
// uses the actual os.Stdout file descriptor.
var stdoutIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// noTUI lets `--no-tui` (or a non-TTY stdout) force the plain table even in an
// interactive shell, which is handy for screenshots, logs, and scripting.
var noTUIFlag bool

// gitPush and metaLoad are indirected so the push subcommand can be unit-tested
// without a real repo or working-tree changes. They default to the real
// implementations.
var (
	gitPush  = git.Push
	metaLoad = meta.Load
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entrypoint. It returns the process exit code so that
// tests can exercise flag handling without calling os.Exit.
func run(args []string, stdout, stderr io.Writer) int {
	// Subcommand dispatch happens before flag parsing so subcommands own their
	// own flags. Today only `push` exists; everything else is the default
	// list/browse behavior (optionally with top-level flags).
	if len(args) > 0 && args[0] == "push" {
		return runPush(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("stash-stash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash %s — a concierge for your git stash graveyard.\n\n", version)
		fmt.Fprintf(stderr, "Usage:\n  stash-stash [flags]\n  stash-stash push [-m \"label\"]\n\nFlags:\n")
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(&noTUIFlag, "no-tui", false, "force plain table output even on a TTY")

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

	return listOrBrowse(stdout, stderr)
}

// listOrBrowse loads the current repo's stashes, enriches them with sidecar
// labels (matched by content SHA) and diffstats, and either opens the
// interactive TUI (TTY stdout, not --no-tui) or prints the plain table. It
// returns the process exit code: 0 on success (including the no-stash case),
// 1 on a real error talking to git, 1 on a TUI runtime failure.
func listOrBrowse(stdout, stderr io.Writer) int {
	ctx := context.Background()

	stashes, err := git.List(ctx)
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
		fmt.Fprintln(stdout, "No stashes found. Your graveyard is empty — nice. 🪦")
		return 0
	}

	// Enrich with sidecar labels + diffstats. Metadata is best-effort: a
	// sidecar problem must never stop us listing stashes, so a load error is
	// reported to stderr but we continue label-less.
	store := loadAndApplyMeta(ctx, stashes, stderr)
	git.EnrichDiffstats(ctx, stashes)

	// Interactive path: only when we have a real terminal and the user didn't
	// opt out. Everything else (pipes, CI, --no-tui) gets the plain table.
	if !noTUIFlag && stdout == os.Stdout && stdoutIsTTY() {
		// Wire the mutating actions. Reload re-lists and re-enriches so the TUI
		// can resync after a pop/drop (indices shift). It is best-effort: a
		// reload error is surfaced in-TUI, not fatal.
		actions := tui.Actions{
			Apply: git.Apply,
			Pop:   git.Pop,
			Drop:  git.Drop,
			Reload: func(ctx context.Context) ([]model.Stash, error) {
				fresh, err := git.List(ctx)
				if err != nil {
					return nil, err
				}
				if store != nil {
					for i := range fresh {
						if label, ok := store.Label(fresh[i].SHA); ok {
							fresh[i].Label = label
						}
					}
				}
				git.EnrichDiffstats(ctx, fresh)
				return fresh, nil
			},
		}
		if err := tui.Run(stashes, git.Show, store, actions, now(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(stderr, "stash-stash: tui: %v\n", err)
			return 1
		}
		return 0
	}

	if err := render.Table(stdout, stashes, now()); err != nil {
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
