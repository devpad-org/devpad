// Package promptname quotes a name that code under review chose, such as a
// file path or a branch, on one line of a prompt for an agent, so it can
// neither break out of its quotes or its line nor hide text.
package promptname

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxRunes bounds the characters of a name Quote keeps.
const MaxRunes = 200

// Quote quotes name in backticks: only printable characters and spaces
// stay, and every other character, such as a control, format, separator,
// private-use, unassigned or default-ignorable one, becomes "?", backticks
// "'", and it is cut to MaxRunes characters, marked "…".
func Quote(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if n == MaxRunes {
			b.WriteString("…")
			break
		}
		switch {
		case r == '`':
			r = '\''
		case r == utf8.RuneError, r != ' ' && !unicode.IsGraphic(r), unicode.In(r, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector):
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return "`" + b.String() + "`"
}
