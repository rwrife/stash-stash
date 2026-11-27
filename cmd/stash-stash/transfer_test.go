package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwrife/stash-stash/internal/meta"
	"github.com/rwrife/stash-stash/internal/model"
)

func TestExportWipeAndImportRoundTrip(t *testing.T) {
	dir := tempRepoSidecar(t)
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return []model.Stash{{SHA: "known-sha"}}, nil
	}
	t.Cleanup(func() { gitList = originalList })

	store, err := metaLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store.SetLabel("known-sha", "payments: retry")
	store.SetTags("known-sha", []string{"wip", "hotfix"})
	store.SetNote("known-sha", "blocked on API")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(dir, "handoff.json")
	var stdout, stderr bytes.Buffer
	if code := runExport([]string{"--out", bundlePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("export exit = %d, stderr = %q", code, stderr.String())
	}
	if err := os.Remove(filepath.Join(dir, ".git", "stash-stash.json")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runImport([]string{bundlePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("import exit = %d, stderr = %q", code, stderr.String())
	}
	restored, err := metaLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := restored.Entries["known-sha"]
	if entry.Label != "payments: retry" || entry.Note != "blocked on API" || strings.Join(entry.Tags, ",") != "hotfix,wip" {
		t.Fatalf("restored entry = %+v", entry)
	}
}

func TestImportSkipsConflictUnlessForce(t *testing.T) {
	dir := tempRepoSidecar(t)
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return []model.Stash{{SHA: "known-sha"}}, nil
	}
	t.Cleanup(func() { gitList = originalList })

	local, err := metaLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	local.SetLabel("known-sha", "local label")
	if err := local.Save(); err != nil {
		t.Fatal(err)
	}
	bundle, err := (&meta.Store{Version: 1, Entries: map[string]meta.Entry{
		"known-sha": {Label: "imported label"},
	}}).EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(bundlePath, bundle, 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{bundlePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("non-forced import exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Skipped conflicting SHA known-sha") {
		t.Fatalf("stdout = %q, want conflict report", stdout.String())
	}
	local, err = metaLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Entries["known-sha"].Label; got != "local label" {
		t.Fatalf("label after skipped import = %q, want local label", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runImport([]string{bundlePath, "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("forced import exit = %d, stderr = %q", code, stderr.String())
	}
	local, err = metaLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Entries["known-sha"].Label; got != "imported label" {
		t.Fatalf("label after forced import = %q, want imported label", got)
	}
}

func TestImportReportsUnknownSHAs(t *testing.T) {
	dir := tempRepoSidecar(t)
	originalList := gitList
	gitList = func(context.Context) ([]model.Stash, error) {
		return []model.Stash{{SHA: "known-sha"}}, nil
	}
	t.Cleanup(func() { gitList = originalList })
	bundle, err := (&meta.Store{Version: 1, Entries: map[string]meta.Entry{
		"unknown-sha": {Label: "unknown"},
	}}).EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(bundlePath, bundle, 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{bundlePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("import exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Skipped unknown SHA unknown-sha") {
		t.Fatalf("stdout = %q, want unknown SHA report", stdout.String())
	}
	if sidecar := readSidecar(t, dir); sidecar != "" {
		t.Fatalf("sidecar changed for unknown SHA: %s", sidecar)
	}
}
