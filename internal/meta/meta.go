// Package meta manages stash-stash's sidecar metadata file,
// `.git/stash-stash.json`. It maps a stash's *content* SHA to the
// human-readable bits git can't store well: a label (and, in later milestones,
// tags and notes).
//
// Keying by content SHA — not by the volatile stash@{N} index — is the whole
// point: labels must survive `git stash pop`/`push` reshuffling the stack. The
// SHA of a stash commit is stable for that stash's lifetime, so a label written
// against it stays attached no matter where the entry drifts in the list.
//
// The store is plain stdlib JSON, loaded and saved atomically, with no DB and
// no config server (see PLAN.md "Out of scope"). Mutating git operations (M5)
// will call Save alongside the git op so the sidecar never drifts from reality.
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// fileName is the sidecar's basename inside the repo's git dir.
const fileName = "stash-stash.json"

// schemaVersion is bumped if the on-disk shape changes incompatibly. Readers
// tolerate unknown fields, so additive changes don't require a bump.
const schemaVersion = 1

// Entry is the per-stash metadata stash-stash persists. Only Label is used at
// M4; Tags and Note are reserved for later milestones but defined now so the
// JSON shape is forward-compatible and we don't need a schema bump later.
type Entry struct {
	// Label is the human-friendly name for the stash, e.g. "payments: retry fix".
	Label string `json:"label,omitempty"`

	// Tags are short classifiers (e.g. "wip", "experiment"). Reserved for a
	// later milestone; persisted now for forward compatibility.
	Tags []string `json:"tags,omitempty"`

	// Note is an optional multi-line "why". Reserved for a later milestone.
	Note string `json:"note,omitempty"`

	// UpdatedAt records when this entry was last written, in UTC RFC3339.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// Store is the deserialized sidecar: a schema version plus a SHA→Entry map.
//
// The zero value is not usable; construct via Load (which returns an empty,
// ready store when the file is absent) so the map and path are initialized.
type Store struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`

	// path is the absolute file path this store loads from / saves to. It is
	// unexported so it never leaks into the JSON.
	path string
}

// gitDirFunc resolves the repository's git directory (the place the sidecar
// lives). It is a package var so tests can point it at a temp dir without a
// real repo. The default shells out to `git rev-parse --absolute-git-dir`,
// which correctly handles worktrees and `.git` files.
var gitDirFunc = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--absolute-git-dir")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isNotARepo(stderr.Bytes()) {
			return "", ErrNotARepo
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git rev-parse --absolute-git-dir: %s", msg)
		}
		return "", fmt.Errorf("git rev-parse --absolute-git-dir: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ErrNotARepo mirrors git.ErrNotARepo for callers that resolve the sidecar
// path before they have any stashes. It signals "outside a work tree", a
// friendly/expected condition rather than a crash.
var ErrNotARepo = errors.New("not a git repository")

// Load reads the sidecar for the current repository. A missing file is not an
// error: it returns an empty, ready-to-use Store bound to the correct path so
// a subsequent Save creates it. It returns ErrNotARepo outside a work tree.
func Load(ctx context.Context) (*Store, error) {
	gitDir, err := gitDirFunc(ctx)
	if err != nil {
		return nil, err
	}
	return loadFrom(filepath.Join(gitDir, fileName))
}

// loadFrom is the path-explicit core of Load, factored out for testing.
func loadFrom(path string) (*Store, error) {
	s := &Store{Version: schemaVersion, Entries: map[string]Entry{}, path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil // absent sidecar → empty store
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Tolerate an empty file (e.g. a truncated prior write) as an empty store
	// rather than a JSON syntax error.
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	s.path = path
	if s.Version == 0 {
		s.Version = schemaVersion
	}
	return s, nil
}

// Label returns the stored label for a stash content SHA, and whether one was
// set. An empty SHA never matches.
func (s *Store) Label(sha string) (string, bool) {
	if s == nil || sha == "" {
		return "", false
	}
	e, ok := s.Entries[sha]
	if !ok {
		return "", false
	}
	return e.Label, e.Label != ""
}

// SetLabel records (or, with an empty label, clears) the label for a stash
// content SHA and stamps UpdatedAt. It mutates the in-memory store only;
// callers must Save to persist. A no-op (empty SHA) is ignored.
//
// Setting an empty label removes the entry entirely when it carries no other
// metadata, keeping the sidecar tidy.
func (s *Store) SetLabel(sha, label string) {
	if s == nil || sha == "" {
		return
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	label = strings.TrimSpace(label)
	e := s.Entries[sha]
	e.Label = label
	e.UpdatedAt = time.Now().UTC()
	if e.Label == "" && len(e.Tags) == 0 && e.Note == "" {
		delete(s.Entries, sha)
		return
	}
	s.Entries[sha] = e
}

// Prune drops entries whose SHA is not in the supplied set of live stash SHAs.
// This is how stale metadata (for stashes that were dropped/popped outside
// stash-stash) gets garbage-collected on load. It returns the number removed.
func (s *Store) Prune(liveSHAs map[string]struct{}) int {
	if s == nil {
		return 0
	}
	removed := 0
	for sha := range s.Entries {
		if _, ok := liveSHAs[sha]; !ok {
			delete(s.Entries, sha)
			removed++
		}
	}
	return removed
}

// Save writes the store to its bound path atomically (temp file + rename) so a
// crash mid-write can't corrupt an existing sidecar. The git dir always exists
// by the time we have a Store, so no directory creation is needed.
func (s *Store) Save() error {
	if s == nil {
		return errors.New("meta: Save on nil store")
	}
	if s.path == "" {
		return errors.New("meta: Save with no path (store not from Load)")
	}
	if s.Version == 0 {
		s.Version = schemaVersion
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sidecar: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, fileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp sidecar: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp sidecar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp sidecar: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace sidecar: %w", err)
	}
	return nil
}

// Path returns the absolute sidecar path the store is bound to (useful for
// diagnostics and tests).
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// isNotARepo reports whether git's stderr indicates we're outside a work tree.
// Mirrors the check in internal/git so behavior is consistent.
func isNotARepo(stderr []byte) bool {
	msg := strings.ToLower(string(stderr))
	return strings.Contains(msg, "not a git repository") ||
		strings.Contains(msg, "this operation must be run in a work tree")
}
