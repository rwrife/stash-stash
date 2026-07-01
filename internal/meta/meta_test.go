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

// --- issue #21: tags & filtering ---

func TestSlugTag(t *testing.T) {
	cases := map[string]string{
		"WIP":            "wip",
		"  experiment  ": "experiment",
		"Hot Fix!":       "hot-fix",
		"feature/login":  "feature-login",
		"a___b...c":      "a-b-c",
		"---":            "",
		"":               "",
		"Café":           "caf", // non-ASCII dropped, trailing hyphen trimmed
		"  ":             "",
		"v2.0":           "v2-0",
	}
	for in, want := range cases {
		if got := SlugTag(in); got != want {
			t.Errorf("SlugTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetTagsNormalizesAndRoundTrips(t *testing.T) {
	s := tempStore(t)
	// Mixed case, dupes, junk, and an order that must come back sorted.
	s.SetTags("sha-1", []string{"WIP", "experiment", "wip", "Hot Fix", "  ", "---"})
	got := s.Tags("sha-1")
	want := []string{"experiment", "hot-fix", "wip"}
	if !equalStrings(got, want) {
		t.Fatalf("Tags after SetTags = %v, want %v", got, want)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := loadFrom(s.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Tags("sha-1"); !equalStrings(got, want) {
		t.Errorf("Tags after reload = %v, want %v", got, want)
	}
}

func TestTagsReturnedCopyIsIndependent(t *testing.T) {
	s := tempStore(t)
	s.SetTags("sha-1", []string{"wip", "hotfix"})
	got := s.Tags("sha-1")
	got[0] = "mutated"
	if again := s.Tags("sha-1"); again[0] == "mutated" {
		t.Error("Tags returned a slice aliasing internal storage")
	}
}

func TestAddTagsUnions(t *testing.T) {
	s := tempStore(t)
	s.SetTags("sha-1", []string{"wip"})
	s.AddTags("sha-1", []string{"hotfix", "wip", "Review"})
	got := s.Tags("sha-1")
	want := []string{"hotfix", "review", "wip"}
	if !equalStrings(got, want) {
		t.Errorf("Tags after AddTags = %v, want %v", got, want)
	}
	// Adding only junk is a no-op and must not create an entry.
	s2 := tempStore(t)
	s2.AddTags("sha-x", []string{"---", "  "})
	if len(s2.Entries) != 0 {
		t.Errorf("AddTags of only-junk created %d entries, want 0", len(s2.Entries))
	}
}

func TestRemoveTag(t *testing.T) {
	s := tempStore(t)
	s.SetTags("sha-1", []string{"wip", "hotfix", "review"})
	if !s.RemoveTag("sha-1", "HotFix") { // slugifies to hotfix
		t.Error("RemoveTag(hotfix) returned false")
	}
	if got, want := s.Tags("sha-1"), []string{"review", "wip"}; !equalStrings(got, want) {
		t.Errorf("Tags after RemoveTag = %v, want %v", got, want)
	}
	if s.RemoveTag("sha-1", "nope") {
		t.Error("RemoveTag of an absent tag returned true")
	}
	// Removing the last tags clears the entry entirely (no other metadata).
	s.RemoveTag("sha-1", "review")
	s.RemoveTag("sha-1", "wip")
	if _, ok := s.Entries["sha-1"]; ok {
		t.Error("entry survived removing its last tag with no other metadata")
	}
}

func TestSetTagsEmptyClearsButKeepsLabel(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha-1", "keep me")
	s.SetTags("sha-1", []string{"wip"})
	s.SetTags("sha-1", nil) // clear tags only
	if got := s.Tags("sha-1"); got != nil {
		t.Errorf("Tags after clear = %v, want nil", got)
	}
	if lbl, ok := s.Label("sha-1"); !ok || lbl != "keep me" {
		t.Errorf("label lost when clearing tags: %q ok=%v", lbl, ok)
	}
}

func TestHasAllTags(t *testing.T) {
	s := tempStore(t)
	s.SetTags("sha-1", []string{"wip", "hotfix"})
	if !s.HasAllTags("sha-1", nil) {
		t.Error("empty want should match (no filter)")
	}
	if !s.HasAllTags("sha-1", []string{"WIP"}) {
		t.Error("single matching tag (case-insensitive) should match")
	}
	if !s.HasAllTags("sha-1", []string{"wip", "HotFix"}) {
		t.Error("AND of present tags should match")
	}
	if s.HasAllTags("sha-1", []string{"wip", "review"}) {
		t.Error("AND with an absent tag should not match")
	}
	if s.HasAllTags("missing", []string{"wip"}) {
		t.Error("unknown SHA should not match a non-empty filter")
	}
}

func TestSplitTags(t *testing.T) {
	got := SplitTags(" wip, Hot Fix ,,wip , --- ")
	want := []string{"hot-fix", "wip"}
	if !equalStrings(got, want) {
		t.Errorf("SplitTags = %v, want %v", got, want)
	}
	if SplitTags("   ") != nil {
		t.Error("SplitTags of blank should be nil")
	}
}

// equalStrings compares two string slices element-wise (both nil counts equal).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
