package domain

import "unicode/utf8"

// CutText cuts s to at most n bytes, before the character the cut would
// split. It looks back no further than one character, so bytes that are not
// UTF-8 cost nothing and are cut like any other.
func CutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for end := n; end > n-utf8.UTFMax && end > 0; end-- {
		if utf8.RuneStart(s[end]) {
			return s[:end]
		}
	}
	return s[:n]
}
