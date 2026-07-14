// Compost command (issue #27): auto-archive stale stashes instead of dropping
// them. `stash-stash compost` moves stashes older than --stale-days into the
// durable archive namespace (refs/stash-stash/archive/<sha>), so they leave the
// active list but stay 100% recoverable. `--list` shows what's archived and
// `--restore <id>` brings one back as a real stash. Sidecar metadata (label,
// tags, notes) is SHA-keyed and follows the stash into and out of the archive
// automatically.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/git"
	"github.com/rwrife/stash-stash/internal/model"
)

// runCompost implements `stash-stash compost [--stale-days N] [--list] [--restore <id>]`.
//
// Modes (mutually exclusive):
//   - --list           : enumerate archived stashes and exit (read-only).
//   - --restore <id>   : bring one archived stash back to the stack.
//   - (default)        : bulk-compost every stash at/over the stale threshold.
//
// It returns the process exit code: 0 on success (including "nothing to do",
// which is reported but not an error), 2 on bad flags/usage, and 1 on a git or
// sidecar failure.
func runCompost(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stash-stash compost", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "stash-stash compost \u2014 archive stale stashes instead of losing them.\n\n")
		fmt.Fprintf(stderr, "Instead of dropping a dusty stash (recoverable only via the reflog), compost pins\nit under refs/stash-stash/archive/<sha> and removes it from the active list \u2014\n100%% recoverable, forever.\n\n")
		fmt.Fprintf(stderr, "Usage:\n  stash-stash compost [--stale-days N]   archive stashes at/over the threshold\n  stash-stash compost --list             list archived stashes\n  stash-stash compost --restore <id>     restore an archived stash\n\nFlags:\n")
		fs.PrintDefaults()
	}
	staleDays := fs.Int("stale-days", defaultStaleDays, "compost stashes older than this many days (0 composts all)")
	list := fs.Bool("list", false, "list archived stashes instead of composting")
	restore := fs.String("restore", "", "restore an archived stash by SHA (or unique prefix)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "stash-stash compost: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *staleDays < 0 {
		fmt.Fprintln(stderr, "stash-stash compost: --stale-days must be >= 0 (0 composts every stash).")
		return 2
	}
	if *list && strings.TrimSpace(*restore) != "" {
		fmt.Fprintln(stderr, "stash-stash compost: use either --list or --restore, not both.")
		return 2
	}

	ctx := context.Background()

	switch {
	case *list:
		return runCompostList(ctx, stdout, stderr)
	case strings.TrimSpace(*restore) != "":
		return runCompostRestore(ctx, strings.TrimSpace(*restore), stdout, stderr)
	default:
		return runCompostArchive(ctx, *staleDays, stdout, stderr)
	}
}

