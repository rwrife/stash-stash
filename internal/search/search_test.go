package search

import (
	"strings"
	"testing"
)

// samplePatch is a small but representative `git stash show -p` output: a
// modified file (mixed add/remove/context), a brand-new file, and a deleted
// file, plus assorted diff metadata that must never be treated as content.
const samplePatch = `diff --git a/internal/git/retry.go b/internal/git/retry.go
index 1111111..2222222 100644
--- a/internal/git/retry.go
+++ b/internal/git/retry.go
@@ -10,7 +10,8 @@ func Do() {
 	// context before
-	retryBudget := 3
+	retryBudget := 5
+	logRetry("attempt")
 	// context after
 }
diff --git a/docs/new.md b/docs/new.md
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/docs/new.md
@@ -0,0 +1,2 @@
+# Heading
+mentions retry in prose
diff --git a/old/gone.txt b/old/gone.txt
deleted file mode 100644
index 4444444..0000000
--- a/old/gone.txt
+++ /dev/null
@@ -1,1 +0,0 @@
-stale retry note
`

func TestScanSubstringCaseInsensitive(t *testing.T) {
	got := Scan(samplePatch, Literal("RETRY"))
	// Expected matches (case-insensitive substring "retry"):
	//   retry.go: -retryBudget := 3, +retryBudget := 5, +logRetry("attempt")
	//   new.md:   +mentions retry in prose
	//   gone.txt: -stale retry note
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5\n%s", len(got), dump(got))
	}

	// First match: the removed line in retry.go at old-line 11.
	if got[0].File != "internal/git/retry.go" || got[0].Kind != KindRemoved || got[0].Line != 11 {
		t.Errorf("match[0] = %+v, want retry.go removed @11", got[0])
	}
	if got[0].Text != "\tretryBudget := 3" {
		t.Errorf("match[0].Text = %q, want the removed line with its indentation", got[0].Text)
	}
	// Added replacement at new-line 11, then the new logRetry at new-line 12.
	if got[1].Kind != KindAdded || got[1].Line != 11 {
		t.Errorf("match[1] = %+v, want added @11", got[1])
	}
	if got[2].Kind != KindAdded || got[2].Line != 12 || !strings.Contains(got[2].Text, "logRetry") {
		t.Errorf("match[2] = %+v, want added @12 logRetry", got[2])
	}

	// New-file match attributes to the b/ path even though --- is /dev/null.
	if got[3].File != "docs/new.md" || got[3].Kind != KindAdded || got[3].Line != 2 {
		t.Errorf("match[3] = %+v, want docs/new.md added @2", got[3])
	}

	// Deleted-file match attributes to the a/ path (b/ side is /dev/null).
	if got[4].File != "old/gone.txt" || got[4].Kind != KindRemoved || got[4].Line != 1 {
		t.Errorf("match[4] = %+v, want old/gone.txt removed @1", got[4])
	}
}

func TestScanContextLineMatches(t *testing.T) {
	// "context" appears only on unchanged context lines.
	got := Scan(samplePatch, Literal("context"))
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 context matches\n%s", len(got), dump(got))
	}
	for _, m := range got {
		if m.Kind != KindContext {
			t.Errorf("match %+v: kind = %v, want context", m, m.Kind)
		}
	}
	// "context before" sits at new-line 10, "context after" at new-line 13.
	if got[0].Line != 10 || got[1].Line != 13 {
		t.Errorf("context lines = %d,%d, want 10,13", got[0].Line, got[1].Line)
	}
}

func TestScanDoesNotMatchDiffMetadata(t *testing.T) {
	// "index", "diff", "mode", and the file paths themselves appear in metadata
	// lines; none of those should ever produce a content match.
	for _, needle := range []string{"diff --git", "index 1111111", "new file mode", "deleted file mode"} {
		if got := Scan(samplePatch, Literal(needle)); len(got) != 0 {
			t.Errorf("Scan(%q) matched metadata: %s", needle, dump(got))
		}
	}
}

func TestScanRegexCaseInsensitiveByDefault(t *testing.T) {
	m, err := Regexp(`retrybudget := [0-9]`)
	if err != nil {
		t.Fatalf("Regexp() error = %v", err)
	}
	got := Scan(samplePatch, m)
	// Case-insensitive: matches both retryBudget lines (added + removed).
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2\n%s", len(got), dump(got))
	}
	if got[0].Kind != KindRemoved || got[1].Kind != KindAdded {
		t.Errorf("kinds = %v,%v, want removed,added", got[0].Kind, got[1].Kind)
	}
}

func TestRegexInvalidPattern(t *testing.T) {
	if _, err := Regexp("("); err == nil {
		t.Fatal("Regexp(\"(\") error = nil, want a compile error")
	}
}

func TestRegexRespectsInlineFlags(t *testing.T) {
	// A pattern that opts into case-sensitivity via an inline flag must be
	// honored (we only auto-prepend (?i) when the user didn't set flags).
	m, err := Regexp(`(?-i:RETRY)`)
	if err != nil {
		t.Fatalf("Regexp() error = %v", err)
	}
	// Only the literal-uppercase "RETRY" (in "logRetry"? no — that's mixed) ...
	// In samplePatch the only uppercase "RETRY" substring is none; ensure the
	// case-sensitive pattern does NOT match the lowercase "retry" lines.
	if got := Scan(samplePatch, m); len(got) != 0 {
		t.Errorf("case-sensitive RETRY matched lowercase lines: %s", dump(got))
	}
}

func TestSubstringEmptyNeedleMatchesNothing(t *testing.T) {
	if Literal("").MatchLine("anything") {
		t.Error("empty needle should match nothing")
	}
}

func TestScanEmptyInputs(t *testing.T) {
	if got := Scan("", Literal("x")); got != nil {
		t.Errorf("Scan(empty patch) = %v, want nil", got)
	}
	if got := Scan(samplePatch, nil); got != nil {
		t.Errorf("Scan(nil matcher) = %v, want nil", got)
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		in     string
		oStart int
		nStart int
		ok     bool
	}{
		{"@@ -10,7 +10,8 @@ func Do() {", 10, 10, true},
		{"@@ -0,0 +1,2 @@", 0, 1, true},
		{"@@ -1 +1 @@", 1, 1, true}, // single-line ranges omit the count
		{"@@ no closing", 0, 0, false},
		{"not a hunk", 0, 0, false},
	}
	for _, c := range cases {
		o, n, ok := parseHunkHeader(c.in)
		if ok != c.ok || (ok && (o != c.oStart || n != c.nStart)) {
			t.Errorf("parseHunkHeader(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, o, n, ok, c.oStart, c.nStart, c.ok)
		}
	}
}

func TestLineKindSymbol(t *testing.T) {
	if KindAdded.Symbol() != "+" || KindRemoved.Symbol() != "-" || KindContext.Symbol() != " " {
		t.Errorf("symbols = %q/%q/%q, want +/-/space",
			KindAdded.Symbol(), KindRemoved.Symbol(), KindContext.Symbol())
	}
}

func TestTrimLine(t *testing.T) {
	if got := trimLine("\tindented  \r"); got != "\tindented" {
		t.Errorf("trimLine = %q, want leading tab kept, trailing stripped", got)
	}
}

// dump renders matches compactly for failure messages.
func dump(ms []Match) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.File)
		b.WriteString(":")
		b.WriteString(m.Kind.Symbol())
		b.WriteString(m.Text)
		b.WriteString("\n")
	}
	return b.String()
}
