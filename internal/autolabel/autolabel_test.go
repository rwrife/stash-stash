package autolabel

import (
	"strings"
	"testing"
)

func TestArea(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		{"", ""},
		{"   ", ""},
		{"main", "main"},
		{"payments", "payments"},
		{"feature/payments", "payments"},
		{"feat/payments", "payments"},
		{"fix/payments-retry", "payments retry"},
		{"bugfix/cache_miss", "cache miss"},
		{"hotfix/login", "login"},
		{"chore/deps", "deps"},
		// Deepest segment is the most specific topic.
		{"feature/payments/retry", "retry"},
		// A lone prefix-looking name is kept (nothing more specific follows).
		{"feature", "feature"},
		{"fix", "fix"},
		// Ticket-style branches tidy all separators into spaces.
		{"PROJ-123-add-cache", "PROJ 123 add cache"},
		// Leading/doubled slashes don't produce empty segments.
		{"/feature//payments/", "payments"},
		// Prefix match is case-insensitive.
		{"Feature/Search", "Search"},
	}
	for _, c := range cases {
		if got := Area(c.branch); got != c.want {
			t.Errorf("Area(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

func TestHint(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"internal/git/retry.go", "retry"},
		{"src/payments/Retry.tsx", "Retry"},
		{"README.md", "README"},
		{"docs/api-spec.yaml", "api spec"},
		{"path/to/some_module.py", "some module"},
		// No extension to drop.
		{"Makefile", "Makefile"},
		{"cmd/tool/Dockerfile", "Dockerfile"},
		// Dotfile: keep the name, drop the leading dot.
		{".gitignore", "gitignore"},
		// Only the final extension is stripped; ".env.local" -> ".env" -> "env".
		{"config/.env.local", "env"},
		// Windows-style separators normalize to a clean base.
		{"src\\pkg\\thing.go", "thing"},
		// Multi-dot filename: only the final extension is stripped.
		{"archive.tar.gz", "archive tar"},
	}
	for _, c := range cases {
		if got := Hint(c.file); got != c.want {
			t.Errorf("Hint(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestDerive(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		file   string
		want   string
	}{
		{"both", "feature/payments", "internal/retry.go", "payments: retry"},
		{"both with tidy", "fix/auth", "src/login-form.tsx", "auth: login form"},
		// Hint is required: branch-only yields no auto-label (branch shows in its
		// own column).
		{"branch only (no file)", "feature/payments", "", ""},
		{"file only (detached HEAD)", "", "internal/cache.go", "cache"},
		{"neither", "", "", ""},
		{"plain main + file", "main", "README.md", "main: README"},
		{"whitespace only", "   ", "  ", ""},
	}
	for _, c := range cases {
		if got := Derive(c.branch, c.file); got != c.want {
			t.Errorf("%s: Derive(%q, %q) = %q, want %q", c.name, c.branch, c.file, got, c.want)
		}
	}
}

// Derive must never return leading/trailing whitespace or a dangling ": ".
func TestDeriveNoDanglingSeparator(t *testing.T) {
	// Branch but no file → no label at all (not "payments: ").
	if got := Derive("feature/payments", ""); got != "" {
		t.Errorf("Derive with empty file = %q, want \"\" (no dangling colon)", got)
	}
	if got := Derive("", "x.go"); got != "x" {
		t.Errorf("Derive with empty branch = %q, want %q", got, "x")
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"simple label", "payments: fix retry", "payments-fix-retry"},
		{"git subject", "WIP on main: a1b2c3d", "wip-on-main-a1b2c3d"},
		{"keeps slashes", "feature/login flow", "feature/login-flow"},
		{"collapses spaces", "  spaced   out  ", "spaced-out"},
		{"lowercases", "Payments Retry", "payments-retry"},
		{"strips punctuation runs", "fix!!! the?? bug", "fix-the-bug"},
		{"trims separators", "--edge--", "edge"},
		{"double dots forbidden", "a..b", "a-b"},
		{"no doubled slashes", "a//b", "a/b"},
		{"trailing slash dropped", "feature/", "feature"},
		{"leading slash dropped", "/feature", "feature"},
		{"dot-lock suffix stripped", "release.lock", "release"},
		{"only punctuation is empty", "!@#$%", ""},
		{"underscores survive", "snake_case_name", "snake_case_name"},
		{"unicode becomes hyphen", "café résumé", "caf-r-sum"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("%s: Slug(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// Slug output must always be a name `git check-ref-format` would accept: no
// spaces, no forbidden metacharacters, no ".." or doubled/edge slashes.
func TestSlugProducesSafeRefs(t *testing.T) {
	inputs := []string{
		"payments: fix retry",
		"WIP on main: a1b2c3d Tidy things",
		"feature/PROJ-123: add cache layer~v2",
		"weird:?*[chars]\\here",
		"...dots...and---dashes...",
	}
	const forbidden = " ~^:?*[\\"
	for _, in := range inputs {
		got := Slug(in)
		if got == "" {
			continue // empty is a valid "no suggestion" signal
		}
		if strings.ContainsAny(got, forbidden) {
			t.Errorf("Slug(%q) = %q contains a git-forbidden character", in, got)
		}
		if strings.Contains(got, "..") {
			t.Errorf("Slug(%q) = %q contains '..'", in, got)
		}
		if strings.Contains(got, "//") {
			t.Errorf("Slug(%q) = %q contains '//'", in, got)
		}
		if strings.HasPrefix(got, "/") || strings.HasSuffix(got, "/") {
			t.Errorf("Slug(%q) = %q has an edge slash", in, got)
		}
		if strings.HasSuffix(got, ".lock") {
			t.Errorf("Slug(%q) = %q ends in .lock", in, got)
		}
	}
}