// runCompostArchive bulk-composts every stash whose age is at or past the stale
// threshold. It lists stashes, enriches labels for friendly output, filters by
// staleness, then archives each (newest-first is fine — each drop reshuffles
// indices, so we re-resolve by re-listing after every archive to stay correct).
func runCompostArchive(ctx context.Context, staleDays int, stdout, stderr io.Writer) int {
	stashes, err := gitList(ctx)
	if err != nil {
		return reportListErr(err, stderr)
	}
	if len(stashes) == 0 {
		fmt.Fprintln(stdout, "No stashes to compost \u2014 your graveyard is already empty. \U0001FAA6")
		return 0
	}

	// Attach labels so the report reads in human terms; best-effort.
	loadAndApplyMeta(ctx, stashes, stderr)

	// Select the dusty ones. staleDays==0 means "compost everything".
	var targets []model.Stash
	for _, s := range stashes {
		if staleDays == 0 || age.Classify(s.Created, now(), staleDays).Dusty() {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintf(stdout, "Nothing older than %d day(s) to compost. Everything's fresh. \u2728\n", staleDays)
		return 0
	}

	// Archive by content SHA. Because each `git stash drop` shifts stash@{N}
	// indices, we can't trust the pre-listed Ref() after the first drop. Instead
	// we re-list before each archive and find the target by its stable SHA, so
	// indices are always correct. Archiving is keyed on SHA which never moves.
	archived := 0
	for _, want := range targets {
		fresh, lerr := gitList(ctx)
		if lerr != nil {
			return reportListErr(lerr, stderr)
		}
		ref, ok := refForSHA(fresh, want.SHA)
		if !ok {
			// Already gone (e.g. concurrently dropped) — skip quietly.
			continue
		}
		if _, aerr := gitArchive(ctx, ref); aerr != nil {
			if errors.Is(aerr, git.ErrAlreadyArchived) {
				continue
			}
			fmt.Fprintf(stderr, "stash-stash compost: could not archive %s (%v).\n", labelFor(want), aerr)
			continue
		}
		archived++
		fmt.Fprintf(stdout, "\U0001F343 composted %s  (%s)\n", labelFor(want), shortSHA(want.SHA))
	}

	if archived == 0 {
		fmt.Fprintln(stdout, "Nothing composted.")
		return 0
	}
	fmt.Fprintf(stdout, "\nComposted %d stash(es). Recover any with `stash-stash compost --restore <id>`.\n", archived)
	return 0
}

// runCompostList prints every archived stash (compost --list): label/subject,
// age of the work, and when it was archived, so nothing is ever truly lost.
func runCompostList(ctx context.Context, stdout, stderr io.Writer) int {
	archived, err := gitArchivedStashes(ctx)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		}
		fmt.Fprintf(stderr, "stash-stash: %v\n", err)
		return 1
	}
	if len(archived) == 0 {
		fmt.Fprintln(stdout, "No composted stashes. The archive is empty.")
		return 0
	}

	// Enrich with sidecar labels (SHA-keyed) for friendly names.
	store, _ := metaLoad(ctx)

	fmt.Fprintf(stdout, "Composted stashes (%d):\n\n", len(archived))
	for _, a := range archived {
		name := a.Subject
		if store != nil {
			if label, ok := store.Label(a.SHA); ok && label != "" {
				name = label
			}
		}
		if strings.TrimSpace(name) == "" {
			name = "(no subject)"
		}
		workAge := "unknown age"
		if !a.Created.IsZero() {
			workAge = age.Humanize(a.Created, now())
		}
		archivedAge := ""
		if !a.ArchivedAt.IsZero() {
			archivedAge = ", archived " + age.Humanize(a.ArchivedAt, now()) + " ago"
		}
		fmt.Fprintf(stdout, "  %s  %s  (%s old%s)\n", shortSHA(a.SHA), name, workAge, archivedAge)
	}
	fmt.Fprintf(stdout, "\nRestore one with `stash-stash compost --restore <id>`.\n")
	return 0
}

// runCompostRestore brings a single archived stash back to the stack
// (compost --restore <id>). id may be a full SHA or any unique prefix.
func runCompostRestore(ctx context.Context, id string, stdout, stderr io.Writer) int {
	full, err := gitRestore(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, git.ErrNotARepo):
			fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
			return 1
		case errors.Is(err, git.ErrNotArchived):
			fmt.Fprintf(stderr, "stash-stash compost: no composted stash matches %q. Try `compost --list`.\n", id)
			return 1
		default:
			fmt.Fprintf(stderr, "stash-stash compost: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "\u267B\uFE0F  restored %s to the stack as stash@{0}. Its label, tags, and notes came back with it.\n", shortSHA(full))
	return 0
}

// refForSHA finds the current stash@{N} ref for a given content SHA in a fresh
// listing, so archiving stays correct even as indices shift between drops.
func refForSHA(stashes []model.Stash, sha string) (string, bool) {
	for _, s := range stashes {
		if s.SHA == sha {
			return s.Ref(), true
		}
	}
	return "", false
}

// labelFor returns the best human name for a stash in compost output: its
// display label, falling back to a short SHA when even the subject is blank.
func labelFor(s model.Stash) string {
	if d := strings.TrimSpace(s.Display()); d != "" {
		return d
	}
	return shortSHA(s.SHA)
}

// reportListErr maps a gitList error to a friendly message + exit code, shared
// by the compost sub-modes so the not-a-repo case reads consistently.
func reportListErr(err error, stderr io.Writer) int {
	if errors.Is(err, git.ErrNotARepo) {
		fmt.Fprintln(stderr, "stash-stash: not a git repository (run me inside one).")
		return 1
	}
	fmt.Fprintf(stderr, "stash-stash: %v\n", err)
	return 1
}
