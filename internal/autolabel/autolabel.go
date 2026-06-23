// Package autolabel derives a sensible, human-readable label for a stash that
// has no explicit sidecar label, so the list shows "payments: fix retry"
// instead of git's anonymous "WIP on main: a1b2c3d …".
//
// The recipe (see issue #7) combines two signals the rest of stash-stash
// already gathers:
//
//   - the origin branch the stash was born on (the "area"), and
//   - the stash's top changed file/path (the "hint").
//
// The result is "<area>: <hint>". The hint (from the top changed file) is the
// load-bearing half: a stash with no detectable changed file gets no
// auto-label (the branch alone is already shown in its own column). A
// detached-HEAD stash with no branch falls back to just the hint. Derive
// returns "" when no hint can be built, so callers fall back to git's subject. The logic is deliberately dependency-free and pure so it is trivial
// to unit-test without a repo, and so callers (model, render, tui, jsonout) all
// derive identical labels.
//
// Auto-labels are advisory: they are never written to the sidecar. They are
// computed on the fly for display only, and callers visually distinguish them
// from real, user-chosen labels (e.g. dim/italic) so the user can tell a guess
// from a name they typed.
package autolabel

import (
	"path"
	"strings"
)

// branchPrefixes are the conventional "type/" prefixes teams put on branch
// names (git-flow, Conventional Branches, etc.). We strip them so the area
// reads as the actual topic ("payments") rather than the workflow noise
// ("feature/payments" → "payments"). Matched case-insensitively.
var branchPrefixes = []string{
	"feature", "feat", "features",
	"fix", "bugfix", "hotfix", "bug",
	"chore", "chores",
	"release", "releases",
	"refactor", "refactors",
	"task", "tasks",
	"wip", "draft",
	"users", "user", "dev",
}

// Derive returns the auto-label for a stash, given the origin branch and the
// stash's top changed file path. The hint (from the changed file) is the
// load-bearing signal, so Derive returns "" whenever there is no top file —
// the branch alone is already shown in its own column, so an area-only label
// would just be redundant noise. When a hint exists it returns:
//
//	"<area>: <hint>"  when the branch yields an area, else
//	"<hint>"          (e.g. a detached-HEAD stash with no branch).
//
// The output is display-ready (lower-noise, trimmed); callers add their own
// styling to mark it as a guess.
func Derive(branch, topFile string) string {
	hint := Hint(topFile)
	if hint == "" {
		// No changed-file signal: don't invent a label from the branch alone
		// (the branch is already displayed separately).
		return ""
	}
	if area := Area(branch); area != "" {
		return area + ": " + hint
	}
	return hint
}

// Area extracts the topic from a branch name: it drops a leading conventional
// type segment ("feature/", "fix/", …), keeps the most specific remaining
// segment, and tidies separators into spaces. Examples:
//
//	"payments"               -> "payments"
//	"feature/payments"       -> "payments"
//	"fix/payments-retry"     -> "payments retry"
//	"feature/payments/retry" -> "retry"  (deepest segment is most specific)
//	"PROJ-123-add-cache"     -> "PROJ-123 add cache"
//
// It returns "" for an empty/whitespace branch.
func Area(branch string) string {
	b := strings.TrimSpace(branch)
	if b == "" {
		return ""
	}

	// Split on path separators; a branch like "feature/payments/retry" is a
	// hierarchy where the deepest segment is the most specific topic.
	segs := splitNonEmpty(b, "/")
	if len(segs) == 0 {
		return ""
	}

	// Drop a single leading conventional type segment ("feature", "fix", …)
	// when there's something more specific after it.
	if len(segs) > 1 && isPrefixToken(segs[0]) {
		segs = segs[1:]
	}

	topic := segs[len(segs)-1]
	return tidy(topic)
}

// Hint derives a short, readable hint from a changed file path: it takes the
// base name, drops a single trailing extension, and tidies separators into
// spaces. Examples:
//
//	"internal/git/retry.go"  -> "retry"
//	"src/payments/Retry.tsx" -> "Retry"
//	"README.md"              -> "README"
//	"docs/api-spec.yaml"     -> "api spec"
//	"Makefile"               -> "Makefile"  (no extension to drop)
//
// A dotfile like ".gitignore" keeps its leading dot name ("gitignore" is wrong;
// we return "gitignore" without the dot but never an empty string). Returns ""
// for an empty path.
func Hint(topFile string) string {
	f := strings.TrimSpace(topFile)
	if f == "" {
		return ""
	}
	// Normalize separators so a Windows-style path still yields a clean base.
	f = strings.ReplaceAll(f, "\\", "/")

	base := path.Base(f)
	base = strings.TrimSpace(base)
	if base == "" || base == "/" || base == "." {
		return ""
	}

	// Strip a single trailing extension, but not a leading dot (dotfiles).
	if ext := path.Ext(base); ext != "" && ext != base {
		base = strings.TrimSuffix(base, ext)
	}
	// A dotfile like ".gitignore" has path.Ext == ".gitignore"; the guard above
	// (ext != base) leaves it intact, so trim the leading dot for readability.
	base = strings.TrimPrefix(base, ".")

	return tidy(base)
}

// tidy turns a raw token into a readable hint/area: separators (-, _, .) become
// spaces, runs of whitespace collapse to one, and the result is trimmed. It
// preserves the original casing (so "Retry" and "API" survive) since stash
// authors often encode meaning in case.
func tidy(s string) string {
	if s == "" {
		return ""
	}
	repl := strings.NewReplacer("-", " ", "_", " ", ".", " ")
	s = repl.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// isPrefixToken reports whether seg is a conventional branch-type prefix
// ("feature", "fix", …), matched case-insensitively.
func isPrefixToken(seg string) bool {
	low := strings.ToLower(strings.TrimSpace(seg))
	for _, p := range branchPrefixes {
		if low == p {
			return true
		}
	}
	return false
}

// splitNonEmpty splits s on sep and drops empty fields (e.g. from a leading or
// doubled separator), trimming each kept field.
func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
