package meta

import (
	"strings"
	"testing"
)

func TestBundleExportImportRoundTrip(t *testing.T) {
	source := &Store{Version: schemaVersion, Entries: map[string]Entry{
		"known": {Label: "payments: retry", Tags: []string{"wip", "hotfix"}, Note: "blocked on API"},
	}}
	data, err := source.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := DecodeBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	target := &Store{Version: schemaVersion, Entries: map[string]Entry{}}
	result, err := target.MergeBundle(bundle, map[string]struct{}{"known": {}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || len(result.Unknown) != 0 {
		t.Fatalf("merge result = %+v, want one added entry", result)
	}
	if got := target.Entries["known"]; got.Label != "payments: retry" || got.Note != "blocked on API" || strings.Join(got.Tags, ",") != "hotfix,wip" {
		t.Fatalf("restored entry = %+v", got)
	}
}

func TestMergeBundleFillsMissingMetadataAndUnionsTags(t *testing.T) {
	store := &Store{Version: schemaVersion, Entries: map[string]Entry{
		"known": {Label: "local label", Tags: []string{"local"}},
	}}
	bundle := &Bundle{Version: schemaVersion, Entries: map[string]Entry{
		"known": {Label: "local label", Tags: []string{"shared"}, Note: "imported note"},
	}}
	result, err := store.MergeBundle(bundle, map[string]struct{}{"known": {}}, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := store.Entries["known"]
	if result.Merged != 1 || entry.Label != "local label" || entry.Note != "imported note" || strings.Join(entry.Tags, ",") != "local,shared" {
		t.Fatalf("merge result = %+v, entry = %+v", result, entry)
	}
}

func TestMergeBundleSkipsConflictUnlessForced(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip", true: "force"}[force], func(t *testing.T) {
			store := &Store{Version: schemaVersion, Entries: map[string]Entry{
				"known": {Label: "local"},
			}}
			bundle := &Bundle{Version: schemaVersion, Entries: map[string]Entry{
				"known": {Label: "imported", Note: "new note"},
			}}
			result, err := store.MergeBundle(bundle, map[string]struct{}{"known": {}}, force)
			if err != nil {
				t.Fatal(err)
			}
			if force {
				if result.Overwritten != 1 || store.Entries["known"].Label != "imported" {
					t.Fatalf("forced merge = %+v, entry = %+v", result, store.Entries["known"])
				}
			} else if len(result.Conflicts) != 1 || store.Entries["known"].Label != "local" {
				t.Fatalf("non-forced merge = %+v, entry = %+v", result, store.Entries["known"])
			}
		})
	}
}

func TestMergeBundleReportsUnknownSHAs(t *testing.T) {
	store := &Store{Version: schemaVersion, Entries: map[string]Entry{}}
	bundle := &Bundle{Version: schemaVersion, Entries: map[string]Entry{
		"missing": {Label: "not local"},
	}}
	result, err := store.MergeBundle(bundle, map[string]struct{}{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Unknown) != 1 || result.Unknown[0] != "missing" || len(store.Entries) != 0 {
		t.Fatalf("merge result = %+v, entries = %+v", result, store.Entries)
	}
}

func TestEncodeBundleIsStable(t *testing.T) {
	a := &Store{Version: schemaVersion, Entries: map[string]Entry{
		"b": {Label: "second"}, "a": {Label: "first"},
	}}
	b := &Store{Version: schemaVersion, Entries: map[string]Entry{
		"a": {Label: "first"}, "b": {Label: "second"},
	}}
	first, err := a.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("export differs for equivalent metadata:\n%s\n%s", first, second)
	}
}
