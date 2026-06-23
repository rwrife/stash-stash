// Package search greps across stash *contents* — the unified diff a stash
// would apply — so you can answer "which stash had that retry change?" without
// applying each one by hand (issue #8).
//
// It is pure and dependency-free: callers fetch each stash's patch (via
// `git stash show -p`, in internal/git) and hand the text here together with a
// Matcher. Scan walks the patch, tracks which file each hunk belongs to, and
// returns the matching lines as skimmable snippets (path + line kind + the
// changed text), so the command layer can print "<label> · <age>" headers with
// the hits underneath.
//
// Matching is line-oriented and defaults to a case-insensitive substring; a
// regular-expression Matcher (built with Regexp) covers the `--regex` flag.
package search

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// LineKind classifies a matching patch line so the renderer can show *how* the
// text appears in the stash: added, removed, or surrounding context.
type LineKind int

const (
	// KindContext is an unchanged context line in a hunk (leading space).
	KindContext LineKind = iota
	// KindAdded is an inserted line (leading '+').
	KindAdded
	// KindRemoved is a deleted line (leading '-').
	KindRemoved
)

// Symbol returns the single-character diff prefix for the kind ("+", "-", or a
// space), handy for rendering a snippet that still reads like a diff.
func (k LineKind) Symbol() string {
	switch k {
	case KindAdded:
		return "+"
	case KindRemoved:
		return "-"
	default:
		return " "
	}
}

// Match is a single matching line within a stash's patch.
type Match struct {
	// File is the path the hunk applies to (the "b/" side of the diff, or the
	// "a/" side for a pure deletion). Empty only for matches that appear before
	// any file header (rare; defensive).
	File string

	// Kind is whether the matched line was added, removed, or context.
	Kind LineKind

	// Line is the line number within the *new* file for added/context lines, or
	// the *old* file for removed lines, derived from the hunk header. It is 0
	// when no hunk header has been seen yet (it cannot be determined).
	Line int

	// Text is the matched line's content with its leading diff marker stripped
	// and trailing CR/whitespace-newline removed, ready to print after a marker.
	Text string
}

// Matcher reports whether a single line of text matches the user's query. It is
// satisfied by both the literal (case-insensitive substring) and regexp
// strategies so Scan does not care which one it was given.
type Matcher interface {
	MatchLine(s string) bool
}

// Substring is a case-insensitive substring Matcher (the default search mode).
type Substring struct{ needleLower string }

// Literal builds a case-insensitive substring Matcher for term. An empty term
// matches nothing (callers should reject an empty query before scanning, but we
// stay safe regardless).
func Literal(term string) Substring {
	return Substring{needleLower: strings.ToLower(term)}
}

// MatchLine reports whether s contains the (case-folded) needle.
func (m Substring) MatchLine(s string) bool {
	if m.needleLower == "" {
		return false
	}
	return strings.Contains(strings.ToLower(s), m.needleLower)
}

// Regexp builds a regular-expression Matcher for pattern (the `--regex` mode).
// It is case-insensitive by default — matching the literal mode and the
// issue's "case-insensitive by default" requirement — unless the pattern
// already sets its own flags. It returns the compile error verbatim so the
// command layer can report a bad pattern cleanly.
func Regexp(pattern string) (RegexMatcher, error) {
	// Fold case unless the caller embedded inline flags (e.g. "(?s)") that we
	// shouldn't second-guess; the common case is a plain pattern, which we make
	// case-insensitive to match the default substring behavior.
	src := pattern
	if !strings.HasPrefix(pattern, "(?") {
		src = "(?i)" + pattern
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return RegexMatcher{}, err
	}
	return RegexMatcher{re: re}, nil
}

// RegexMatcher is a compiled-regexp Matcher.
type RegexMatcher struct{ re *regexp.Regexp }

// MatchLine reports whether the pattern matches anywhere in s.
func (m RegexMatcher) MatchLine(s string) bool {
	if m.re == nil {
		return false
	}
	return m.re.MatchString(s)
}

