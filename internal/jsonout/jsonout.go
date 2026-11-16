// Package jsonout renders the stash list as machine-readable JSON for the
// `--json` flag (M6), so stash-stash composes with jq, scripts, and CI instead
// of forcing humans to scrape the table. The schema is intentionally small and
// stable: each stash carries its ref, SHA, label/subject, branch, age, a
// staleness bucket (relative to the active --stale-days threshold), and its
// diffstat. A top-level summary reports how many stashes are "gathering dust"
// so a script can gate on it without re-deriving the rule.
package jsonout

import (
	"encoding/json"
	"io"
	"time"

	"github.com/rwrife/stash-stash/internal/age"
	"github.com/rwrife/stash-stash/internal/model"
)

// Diffstat is the JSON shape of a stash's change summary. Fields mirror
// model.Diffstat but are explicitly tagged for a stable wire format.
type Diffstat struct {
	Added   int  `json:"added"`
	Deleted int  `json:"deleted"`
	Files   int  `json:"files"`
	Binary  bool `json:"binary"`
}

// Stash is the JSON shape of a single stash entry. AgeSeconds is the raw age so
// consumers can apply their own thresholds; Age is the same humanized token the
// table shows; Stale is true when the stash counts as gathering dust under the
// active threshold; Staleness is the bucket name ("fresh"/"aging"/"stale"/
// "ancient"). Display is the label stash-stash would show (user label, else the
// auto-derived "<area>: <hint>", else the subject) and LabelSource says which
// of those it is ("user"/"auto"/"subject"); AutoLabel is always the derived
// guess (when one can be built) regardless of whether a user label overrides it,
// so scripts can surface or apply it. Tags is the stash's sidecar classifiers
// (issue #21), always present (an empty array when none) so consumers can index
// it without a nil check, and `--tag` filtering applies to this output too.
type Stash struct {
	Index       int       `json:"index"`
	Ref         string    `json:"ref"`
	SHA         string    `json:"sha"`
	Label       string    `json:"label,omitempty"`
	AutoLabel   string    `json:"auto_label,omitempty"`
	Tags        []string  `json:"tags"`
	Note        string    `json:"note,omitempty"`
	Display     string    `json:"display"`
	LabelSource string    `json:"label_source"`
	Subject     string    `json:"subject"`
	Branch      string    `json:"branch,omitempty"`
	TopFile     string    `json:"top_file,omitempty"`
	Created     time.Time `json:"created"`
	Age         string    `json:"age"`
	AgeSeconds  int64     `json:"age_seconds"`
	Staleness   string    `json:"staleness"`
	Stale       bool      `json:"stale"`
	Diffstat    Diffstat  `json:"diffstat"`
}

// Output is the top-level JSON document: the active stale threshold, a summary,
// and the list of stashes.
type Output struct {
	StaleDays  int     `json:"stale_days"`
	Count      int     `json:"count"`
	DustyCount int     `json:"dusty_count"`
	Stashes    []Stash `json:"stashes"`
}

