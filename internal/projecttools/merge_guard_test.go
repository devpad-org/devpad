package projecttools

import (
	"reflect"
	"strings"
	"testing"
)

// The assistant can open merge requests and comment on them but never
// merge or close one, so it can't merge over a pipeline gate either: the
// merge request surface it is given has no way to.
func TestTheAssistantCannotMerge(t *testing.T) {
	surface := reflect.TypeFor[MergeRequests]()
	for i := range surface.NumMethod() {
		if name := surface.Method(i).Name; strings.Contains(name, "Merge") || strings.Contains(name, "Override") || name == "Close" {
			t.Errorf("the assistant's merge request surface has %s", name)
		}
	}
}
