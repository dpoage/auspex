package lint

import (
	"regexp"
	"strings"
)

// listMarker matches a list item marker at the start of a line's text, after
// its indentation: "-", "*", "+", or 1-9 digits followed by "." or ")", then
// a space or tab.
var listMarker = regexp.MustCompile(`^(?:[-*+]|[0-9]{1,9}[.)])[ \t]`)

// atxHeading matches an ATX heading's text, after its indentation: 1-6 "#"
// then a space, a tab, or the end of the line.
var atxHeading = regexp.MustCompile(`^#{1,6}(?:[ \t]|$)`)

// fenceOpen matches a code fence line at any indentation: a run of three or
// more backticks or tildes. The run is captured to find its closing fence.
// opensFence rejects a backtick run followed by another backtick on the same
// line, which is inline code, not a fence.
var fenceOpen = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})(.*)$")

// Units splits text into the unit shape the oracle-rounds skill judges one
// at a time: paragraphs and top-level bullets. A blank line ends a unit.
//
// A list item starts a new unit unless it is nested inside the open list
// item: the outermost item of the current list. Nesting is relative, as in
// CommonMark: an item is nested when its marker is indented at least as far
// as the open item's content column, where the text after its marker starts.
// A nested item stays with its parent, however few columns deeper it sits.
// A non-nested item indented 0-3 columns becomes the open item. A blank line
// does not close the open item, but after one, a line indented less than its
// content column does.
//
// An ATX heading line always starts a unit, even directly after a list
// item; every other line joins the unit it follows (wrapped or lazy
// continuation, paragraph under a heading). A fenced code block is never
// split: every line from its opening fence to its closing fence joins the
// current unit, blank and marker-shaped lines included. Units are
// whitespace-trimmed, and empty units are dropped.
func Units(text string) []string {
	var units []string
	var cur []string
	flush := func() {
		if u := strings.TrimSpace(strings.Join(cur, "\n")); u != "" {
			units = append(units, u)
		}
		cur = cur[:0]
	}
	fence := ""    // the open fence's run of backticks or tildes, "" outside a fence
	open := -1     // the open list item's content column, -1 when no item is open
	blank := false // the previous line was blank
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if fence != "" {
			cur = append(cur, line)
			if closesFence(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			blank = true
			continue
		}
		col, rest := columnAfter(0, line)
		nested := open >= 0 && col >= open
		content, item := itemContent(col, rest)
		switch {
		case col <= 3 && atxHeading.MatchString(rest):
			flush()
			if !nested {
				open = -1
			}
		case item && nested: // a nested item stays with its parent's unit
		case item && col <= 3:
			flush()
			open = content
		case blank && !nested:
			open = -1
		}
		blank = false
		cur = append(cur, line)
		if run, ok := opensFence(line); ok {
			fence = run
		}
	}
	flush()
	return units
}

// columnAfter returns the column reached after the leading spaces and tabs
// of s (which starts at column col) and the rest of s. A tab advances to
// the next multiple of 4.
func columnAfter(col int, s string) (int, string) {
	for i := range len(s) {
		switch s[i] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col, s[i:]
		}
	}
	return col, ""
}

// itemContent reports whether text, starting at column col, opens a list item
// and returns its content column: the marker's end plus the whitespace after
// it, or one column further when nothing follows it.
func itemContent(col int, text string) (int, bool) {
	m := listMarker.FindString(text)
	if m == "" {
		return 0, false
	}
	end := col + len(m) - 1 // the column just past the marker
	content, rest := columnAfter(end, text[len(m)-1:])
	if content-end > 4 || rest == "" {
		return end + 1, true
	}
	return content, true
}

// opensFence reports whether line opens a code fence, and the fence's run.
func opensFence(line string) (string, bool) {
	m := fenceOpen.FindStringSubmatch(line)
	if m == nil || (m[1][0] == '`' && strings.Contains(m[2], "`")) {
		return "", false
	}
	return m[1], true
}

// closesFence reports whether line closes a fence opened with run: the same
// character repeated at least as many times, with nothing after it.
func closesFence(line, run string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(run) && strings.Trim(t, run[:1]) == ""
}
