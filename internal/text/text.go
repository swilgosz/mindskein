// Package text holds the column helpers the renderers share.
//
// They count runes rather than bytes: titles here are written in two languages,
// and byte arithmetic on them has already produced an invalid UTF-8 truncation.
// Column width itself needs no helper — fmt measures a %s width in runes,
// verified rather than assumed.
package text

// Truncate hard-cuts s to n runes, marking the cut with an ellipsis.
func Truncate(s string, n int) string {
	if n <= 0 || len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
