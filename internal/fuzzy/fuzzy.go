// Package fuzzy provides a tiny, dependency-free subsequence matcher used by
// the TUI's live filter-as-you-type (issue #29).
//
// The match model is deliberately simple and predictable: a query matches a
// target when every rune of the query appears in the target, in order, as a
// (not necessarily contiguous) subsequence — the same "type a few chars to
// narrow" feel as fzf/lazygit. Matching is case-insensitive.
//
// Match also returns the target rune indices that were consumed, so callers can
// highlight the matched characters. A degenerate empty query matches everything
// with no highlighted positions.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

// Match reports whether query matches target as a case-insensitive subsequence
// and, when it does, the indices (into target's rune slice) of the matched
// characters. An empty (or whitespace-only) query matches everything and
// returns a nil index slice.
//
// The greedy left-to-right scan is intentional: it is O(len(target)), stable,
// and good enough for a stash list of realistic size. It highlights the first
// valid alignment of the query in the target.
func Match(query, target string) (bool, []int) {
	q := strings.TrimSpace(query)
	if q == "" {
		return true, nil
	}
	qr := []rune(strings.ToLower(q))
	tr := []rune(target)

	positions := make([]int, 0, len(qr))
	qi := 0
	for ti := 0; ti < len(tr) && qi < len(qr); ti++ {
		if unicode.ToLower(tr[ti]) == qr[qi] {
			positions = append(positions, ti)
			qi++
		}
	}
	if qi != len(qr) {
		return false, nil
	}
	return true, positions
}

// Matches is a convenience wrapper that reports only whether query matches
// target, discarding the highlight positions.
func Matches(query, target string) bool {
	ok, _ := Match(query, target)
	return ok
}

// MatchAny reports whether query matches at least one of the given targets
// (case-insensitive subsequence). It is used to filter a stash across several
// fields (label, branch, tags, note) with a single query: a hit in any field
// keeps the row. An empty query matches everything.
func MatchAny(query string, targets ...string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	for _, t := range targets {
		if Matches(query, t) {
			return true
		}
	}
	return false
}

// HighlightSet returns the matched rune indices for query against target as a
// set, convenient for renderers that walk target rune-by-rune and ask "is this
// index highlighted?". A non-matching (or empty) query yields an empty set.
func HighlightSet(query, target string) map[int]struct{} {
	ok, pos := Match(query, target)
	if !ok || len(pos) == 0 {
		return map[int]struct{}{}
	}
	set := make(map[int]struct{}, len(pos))
	for _, p := range pos {
		set[p] = struct{}{}
	}
	return set
}

// sortedPositions returns pos sorted ascending (defensive; Match already yields
// ascending order). Exposed indirectly via tests to lock the contract.
func sortedPositions(pos []int) []int {
	out := append([]int(nil), pos...)
	sort.Ints(out)
	return out
}