// Write renders the stashes as indented JSON to w. now is used to compute ages
// and staleness; staleDays is the active --stale-days threshold (0 disables
// staleness, so nothing is reported as dusty).
func Write(w io.Writer, stashes []model.Stash, now time.Time, staleDays int) error {
	out := Output{
		StaleDays: staleDays,
		Count:     len(stashes),
		Stashes:   make([]Stash, 0, len(stashes)),
	}

	for _, s := range stashes {
		bucket := age.Classify(s.Created, now, staleDays)
		ageSecs := int64(0)
		if !s.Created.IsZero() {
			if d := now.Sub(s.Created); d > 0 {
				ageSecs = int64(d.Seconds())
			}
		}
		dusty := bucket.Dusty()
		if dusty {
			out.DustyCount++
		}
		display, srcKind := s.DisplaySource()
		out.Stashes = append(out.Stashes, Stash{
			Index:       s.Index,
			Ref:         s.Ref(),
			SHA:         s.SHA,
			Label:       s.Label,
			AutoLabel:   s.AutoLabel(),
			Tags:        jsonTags(s.Tags),
			Note:        s.Note,
			Display:     display,
			LabelSource: srcKind.String(),
			Subject:     s.Subject,
			Branch:      s.Branch,
			TopFile:     s.TopFile,
			Created:     s.Created.UTC(),
			Age:         age.Humanize(s.Created, now),
			AgeSeconds:  ageSecs,
			Staleness:   bucket.String(),
			Stale:       dusty,
			Diffstat: Diffstat{
				Added:   s.Diffstat.Added,
				Deleted: s.Diffstat.Deleted,
				Files:   s.Diffstat.Files,
				Binary:  s.Diffstat.Binary,
			},
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// RepoGroup pairs a repository path with its stashes for the cross-repo JSON
// output (issue #33). It mirrors the render layer's grouping so the CLI can
// hand the same data to either renderer.
type RepoGroup struct {
	Repo    string
	Stashes []model.Stash
}

// RepoOutput is one repo's entry in the cross-repo JSON document: its path plus
// the same per-stash shapes the single-repo output uses.
type RepoOutput struct {
	Repo       string  `json:"repo"`
	Count      int     `json:"count"`
	DustyCount int     `json:"dusty_count"`
	Stashes    []Stash `json:"stashes"`
}

// CrossRepoOutput is the top-level document for `--all-repos --json`: the active
// threshold, machine-wide totals, and per-repo groups (each with its own
// stashes). Repos are emitted in the order given (the CLI sorts by path).
type CrossRepoOutput struct {
	StaleDays  int          `json:"stale_days"`
	RepoCount  int          `json:"repo_count"`
	Count      int          `json:"count"`
	DustyCount int          `json:"dusty_count"`
	Repos      []RepoOutput `json:"repos"`
}

// WriteCrossRepo renders a multi-repo dashboard as indented JSON to w. Each
// group's stashes are encoded exactly like the single-repo output; top-level
// totals aggregate across every repo so a script can gate on machine-wide dust
// without re-summing. now/staleDays drive age + staleness as elsewhere.
func WriteCrossRepo(w io.Writer, groups []RepoGroup, now time.Time, staleDays int) error {
	out := CrossRepoOutput{
		StaleDays: staleDays,
		RepoCount: len(groups),
		Repos:     make([]RepoOutput, 0, len(groups)),
	}
	for _, g := range groups {
		ro := RepoOutput{Repo: g.Repo, Count: len(g.Stashes), Stashes: make([]Stash, 0, len(g.Stashes))}
		for _, s := range g.Stashes {
			js := encodeStash(s, now, staleDays)
			if js.Stale {
				ro.DustyCount++
				out.DustyCount++
			}
			out.Count++
			ro.Stashes = append(ro.Stashes, js)
		}
		out.Repos = append(out.Repos, ro)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// encodeStash builds the JSON shape for a single stash, shared by the
// single-repo Write and the cross-repo WriteCrossRepo so the per-stash schema
// stays identical across both.
func encodeStash(s model.Stash, now time.Time, staleDays int) Stash {
	bucket := age.Classify(s.Created, now, staleDays)
	ageSecs := int64(0)
	if !s.Created.IsZero() {
		if d := now.Sub(s.Created); d > 0 {
			ageSecs = int64(d.Seconds())
		}
	}
	display, srcKind := s.DisplaySource()
	return Stash{
		Index:       s.Index,
		Ref:         s.Ref(),
		SHA:         s.SHA,
		Label:       s.Label,
		AutoLabel:   s.AutoLabel(),
		Tags:        jsonTags(s.Tags),
		Note:        s.Note,
		Display:     display,
		LabelSource: srcKind.String(),
		Subject:     s.Subject,
		Branch:      s.Branch,
		TopFile:     s.TopFile,
		Created:     s.Created.UTC(),
		Age:         age.Humanize(s.Created, now),
		AgeSeconds:  ageSecs,
		Staleness:   bucket.String(),
		Stale:       bucket.Dusty(),
		Diffstat: Diffstat{
			Added:   s.Diffstat.Added,
			Deleted: s.Diffstat.Deleted,
			Files:   s.Diffstat.Files,
			Binary:  s.Diffstat.Binary,
		},
	}
}

// jsonTags returns tags as a non-nil slice so the JSON renders "[]" rather than
// "null" for a stash with no tags, keeping the wire shape stable for scripts
// that index `.tags[]` unconditionally.
func jsonTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	out := make([]string, len(tags))
	copy(out, tags)
	return out
}
