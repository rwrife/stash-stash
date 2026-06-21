package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
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
