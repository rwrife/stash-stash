// Command stash-stash is a concierge for your git stash graveyard.
//
// This is the M1 scaffold: it builds, vets clean, parses a --version flag,
// and prints a stubbed "no stashes found" message. Real stash reading,
// labeling, and the TUI arrive in later milestones (see PLAN.md).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// version is the build version. It is overridable at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/stash-stash
var version = "dev"

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

	// M1 stub: real stash listing lands in M2.
	fmt.Fprintln(stdout, "No stashes found. (stash-stash is still scaffolding — see PLAN.md)")
	return 0
}
