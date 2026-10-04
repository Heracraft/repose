package store

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanText makes s storable in a text column and safe in a log line: valid
// UTF-8, no control characters but newline and tab, at most n bytes cut on
// a rune boundary.
func CleanText(s string, n int) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
