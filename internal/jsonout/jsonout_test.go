package jsonout

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/rwrife/stash-stash/internal/model"
)

func TestWriteSchema(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "deadbeef", Subject: "WIP on main: fix retry", Branch: "main",
			Created:  now.Add(-2 * time.Hour),
			Diffstat: model.Diffstat{Added: 12, Deleted: 3, Files: 2}},
		{Index: 1, SHA: "cafebabe", Subject: "On feature/x: half-done", Branch: "feature/x",
			Label:   "payments: retry fix",
			Created: now.Add(-30 * 24 * time.Hour)}, // > 2x of 14 => ancient/dusty
	}

	var buf bytes.Buffer
	if err := Write(&buf, stashes, now, 14); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got Output
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if got.StaleDays != 14 {
		t.Errorf("StaleDays = %d, want 14", got.StaleDays)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	if got.DustyCount != 1 {
		t.Errorf("DustyCount = %d, want 1", got.DustyCount)
	}
	if len(got.Stashes) != 2 {
		t.Fatalf("len(Stashes) = %d, want 2", len(got.Stashes))
	}

	fresh := got.Stashes[0]
	if fresh.Ref != "stash@{0}" || fresh.Stale || fresh.Staleness != "fresh" {
		t.Errorf("row0 unexpected: %+v", fresh)
	}
	if fresh.Diffstat.Added != 12 || fresh.Diffstat.Deleted != 3 || fresh.Diffstat.Files != 2 {
		t.Errorf("row0 diffstat = %+v", fresh.Diffstat)
	}
	if fresh.AgeSeconds != int64((2 * time.Hour).Seconds()) {
		t.Errorf("row0 AgeSeconds = %d, want %d", fresh.AgeSeconds, int64((2 * time.Hour).Seconds()))
	}

	dusty := got.Stashes[1]
	if !dusty.Stale || dusty.Staleness != "ancient" {
		t.Errorf("row1 should be ancient+stale: %+v", dusty)
	}
	if dusty.Label != "payments: retry fix" {
		t.Errorf("row1 label = %q", dusty.Label)
	}
}

func TestWriteStaleDisabled(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	stashes := []model.Stash{
		{Index: 0, SHA: "a", Subject: "old", Created: now.Add(-365 * 24 * time.Hour)},
	}
	var buf bytes.Buffer
	if err := Write(&buf, stashes, now, 0); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var got Output
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.DustyCount != 0 {
		t.Errorf("DustyCount = %d, want 0 (staleness disabled)", got.DustyCount)
	}
	if got.Stashes[0].Stale {
		t.Errorf("stash marked stale despite staleDays=0")
	}
}

func TestWriteEmpty(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	if err := Write(&buf, nil, now, 14); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var got Output
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Count != 0 || len(got.Stashes) != 0 {
		t.Errorf("expected empty stash list, got %+v", got)
	}
	// Stashes must serialize as [] not null for predictable scripting.
	if !bytes.Contains(buf.Bytes(), []byte(`"stashes": []`)) {
		t.Errorf("empty stashes should render as [], got:\n%s", buf.String())
	}
}
