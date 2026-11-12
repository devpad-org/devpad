package domain

import "testing"

func TestCutText(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short", "abc", 5, "abc"},
		{"ascii", "abcdef", 4, "abcd"},
		{"before a split character", "ab€cd", 4, "ab"},
		{"after a whole character", "ab€cd", 5, "ab€"},
		{"invalid byte early", "a\xffbcdef", 5, "a\xffbcd"},
		{"no character start", "a\x80\x80\x80\x80\x80\x80", 6, "a\x80\x80\x80\x80\x80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CutText(tt.s, tt.n); got != tt.want {
				t.Fatalf("CutText(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}
