// Package text holds the column helpers the renderers share.
//
// Both count runes rather than bytes: the plan and the session titles are
// written in two languages, and byte arithmetic on them has already produced an
// invalid UTF-8 truncation. Column width itself needs no helper — fmt measures
// a %s width in runes, verified rather than assumed.
package text

import (
	"strings"
	"unicode"
)

// Truncate hard-cuts s to n runes, marking the cut with an ellipsis.
func Truncate(s string, n int) string {
	if n <= 0 || len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Summarize cuts s to at most n runes at a word boundary, so a sentence
// truncated for a column still ends on a whole word.
//
// It gives up on the boundary rather than cut past halfway: one long token
// would otherwise trim away most of the line.
func Summarize(s string, n int) string {
	runes := []rune(s)
	if n <= 1 || len(runes) <= n {
		return s
	}
	cut := runes[:n-1]
	for i := len(cut) - 1; i >= n/2; i-- {
		if unicode.IsSpace(cut[i]) {
			cut = cut[:i]
			break
		}
	}
	return strings.TrimRight(string(cut), " ,;:·") + "…"
}

// Clean strips the control characters out of s, leaving ordinary spaces.
//
// The brief prints prose that was typed or pasted into a session, and a paste
// carrying terminal escapes would otherwise redraw the page it is printed on:
// an ESC moves the cursor, and a carriage return overwrites the line above.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
