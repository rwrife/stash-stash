package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
)

func TestCrossRepoTableSortsOldestFirst(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	fresh := model.Stash{Index: 0, SHA: "aaa", Subject: "fresh", Branch: "main", Created: now.Add(-1 * time.Hour)}
	old := model.Stash{Index: 0, SHA: "bbb", Subject: "old", Branch: "main", Created: now.Add(-30 * 24 * time.Hour)}

	groups := []RepoGroup{
		{Repo: "repoFresh", Stashes: []model.Stash{fresh}},
		{Repo: "repoOld", Stashes: []model.Stash{old}},
	}
	var buf bytes.Buffer
	if err := CrossRepoTable(&buf, groups, now, 14); err != nil {
		t.Fatalf("CrossRepoTable: %v", err)
	}
	out := buf.String()

	// The old repo's row must appear before the fresh one (oldest first).
	iOld := strings.Index(out, "repoOld")
	iFresh := strings.Index(out, "repoFresh")
	if iOld < 0 || iFresh < 0 || iOld > iFresh {
		t.Fatalf("expected repoOld before repoFresh, got:\n%s", out)
	}
	// The 30-day stash is dusty; banner should mention gathering dust.
	if !strings.Contains(out, "gathering dust") {
		t.Errorf("expected dust banner, got:\n%s", out)
	}
	// REPO column header present.
	if !strings.Contains(out, "REPO") {
		t.Errorf("expected REPO column, got:\n%s", out)
	}
}

func TestCrossRepoTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := CrossRepoTable(&buf, nil, time.Now(), 14); err != nil {
		t.Fatalf("CrossRepoTable: %v", err)
	}
	if !strings.Contains(buf.String(), "No stashes found") {
		t.Errorf("expected empty message, got %q", buf.String())
	}
}
