// Command stash-stash is a concierge for your git stash graveyard.
//
// M2: it reads real stashes via `git stash list` and prints a plain,
// non-interactive table (index | subject | age | branch). The Bubble Tea TUI,
// sidecar labels, and mutating actions arrive in later milestones (see PLAN.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/render"
)

// version is the build version. It is overridable at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/stash-stash
var version = "dev"

// now is indirected so tests can pin the clock when checking age output.
var now = time.Now

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

	return list(stdout, stderr)
}

// list reads the current repo's stashes and prints them as a table. It returns
// the process exit code: 0 on success (including the no-stash case), 1 on a
// real error talking to git.
func list(stdout, stderr io.Writer) int {
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

	if err := render.Table(stdout, stashes, now()); err != nil {
		fmt.Fprintf(stderr, "stash-stash: rendering table: %v\n", err)
		return 1
	}
	return 0
}
