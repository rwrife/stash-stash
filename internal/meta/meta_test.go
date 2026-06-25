package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempStore returns a Store bound to a path inside a fresh temp dir (no real
// git repo needed), exercising the loadFrom/Save core directly.
func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := loadFrom(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("loadFrom on empty dir: %v", err)
	}
	return s
}

func TestLoadMissingFileIsEmptyStore(t *testing.T) {
	s := tempStore(t)
	if s == nil {
		t.Fatal("loadFrom returned nil store")
	}
	if len(s.Entries) != 0 {
		t.Errorf("fresh store has %d entries, want 0", len(s.Entries))
	}
	if s.Version != schemaVersion {
		t.Errorf("version = %d, want %d", s.Version, schemaVersion)
	}
	if _, ok := s.Label("anything"); ok {
		t.Error("empty store reported a label")
	}
}

func TestSetLabelAndSaveRoundTrip(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha-abc", "payments: retry fix")
	s.SetLabel("sha-def", "  spaced  ") // trimmed on store
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file should now exist and be valid JSON we can reload.
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	reloaded, err := loadFrom(s.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, ok := reloaded.Label("sha-abc"); !ok || got != "payments: retry fix" {
		t.Errorf("reloaded label[sha-abc] = %q (ok=%v), want %q", got, ok, "payments: retry fix")
	}
	if got, _ := reloaded.Label("sha-def"); got != "spaced" {
		t.Errorf("label[sha-def] = %q, want trimmed %q", got, "spaced")
	}
}

func TestSetEmptyLabelRemovesEntry(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha-x", "temporary")
	if _, ok := s.Label("sha-x"); !ok {
		t.Fatal("label not set")
	}
	s.SetLabel("sha-x", "   ") // trims to empty → removal
	if _, ok := s.Label("sha-x"); ok {
		t.Error("clearing the label left an entry behind")
	}
	if _, present := s.Entries["sha-x"]; present {
		t.Error("entry map still contains the cleared SHA")
	}
}

func TestSetLabelEmptySHAIgnored(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("", "ghost")
	if len(s.Entries) != 0 {
		t.Errorf("empty SHA created an entry: %+v", s.Entries)
	}
}

func TestPruneDropsDeadSHAs(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("live", "keep me")
	s.SetLabel("dead", "drop me")

	removed := s.Prune(map[string]struct{}{"live": {}})
	if removed != 1 {
		t.Errorf("Prune removed %d, want 1", removed)
	}
	if _, ok := s.Label("live"); !ok {
		t.Error("Prune dropped a live entry")
	}
	if _, ok := s.Label("dead"); ok {
		t.Error("Prune kept a dead entry")
	}
}

func TestSaveIsAtomicValidJSON(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha", "name")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// Ends with a trailing newline and mentions our fields.
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("sidecar missing trailing newline")
	}
	for _, want := range []string{`"version"`, `"entries"`, `"label"`, "name"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("sidecar JSON missing %q:\n%s", want, data)
		}
	}
	// The unexported path must never be serialized.
	if strings.Contains(string(data), "path") {
		t.Errorf("sidecar leaked the path field:\n%s", data)
	}
}

func TestLoadToleratesEmptyAndGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)

	// Empty file → empty store, no error.
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := loadFrom(path); err != nil || len(s.Entries) != 0 {
		t.Errorf("empty file load: store=%+v err=%v", s, err)
	}

	// Garbage → parse error surfaced.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFrom(path); err == nil {
		t.Error("garbage sidecar did not error")
	}
}

func TestSaveWithoutPathErrors(t *testing.T) {
	s := &Store{Entries: map[string]Entry{}}
	if err := s.Save(); err == nil {
		t.Error("Save with no path should error")
	}
}

// --- issue #10: orphan detection + Remove for `stash-stash doctor` ---------

func TestOrphansReportsOnlyUnknownSHAs(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("live", "on the stack")
	s.SetLabel("recoverable", "dangling but rescuable")
	s.SetLabel("gone", "truly lost work")

	// "live" and "recoverable" are accounted for; only "gone" is an orphan.
	known := map[string]struct{}{"live": {}, "recoverable": {}}
	orphans := s.Orphans(known)
	if len(orphans) != 1 {
		t.Fatalf("Orphans len = %d, want 1 (%+v)", len(orphans), orphans)
	}
	if orphans[0].SHA != "gone" {
		t.Errorf("orphan SHA = %q, want gone", orphans[0].SHA)
	}
	if orphans[0].Entry.Label != "truly lost work" {
		t.Errorf("orphan label = %q, want %q", orphans[0].Entry.Label, "truly lost work")
	}
}

func TestOrphansSortedBySHA(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("ccc", "c")
	s.SetLabel("aaa", "a")
	s.SetLabel("bbb", "b")
	orphans := s.Orphans(map[string]struct{}{}) // nothing known → all orphans
	got := []string{orphans[0].SHA, orphans[1].SHA, orphans[2].SHA}
	want := []string{"aaa", "bbb", "ccc"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orphans order = %v, want %v", got, want)
		}
	}
}

func TestOrphansDoesNotMutateStore(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("gone", "lost")
	_ = s.Orphans(map[string]struct{}{}) // reports it, must not delete it
	if _, ok := s.Label("gone"); !ok {
		t.Error("Orphans mutated the store (entry was removed)")
	}
}

func TestRemoveDeletesEntry(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha", "bye")
	if !s.Remove("sha") {
		t.Fatal("Remove returned false for an existing entry")
	}
	if _, ok := s.Label("sha"); ok {
		t.Error("entry still present after Remove")
	}
	// Removing again (or an unknown SHA) is a no-op returning false.
	if s.Remove("sha") {
		t.Error("Remove of a missing SHA returned true")
	}
	if s.Remove("") {
		t.Error("Remove of an empty SHA returned true")
	}
}
