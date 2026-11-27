package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/git"
)

func runHook(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: stash-stash hook init [--shell bash|zsh] | check [--stale-days N]")
		return 2
	}
	switch args[0] {
	case "init":
		return runHookInit(args[1:], stdout, stderr)
	case "check":
		return runHookCheck(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "stash-stash hook: unknown command %q\n", args[0])
		return 2
	}
}

func runHookInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash hook init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	shell := fs.String("shell", "", "shell to generate integration for (bash or zsh; defaults to $SHELL)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stash-stash hook init [--shell bash|zsh]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "stash-stash hook init: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	name := strings.ToLower(strings.TrimSpace(*shell))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(os.Getenv("SHELL")), ".exe")
	}
	if name != "bash" && name != "zsh" {
		fmt.Fprintf(stderr, "stash-stash hook init: unsupported shell %q (valid shells: bash, zsh)\n", name)
		return 2
	}
	_, err := fmt.Fprintln(stdout, `# Source this snippet to warn before leaving with stale stashes.
git() {
  if [ "$#" -gt 0 ]; then
    case "$1" in
      checkout|switch)
        if [ -t 2 ]; then
          command stash-stash hook check >&2 || true
        fi
        ;;
    esac
  fi
  command git "$@"
}`)
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash hook init: write shell integration: %v\n", err)
		return 1
	}
	return 0
}

func runHookCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash hook check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	staleDays := fs.Int("stale-days", defaultStaleDays, "stale threshold in days")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stash-stash hook check [--stale-days N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *staleDays < 0 {
		fmt.Fprintln(stderr, "stash-stash hook check: expected no arguments and a non-negative --stale-days value")
		return 2
	}
	if *staleDays == 0 {
		return 0
	}
	stashes, err := gitList(context.Background())
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			return 0
		}
		fmt.Fprintf(stderr, "stash-stash hook check: %v\n", err)
		return 1
	}
	dusty := 0
	for _, stash := range stashes {
		if age.Classify(stash.Created, now(), *staleDays).Dusty() {
			dusty++
		}
	}
	if dusty > 0 {
		fmt.Fprintf(stdout, "stash-stash: %d stale %s; run stash-stash to review before switching branches.\n",
			dusty, plural2(dusty, "stash", "stashes"))
	}
	return 0
}
