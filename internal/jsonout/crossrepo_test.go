package jsonout

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
)

func TestWriteCrossRepoAggregates(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	old := model.Stash{Index: 0, SHA: "bbb", Subject: "old", Branch: "main", Created: now.Add(-30 * 24 * time.Hour)}
	fresh := model.Stash{Index: 0, SHA: "aaa", Subject: "fresh", Branch: "main", Created: now.Add(-1 * time.Hour)}

	groups := []RepoGroup{
		{Repo: "repoOld", Stashes: []model.Stash{old}},
		{Repo: "repoFresh", Stashes: []model.Stash{fresh}},
	}
	var buf bytes.Buffer
	if err := WriteCrossRepo(&buf, groups, now, 14); err != nil {
		t.Fatalf("WriteCrossRepo: %v", err)
	}

	var out CrossRepoOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RepoCount != 2 {
		t.Errorf("RepoCount = %d, want 2", out.RepoCount)
	}
	if out.Count != 2 {
		t.Errorf("Count = %d, want 2", out.Count)
	}
	if out.DustyCount != 1 {
		t.Errorf("DustyCount = %d, want 1 (only the 30-day stash)", out.DustyCount)
	}
	if len(out.Repos) != 2 || out.Repos[0].Repo != "repoOld" {
		t.Fatalf("unexpected repos: %+v", out.Repos)
	}
	if out.Repos[0].DustyCount != 1 || out.Repos[1].DustyCount != 0 {
		t.Errorf("per-repo dusty counts wrong: %+v", out.Repos)
	}
	// Tags must render as [] not null.
	if out.Repos[0].Stashes[0].Tags == nil {
		t.Error("expected non-nil Tags slice")
	}
}
