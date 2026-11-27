package meta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Bundle is the portable, SHA-keyed metadata document used by export/import.
type Bundle struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// MergeResult summarizes the metadata merge without hiding entries that were
// skipped because they are unknown locally or conflict with existing values.
type MergeResult struct {
	Added       int
	Merged      int
	Overwritten int
	Unknown     []string
	Conflicts   []string
}

// EncodeBundle returns stable, human-diffable JSON for the store.
func (s *Store) EncodeBundle() ([]byte, error) {
	if s == nil {
		return nil, errors.New("meta: export on nil store")
	}
	version := s.Version
	if version == 0 {
		version = schemaVersion
	}
	entries := make(map[string]Entry, len(s.Entries))
	for sha, entry := range s.Entries {
		entry.Tags = normalizeTags(entry.Tags)
		entries[sha] = entry
	}
	data, err := json.MarshalIndent(Bundle{Version: version, Entries: entries}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode metadata bundle: %w", err)
	}
	return append(data, '\n'), nil
}

// DecodeBundle validates one complete bundle document.
func DecodeBundle(data []byte) (*Bundle, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("parse metadata bundle: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("parse metadata bundle: multiple JSON values")
		}
		return nil, fmt.Errorf("parse metadata bundle: %w", err)
	}
	if bundle.Version != schemaVersion {
		return nil, fmt.Errorf("unsupported metadata bundle version %d (supported: %d)", bundle.Version, schemaVersion)
	}
	if bundle.Entries == nil {
		bundle.Entries = map[string]Entry{}
	}
	for sha, entry := range bundle.Entries {
		if sha == "" {
			return nil, errors.New("parse metadata bundle: entries must have a SHA key")
		}
		entry.Tags = normalizeTags(entry.Tags)
		bundle.Entries[sha] = entry
	}
	return &bundle, nil
}

// MergeBundle applies entries for live SHAs. Conflicting non-empty labels or
// notes are skipped unless force is true; tags merge as a sorted union.
func (s *Store) MergeBundle(bundle *Bundle, liveSHAs map[string]struct{}, force bool) (MergeResult, error) {
	if s == nil {
		return MergeResult{}, errors.New("meta: merge into nil store")
	}
	if bundle == nil || bundle.Version != schemaVersion {
		return MergeResult{}, errors.New("meta: invalid metadata bundle")
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}

	result := MergeResult{}
	for sha, incoming := range bundle.Entries {
		if _, ok := liveSHAs[sha]; !ok {
			result.Unknown = append(result.Unknown, sha)
			continue
		}
		incoming.Tags = normalizeTags(incoming.Tags)
		if emptyEntry(incoming) {
			continue
		}

		current, exists := s.Entries[sha]
		if !exists {
			s.Entries[sha] = incoming
			result.Added++
			continue
		}

		conflict := current.Label != "" && incoming.Label != "" && current.Label != incoming.Label ||
			current.Note != "" && incoming.Note != "" && current.Note != incoming.Note
		if conflict && !force {
			result.Conflicts = append(result.Conflicts, sha)
			continue
		}
		if conflict {
			s.Entries[sha] = incoming
			result.Overwritten++
			continue
		}

		merged := current
		merged.Tags = normalizeTags(append(append([]string(nil), current.Tags...), incoming.Tags...))
		if merged.Label == "" {
			merged.Label = incoming.Label
		}
		if merged.Note == "" {
			merged.Note = incoming.Note
		}
		if entryMetadataEqual(current, merged) {
			continue
		}
		merged.UpdatedAt = time.Now().UTC()
		s.Entries[sha] = merged
		result.Merged++
	}
	sort.Strings(result.Unknown)
	sort.Strings(result.Conflicts)
	return result, nil
}

func emptyEntry(entry Entry) bool {
	return entry.Label == "" && len(entry.Tags) == 0 && entry.Note == ""
}

func entryMetadataEqual(a, b Entry) bool {
	if a.Label != b.Label || a.Note != b.Note || len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}
