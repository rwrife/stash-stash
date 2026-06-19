// Command stash-stash is a concierge for your git stash graveyard.
//
// M3: when stdout is a TTY it reads real stashes and opens an interactive
// Bubble Tea browser (scrollable list + diff preview); otherwise it prints the
// plain, non-interactive table from M2 so pipes and CI stay script-friendly.
// Sidecar labels and mutating actions arrive in later milestones (see PLAN.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/rwrife/stash-stash/internal/git"
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entrypoint. It returns the process exit code so that
// tests can exercise flag handling without calling os.Exit.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash %s — a concierge for your git stash graveyard.\n\n", version)
		fmt.Fprintf(stderr, "Usage:\n  stash-stash [flags]\n\nFlags:\n")
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

// listOrBrowse loads the current repo's stashes and either opens the
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
		fmt.Fprintln(stdout, "No stashes found. Your graveyard is empty — nice. 🪦")
		return 0
	}

	// Interactive path: only when we have a real terminal and the user didn't
	// opt out. Everything else (pipes, CI, --no-tui) gets the plain table.
	if !noTUIFlag && stdout == os.Stdout && stdoutIsTTY() {
		if err := tui.Run(stashes, git.Show, now(), os.Stdin, os.Stdout); err != nil {
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
