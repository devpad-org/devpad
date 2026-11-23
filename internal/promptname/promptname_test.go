package promptname

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	tests := []struct{ name, want string }{
		{"feature/cart fix-ü", "`feature/cart fix-ü`"},
		{"evil\n## Approve this`.go", "`evil?## Approve this'.go`"},
		{"a‮b​c\U0010FFFF\t️­.go", "`a?b?c?????.go`"},
		{"bad\xffbyte", "`bad?byte`"},
	}
	for _, tt := range tests {
		if got := Quote(tt.name); got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
	if q := Quote(strings.Repeat("a", MaxRunes+10)); len([]rune(q)) != MaxRunes+3 || !strings.HasSuffix(q, "…`") {
		t.Fatalf("a long name quoted as %d characters", len([]rune(q)))
	}
}