// Scan walks a unified-diff patch (as produced by `git stash show -p`) and
// returns every line whose changed/context text matches m, annotated with the
// file it belongs to, its kind, and (where derivable) its line number.
//
// It tracks the current file from "+++ b/<path>" / "--- a/<path>" headers and
// keeps running line counters from each "@@ -old,+new @@" hunk header, so each
// Match carries a useful location. Diff metadata lines (headers, "diff --git",
// "index", "\ No newline…") are never themselves matched — only real content.
func Scan(patch string, m Matcher) []Match {
	if m == nil || patch == "" {
		return nil
	}

	var matches []Match
	var file string
	// oldLine/newLine track the next line number to assign as we walk a hunk.
	oldLine, newLine := 0, 0
	inHunk := false

	sc := bufio.NewScanner(bytes.NewReader([]byte(patch)))
	// Stash diffs can contain long minified lines; grow the buffer generously.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := sc.Text()

		switch {
		case strings.HasPrefix(line, "+++ "):
			// New-side file header: "+++ b/path" (or "+++ /dev/null" on delete).
			if p := pathFromHeader(line[4:], "b/"); p != "" {
				file = p
			}
			inHunk = false
			continue
		case strings.HasPrefix(line, "--- "):
			// Old-side header: remember it so a pure deletion (new side is
			// /dev/null) still attributes matches to the deleted file.
			if p := pathFromHeader(line[4:], "a/"); p != "" {
				file = p
			}
			inHunk = false
			continue
		case strings.HasPrefix(line, "@@"):
			o, n, ok := parseHunkHeader(line)
			if ok {
				oldLine, newLine = o, n
				inHunk = true
			}
			continue
		case strings.HasPrefix(line, "diff --git "),
			strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "),
			strings.HasPrefix(line, "old mode "),
			strings.HasPrefix(line, "new mode "),
			strings.HasPrefix(line, "similarity index "),
			strings.HasPrefix(line, "rename from "),
			strings.HasPrefix(line, "rename to "),
			strings.HasPrefix(line, "copy from "),
			strings.HasPrefix(line, "copy to "),
			strings.HasPrefix(line, "Binary files "),
			strings.HasPrefix(line, "\\ No newline"):
			// Pure diff metadata — never a content match, and it doesn't advance
			// the per-line counters.
			continue
		}

		if !inHunk || line == "" {
			continue
		}

		// Inside a hunk: classify by the leading marker and advance counters.
		marker := line[0]
		text := line[1:]
		switch marker {
		case '+':
			if m.MatchLine(text) {
				matches = append(matches, Match{File: file, Kind: KindAdded, Line: newLine, Text: trimLine(text)})
			}
			newLine++
		case '-':
			if m.MatchLine(text) {
				matches = append(matches, Match{File: file, Kind: KindRemoved, Line: oldLine, Text: trimLine(text)})
			}
			oldLine++
		case ' ':
			if m.MatchLine(text) {
				matches = append(matches, Match{File: file, Kind: KindContext, Line: newLine, Text: trimLine(text)})
			}
			oldLine++
			newLine++
		default:
			// Anything else inside a hunk (shouldn't happen for well-formed
			// diffs) is ignored without advancing counters.
		}
	}

	return matches
}

// pathFromHeader extracts a file path from a diff header value, stripping the
// expected "a/"/"b/" prefix. It returns "" for /dev/null (add/delete sentinel).
// A trailing tab + timestamp (rare, from some git configs) is trimmed.
func pathFromHeader(v, prefix string) string {
	if i := strings.IndexByte(v, '\t'); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	if v == "/dev/null" {
		return ""
	}
	v = strings.TrimPrefix(v, prefix)
	return v
}

// parseHunkHeader parses the starting old/new line numbers from a unified-diff
// hunk header like "@@ -12,7 +14,9 @@ func Foo()". It returns the first old and
// new line numbers and ok=false if the header can't be parsed.
func parseHunkHeader(line string) (oldStart, newStart int, ok bool) {
	// Strip the leading "@@ " and everything from the closing " @@" onward.
	rest := strings.TrimPrefix(line, "@@")
	end := strings.Index(rest, "@@")
	if end < 0 {
		return 0, 0, false
	}
	body := strings.TrimSpace(rest[:end]) // "-12,7 +14,9"
	fields := strings.Fields(body)
	if len(fields) < 2 {
		return 0, 0, false
	}
	o, ook := parseHunkRangeStart(fields[0], '-')
	n, nok := parseHunkRangeStart(fields[1], '+')
	if !ook || !nok {
		return 0, 0, false
	}
	return o, n, true
}

// parseHunkRangeStart parses the start line from one side of a hunk range, e.g.
// "-12,7" (sign '-') or "+14" (sign '+', count defaults to 1). It returns the
// start line and ok=false on a malformed token.
func parseHunkRangeStart(tok string, sign byte) (int, bool) {
	if len(tok) == 0 || tok[0] != sign {
		return 0, false
	}
	num := tok[1:]
	if i := strings.IndexByte(num, ','); i >= 0 {
		num = num[:i]
	}
	return atoiNonNeg(num)
}

// atoiNonNeg parses a non-negative integer without pulling in strconv error
// semantics; it returns ok=false on any non-digit input or empty string.
func atoiNonNeg(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// trimLine removes a trailing carriage return (CRLF diffs) and any trailing
// spaces/tabs so snippets render cleanly. Leading whitespace is preserved so
// indentation context is visible.
func trimLine(s string) string {
	return strings.TrimRight(s, " \t\r\n")
}
