package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/meta"
)

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outFile := fs.String("out", "", "write the bundle to a file instead of stdout")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stash-stash export [--out file]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "stash-stash export: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	store, err := metaLoad(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash export: %v\n", err)
		return 1
	}
	data, err := store.EncodeBundle()
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash export: %v\n", err)
		return 1
	}
	if *outFile == "" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "stash-stash export: write bundle: %v\n", err)
			return 1
		}
		return 0
	}
	if err := os.WriteFile(*outFile, data, 0600); err != nil {
		fmt.Fprintf(stderr, "stash-stash export: write %q: %v\n", *outFile, err)
		return 1
	}
	return 0
}

func runImport(args []string, stdout, stderr io.Writer) int {
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file = args[0]
		args = args[1:]
	}

	fs := flag.NewFlagSet("stash-stash import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite conflicting local labels or notes")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: stash-stash import <file> [--force]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if file == "" {
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "stash-stash import: expected one bundle file")
			return 2
		}
		file = fs.Arg(0)
	} else if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "stash-stash import: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash import: read %q: %v\n", file, err)
		return 1
	}
	bundle, err := meta.DecodeBundle(data)
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash import: %v\n", err)
		return 1
	}

	ctx := context.Background()
	stashes, err := gitList(ctx)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
		} else {
			fmt.Fprintf(stderr, "stash-stash import: %v\n", err)
		}
		return 1
	}
	store, err := metaLoad(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash import: %v\n", err)
		return 1
	}
	live := make(map[string]struct{}, len(stashes))
	for _, stash := range stashes {
		if stash.SHA != "" {
			live[stash.SHA] = struct{}{}
		}
	}
	result, err := store.MergeBundle(bundle, live, *force)
	if err != nil {
		fmt.Fprintf(stderr, "stash-stash import: %v\n", err)
		return 1
	}
	if result.Added+result.Merged+result.Overwritten > 0 {
		if err := store.Save(); err != nil {
			fmt.Fprintf(stderr, "stash-stash import: could not save sidecar: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "Imported %d new, merged %d, overwrote %d conflicting entr%s.\n",
		result.Added, result.Merged, result.Overwritten, plural2(result.Overwritten, "y", "ies"))
	for _, sha := range result.Unknown {
		fmt.Fprintf(stdout, "Skipped unknown SHA %s (not present in this repository).\n", sha)
	}
	for _, sha := range result.Conflicts {
		fmt.Fprintf(stdout, "Skipped conflicting SHA %s (use --force to overwrite).\n", sha)
	}
	return 0
}
