//go:build !cgo

package router

import (
	"strings"
	"testing"
)

func TestCodeModeCommentaryWithoutCGORejectsOnlyReservedCalls(t *testing.T) {
	for _, source := range []string{
		`await commentary("working");`,
		"await /* before */ commentary /* after */ ('working');",
	} {
		if _, err := findCodeModeCommentaryCalls(source); err == nil || !strings.Contains(err.Error(), "cgo-enabled") {
			t.Fatalf("source %q error = %v", source, err)
		}
	}

	for _, source := range []string{
		`const commentaryCount = 1; await commentaryCount();`,
		`text("await commentary('ignored')");`,
		"text('await commentary(\\'ignored\\')');",
		"text(`await commentary('ignored')`);",
		"// await commentary('ignored')\ntext('done');",
		"/* await commentary('ignored') */ text('done');",
		`awaitCommentary(); commentary();`,
	} {
		if calls, err := findCodeModeCommentaryCalls(source); err != nil || len(calls) != 0 {
			t.Fatalf("source %q calls = %v, error = %v", source, calls, err)
		}
	}
}
