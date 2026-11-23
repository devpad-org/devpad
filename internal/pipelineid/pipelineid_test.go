package pipelineid

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for _, id := range []string{"1", "42", "a", "pipeline-7", "0-0", strings.Repeat("a", 64)} {
		if !Valid(id) {
			t.Errorf("%q: expected valid", id)
		}
	}
	for _, id := range []string{
		"", "-1", "A1", "a_b", "a.b", "a/b", "../heads/main", "a b", "a\n", "é",
		strings.Repeat("a", 65), "1@{0}", "x.lock",
	} {
		if Valid(id) {
			t.Errorf("%q: expected refused", id)
		}
	}
}

func TestRef(t *testing.T) {
	if got := Ref("12"); got != "refs/devpad/pipelines/12" || !strings.HasPrefix(got, RefPrefix) {
		t.Fatalf("Ref(12) = %q", got)
	}
}
