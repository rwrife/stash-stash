package meta

import "testing"

func TestSetNoteAndSaveRoundTrip(t *testing.T) {
	s := tempStore(t)
	multi := "blocked on the API change\nrevisit after #42"
	s.SetNote("sha-abc", multi)
	s.SetNote("sha-def", "  spaced note  ") // surrounding ws trimmed
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := loadFrom(s.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, ok := reloaded.Note("sha-abc"); !ok || got != multi {
		t.Errorf("reloaded note[sha-abc] = %q (ok=%v), want %q", got, ok, multi)
	}
	if got, _ := reloaded.Note("sha-def"); got != "spaced note" {
		t.Errorf("note[sha-def] = %q, want trimmed %q", got, "spaced note")
	}
}

// A note must survive a reorder: keying by content SHA (not stash@{N}) is the
// whole point. We simulate reorder by reloading and checking the SHA still maps.
func TestNoteSurvivesReorderBySHA(t *testing.T) {
	s := tempStore(t)
	s.SetNote("content-sha-1", "why I stashed this")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := loadFrom(s.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, ok := reloaded.Note("content-sha-1"); !ok || got != "why I stashed this" {
		t.Errorf("note lost across reload/reorder: got %q ok=%v", got, ok)
	}
}

func TestSetEmptyNoteRemovesEntry(t *testing.T) {
	s := tempStore(t)
	s.SetNote("sha-x", "temporary")
	if _, ok := s.Note("sha-x"); !ok {
		t.Fatal("note not set")
	}
	s.SetNote("sha-x", "   ") // trims to empty → removal
	if _, ok := s.Note("sha-x"); ok {
		t.Error("clearing the note left an entry behind")
	}
	if _, present := s.Entries["sha-x"]; present {
		t.Error("entry map still contains the cleared SHA")
	}
}

// Clearing a note must NOT delete an entry that still carries a label.
func TestClearNoteKeepsEntryWithLabel(t *testing.T) {
	s := tempStore(t)
	s.SetLabel("sha-y", "keep me")
	s.SetNote("sha-y", "some why")
	s.SetNote("sha-y", "")
	if _, ok := s.Note("sha-y"); ok {
		t.Error("note should be cleared")
	}
	if got, ok := s.Label("sha-y"); !ok || got != "keep me" {
		t.Errorf("label lost when clearing note: got %q ok=%v", got, ok)
	}
}

func TestSetNoteEmptySHAIgnored(t *testing.T) {
	s := tempStore(t)
	s.SetNote("", "ghost")
	if len(s.Entries) != 0 {
		t.Errorf("empty SHA created %d entries, want 0", len(s.Entries))
	}
}
