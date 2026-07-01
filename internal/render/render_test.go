package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
	"github.com/rwrife/stash-stash/internal/search"
)

func TestTable(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "deadbeef", Subject: "WIP on main: fix retry", Branch: "main", Created: now.Add(-2 * time.Hour),
			Diffstat: model.Diffstat{Added: 12, Deleted: 3, Files: 2}},
		{Index: 1, SHA: "cafebabe", Subject: "On feature/x: half-done", Branch: "", Created: now.Add(-5 * 24 * time.Hour),
			Label: "payments: retry fix"},
	}

	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{"INDEX", "LABEL", "AGE", "BRANCH", "CHANGES",
		"stash@{0}", "stash@{1}", "2h", "5d", "main",
		"+12 -3",              // diffstat for row 0
		"payments: retry fix", // sidecar label for row 1 (overrides subject)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n---\n%s", want, out)
		}
	}

	// The sidecar label must replace the raw subject when present.
	if strings.Contains(out, "On feature/x: half-done") {
		t.Errorf("labeled stash still shows raw subject:\n%s", out)
	}

	// Neither stash is dusty at staleDays=14 (2h, 5d), so no banner/legend:
	// header + 2 rows only.
	if strings.Contains(out, "gathering dust") {
		t.Errorf("unexpected dust banner for fresh stashes:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[2], "-") {
		t.Errorf("row for empty-branch stash missing dash: %q", lines[2])
	}
}

func TestTableDustBanner(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "a", Subject: "fresh", Branch: "main", Created: now.Add(-1 * time.Hour)},
		{Index: 1, SHA: "b", Subject: "old1", Branch: "main", Created: now.Add(-20 * 24 * time.Hour)},
		{Index: 2, SHA: "c", Subject: "old2", Branch: "main", Created: now.Add(-40 * 24 * time.Hour)},
	}

	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()

	// Two of three are dusty (20d, 40d >= 14d); the banner must say so and the
	// legend must appear.
	if !strings.Contains(out, "2 stashes are gathering dust") {
		t.Errorf("expected dust banner counting 2 stashes:\n%s", out)
	}
	if !strings.Contains(out, "older than 14d") {
		t.Errorf("banner should mention the threshold:\n%s", out)
	}
	if !strings.Contains(out, "(* = stale: older than 14d)") {
		t.Errorf("expected stale legend:\n%s", out)
	}
	// Dusty rows must have starred ages (Humanize renders 20d as "2w", 40d as
	// "1mo").
	if !strings.Contains(out, "2w*") || !strings.Contains(out, "1mo*") {
		t.Errorf("dusty rows should have starred ages:\n%s", out)
	}
}

func TestTableStaleDisabled(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "a", Subject: "ancient", Branch: "main", Created: now.Add(-365 * 24 * time.Hour)},
	}
	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 0); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "gathering dust") || strings.Contains(out, "*") {
		t.Errorf("staleDays=0 must disable nag + markers:\n%s", out)
	}
}

func TestTableAutoLabel(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		// No user label, but branch + top file → auto-label "payments: retry".
		{Index: 0, SHA: "a", Subject: "WIP on feature/payments: a1b2c3d",
			Branch: "feature/payments", TopFile: "internal/retry.go", Created: now.Add(-1 * time.Hour)},
		// Explicit user label must NOT be marked as auto.
		{Index: 1, SHA: "b", Subject: "WIP on main: x", Label: "typed name",
			Branch: "main", TopFile: "main.go", Created: now.Add(-1 * time.Hour)},
	}

	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "payments: retry ~") {
		t.Errorf("auto-labeled row should show \"payments: retry ~\":\n%s", out)
	}
	if !strings.Contains(out, "~ = auto-label") {
		t.Errorf("expected auto-label legend when an auto-label is shown:\n%s", out)
	}
	// The auto-label replaces the raw subject.
	if strings.Contains(out, "a1b2c3d") {
		t.Errorf("auto-labeled stash still shows raw subject:\n%s", out)
	}
	// The user-labeled row keeps its name and is not marked with "~".
	if !strings.Contains(out, "typed name") {
		t.Errorf("user label missing:\n%s", out)
	}
	if strings.Contains(out, "typed name ~") {
		t.Errorf("user label wrongly marked as auto-label:\n%s", out)
	}
}

