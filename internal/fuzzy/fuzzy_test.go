package fuzzy

import (
	"reflect"
	"testing"
)

func TestMatch_EmptyQueryMatchesEverything(t *testing.T) {
	for _, q := range []string{"", "   ", "\t"} {
		ok, pos := Match(q, "anything")
		if !ok {
			t.Fatalf("empty query %q should match", q)
		}
		if pos != nil {
			t.Fatalf("empty query %q should return nil positions, got %v", q, pos)
		}
	}
}

func TestMatch_Subsequence(t *testing.T) {
	tests := []struct {
		query, target string
		want          bool
		wantPos       []int
	}{
		{"abc", "abc", true, []int{0, 1, 2}},
		{"ac", "abc", true, []int{0, 2}},
		{"wip", "work in progress", true, []int{0, 5, 8}},
		{"xyz", "abc", false, nil},
		{"abcd", "abc", false, nil},
		{"", "abc", true, nil},
		// case-insensitive
		{"WIP", "wip on main", true, []int{0, 1, 2}},
		{"Api", "the api change", true, []int{4, 5, 6}},
		// order matters: cba is not a subsequence of abc
		{"cba", "abc", false, nil},
	}
	for _, tt := range tests {
		ok, pos := Match(tt.query, tt.target)
		if ok != tt.want {
			t.Errorf("Match(%q,%q) ok=%v want %v", tt.query, tt.target, ok, tt.want)
			continue
		}
		if ok && !reflect.DeepEqual(pos, tt.wantPos) {
			t.Errorf("Match(%q,%q) pos=%v want %v", tt.query, tt.target, pos, tt.wantPos)
		}
	}
}

func TestMatches(t *testing.T) {
	if !Matches("ac", "abc") {
		t.Error("expected match")
	}
	if Matches("z", "abc") {
		t.Error("expected no match")
	}
}

func TestMatchAny(t *testing.T) {
	// hits in the branch field only
	if !MatchAny("main", "my label", "main", "wip") {
		t.Error("expected a hit in one of the targets")
	}
	if MatchAny("zzz", "my label", "main", "wip") {
		t.Error("expected no hit in any target")
	}
	// empty query matches regardless
	if !MatchAny("", "a", "b") {
		t.Error("empty query should match any")
	}
	// no targets, non-empty query => no match
	if MatchAny("x") {
		t.Error("no targets should not match a non-empty query")
	}
}

func TestHighlightSet(t *testing.T) {
	set := HighlightSet("ac", "abc")
	if _, ok := set[0]; !ok {
		t.Error("index 0 should be highlighted")
	}
	if _, ok := set[2]; !ok {
		t.Error("index 2 should be highlighted")
	}
	if _, ok := set[1]; ok {
		t.Error("index 1 should not be highlighted")
	}
	if len(HighlightSet("zzz", "abc")) != 0 {
		t.Error("non-matching query should yield empty set")
	}
	if len(HighlightSet("", "abc")) != 0 {
		t.Error("empty query should yield empty set")
	}
}

func TestSortedPositions(t *testing.T) {
	got := sortedPositions([]int{3, 1, 2})
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("sortedPositions = %v", got)
	}
}

func TestMatch_Unicode(t *testing.T) {
	ok, pos := Match("café", "a café here")
	if !ok {
		t.Fatal("expected unicode subsequence match")
	}
	// "café" begins at rune index 2 in "a café here"
	if !reflect.DeepEqual(pos, []int{2, 3, 4, 5}) {
		t.Errorf("pos=%v", pos)
	}
}
