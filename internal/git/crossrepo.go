// Cross-repo support (issue #33): discover git repositories under a directory
// tree and list each one's stashes, so `stash-stash --all-repos <dir>` can
// surface dusty stashes across every repo you own — not just the one you happen
// to be sitting in.
//
// This is read-only: discovery walks the filesystem and listing shells out to
// the user's real `git` with `-C <dir>`; no mutation paths run here.
package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rwrife/stash-stash/internal/model"
)

// skipDirNames are directory names we never descend into during discovery:
// vendored/dependency trees and build output that would otherwise flood the
// walk (and can themselves contain nested repos we don't care about). Matching
// is by exact base name.
var skipDirNames = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	".terraform":   {},
	".venv":        {},
	"venv":         {},
	"__pycache__":  {},
	"dist":         {},
	"build":        {},
	"target":       {},
}

// defaultMaxDepth bounds how deep DiscoverRepos descends below the root, so a
// scan of a large home directory can't wander arbitrarily far. Depth 0 is the
// root itself; a repo directly under the root is depth 1. It is deliberately
// generous but finite.
const defaultMaxDepth = 8

// DiscoverRepos walks the tree rooted at root and returns the paths of every
// git repository it finds, sorted for stable output. A directory is treated as
// a repo when it contains a `.git` entry (directory or file — the latter covers
// worktrees/submodules). Once a repo is found we do not descend into it (its
// nested submodules are the parent's concern, not separate dashboard rows).
//
// Discovery skips common dependency/build directories (see skipDirNames) and
// bounds its depth (defaultMaxDepth). Individual unreadable directories are
// skipped rather than aborting the whole walk, so one permission error deep in
// the tree can't blank the dashboard. It returns an error only when root itself
// cannot be used (missing, or not a directory).
func DiscoverRepos(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("cannot scan %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cannot scan %q: not a directory", root)
	}

	// Normalize the root so computed depths and returned paths are consistent.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}

	var repos []string
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Unreadable entry: skip it (and its subtree if it's a dir) but keep
			// walking the rest of the tree.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		// Enforce the depth bound relative to the root.
		if depthBelow(absRoot, path) > defaultMaxDepth {
			return fs.SkipDir
		}

		base := d.Name()

		// Skip noise directories outright (but never the root itself).
		if path != absRoot {
			if _, skip := skipDirNames[base]; skip {
				return fs.SkipDir
			}
		}

		// A `.git` entry (dir or file) marks this directory as a repo.
		if hasGit(path) {
			repos = append(repos, path)
			// Don't descend into a repo's own tree.
			return fs.SkipDir
		}
		return nil
	})

	sort.Strings(repos)
	return repos, nil
}

// hasGit reports whether dir contains a `.git` entry (directory or file).
func hasGit(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// depthBelow returns how many path segments deep child is below root. root
// itself is depth 0; an immediate child is depth 1. If child is not under root
// (shouldn't happen during a rooted walk) it returns a large number so the
// caller treats it as "too deep".
func depthBelow(root, child string) int {
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return 1 << 30
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// runnerIn is the cross-repo analogue of runner: it runs a git invocation with
// `-C dir` prepended so the command operates on the given repo regardless of
// the caller's working directory. It is a package variable so tests can stub
// git per-repo without real repositories on disk.
var runnerIn = func(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
	full := append([]string{"-C", dir}, args...)
	return runner(ctx, full...)
}

// ListIn returns all stashes in the repository at dir, most-recent first,
// exactly like List but for an arbitrary repo path (via `git -C dir`). It
// returns ErrNotARepo when dir is not a git work tree, and an empty slice (nil
// error) when the repo simply has no stashes.
func ListIn(ctx context.Context, dir string) ([]model.Stash, error) {
	stdout, stderr, err := runnerIn(ctx, dir, "stash", "list", "--format="+listFormat)
	if err != nil {
		if isNotARepo(stderr) {
			return nil, ErrNotARepo
		}
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("git -C %s stash list: %s", dir, msg)
		}
		return nil, fmt.Errorf("git -C %s stash list: %w", dir, err)
	}
	return parseList(stdout), nil
}

// EnrichDiffstatsIn computes and attaches a Diffstat and top changed file to
// each stash in the repository at dir, the cross-repo analogue of
// EnrichDiffstats. It runs one `git -C dir stash show --numstat` per stash; a
// per-stash failure is non-fatal (that entry keeps a zero Diffstat) so one odd
// stash can't blank the row. Returns the (same, mutated) slice for convenience.
func EnrichDiffstatsIn(ctx context.Context, dir string, stashes []model.Stash) []model.Stash {
	for i := range stashes {
		stdout, stderr, err := runnerIn(ctx, dir, "stash", "show", "--numstat", stashes[i].Ref())
		if err != nil {
			_ = stderr
			continue
		}
		ds, top := parseNumstat(stdout)
		stashes[i].Diffstat = ds
		stashes[i].TopFile = top
	}
	return stashes
}

// RepoStashes pairs a repository path with its stashes for the cross-repo
// dashboard. Repo is the path as returned by DiscoverRepos; Stashes is the
// (possibly empty) list from ListIn, most-recent first.
type RepoStashes struct {
	// Repo is the repository's path (absolute, as discovered).
	Repo string

	// Stashes are the repo's stashes, most-recent first. Empty when the repo
	// has none.
	Stashes []model.Stash
}

// ScanRepos discovers every git repo under root and lists each one's stashes,
// returning only the repos that actually have at least one stash (an empty repo
// contributes nothing to a "dusty stashes" dashboard). Results are ordered by
// repo path for stable output.
//
// A per-repo listing error is non-fatal: that repo is skipped so one broken
// repo can't blank the whole dashboard. It returns an error only when root
// itself cannot be scanned (see DiscoverRepos).
func ScanRepos(ctx context.Context, root string) ([]RepoStashes, error) {
	repos, err := DiscoverRepos(root)
	if err != nil {
		return nil, err
	}
	var out []RepoStashes
	for _, repo := range repos {
		stashes, lerr := ListIn(ctx, repo)
		if lerr != nil {
			// Skip repos we can't read; the dashboard is best-effort.
			continue
		}
		if len(stashes) == 0 {
			continue
		}
		out = append(out, RepoStashes{Repo: repo, Stashes: stashes})
	}
	return out, nil
}