func TestTableNoAutoLabelLegendWhenNone(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	// No top file anywhere → no auto-labels → no "~" legend, subjects shown.
	stashes := []model.Stash{
		{Index: 0, SHA: "a", Subject: "WIP on main: raw", Branch: "main", Created: now.Add(-1 * time.Hour)},
	}
	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "~ = auto-label") {
		t.Errorf("unexpected auto-label legend when nothing is auto-labeled:\n%s", out)
	}
	if !strings.Contains(out, "WIP on main: raw") {
		t.Errorf("row without branch+file should fall back to the subject:\n%s", out)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 60, "short"},
		{"exactlyten", 10, "exactlyten"},
		{"toolongforthis", 5, "tool…"},
		{"anything", 1, "anything"}, // max<=1 is a no-op
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestSearchResults(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	hits := []SearchHit{
		{
			Stash: model.Stash{Index: 0, SHA: "deadbeef", Label: "payments: retry", Branch: "feature/payments", Created: now.Add(-2 * time.Hour)},
			Matches: []search.Match{
				{File: "internal/git/retry.go", Kind: search.KindAdded, Line: 11, Text: "\tretryBudget := 5"},
				{File: "internal/git/retry.go", Kind: search.KindRemoved, Line: 11, Text: "\tretryBudget := 3"},
			},
		},
		{
			// No sidecar label: header falls back through Display() to the subject.
			Stash:   model.Stash{Index: 2, SHA: "cafebabe", Subject: "On main: misc", Branch: "", Created: now.Add(-5 * 24 * time.Hour)},
			Matches: []search.Match{{File: "notes.txt", Kind: search.KindContext, Line: 4, Text: "retry later"}},
		},
	}

	var buf bytes.Buffer
	n, err := SearchResults(&buf, hits, "retry", now)
	if err != nil {
		t.Fatalf("SearchResults() error = %v", err)
	}
	if n != 3 {
		t.Errorf("match count = %d, want 3", n)
	}
	out := buf.String()

	for _, want := range []string{
		"stash@{0}", "payments: retry", "2h", "feature/payments", "2 matches",
		"internal/git/retry.go:11: + \tretryBudget := 5",
		"internal/git/retry.go:11: - \tretryBudget := 3",
		"stash@{2}", "5d", "1 match",
		"notes.txt:4:   retry later", // context line uses a space symbol
	} {
		if !strings.Contains(out, want) {
			t.Errorf("search results missing %q\n---\n%s", want, out)
		}
	}
}

func TestSearchResultsNoHits(t *testing.T) {
	var buf bytes.Buffer
	n, err := SearchResults(&buf, nil, "ghost", time.Now())
	if err != nil {
		t.Fatalf("SearchResults() error = %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if !strings.Contains(buf.String(), `No stash contents matched "ghost"`) {
		t.Errorf("missing no-match line:\n%s", buf.String())
	}
}

func TestFormatTags(t *testing.T) {
	if got := FormatTags(nil); got != "" {
		t.Errorf("FormatTags(nil) = %q, want empty", got)
	}
	if got := FormatTags([]string{"wip", "hotfix"}); got != "#wip #hotfix" {
		t.Errorf("FormatTags = %q, want %q", got, "#wip #hotfix")
	}
	if got := FormatTags([]string{"wip", "", "x"}); got != "#wip #x" {
		t.Errorf("FormatTags skips blanks: got %q", got)
	}
}

func TestTableShowsTags(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "deadbeef", Subject: "WIP on main: fix retry", Branch: "main",
			Created: now.Add(-2 * time.Hour), Label: "retry fix", Tags: []string{"hotfix", "wip"}},
	}
	var buf bytes.Buffer
	if err := Table(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"retry fix", "#hotfix", "#wip"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n---\n%s", want, out)
		}
	}
}
