package router

import (
	"slices"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	for _, prefix := range [][]string{
		nil,
		{"--grok"},
		{"--grok=false", "--timeout", "30s"},
		{"--capture-output", "wrap", "--grok"},
		{"--capture-output", "--"},
	} {
		command := []string{"wrap", "codex", "exec", "--model", "example", "--", "wrap"}
		args := append(slices.Clone(prefix), command...)
		gotPrefix, gotCommand, err := SplitCommand(args)
		if err != nil || !slices.Equal(gotPrefix, prefix) || !slices.Equal(gotCommand, command) {
			t.Errorf("SplitCommand(%q) = %q, %q, %v", args, gotPrefix, gotCommand, err)
		}
	}
}

func TestSplitCommandRestrictionsAndDelimiter(t *testing.T) {
	for _, args := range [][]string{
		{"--listen", "127.0.0.1:8080", "wrap", "codex"},
		{"--provider-base-url=https://example.com", "wrap", "codex"},
		{"--unknown", "wrap", "codex"},
	} {
		if _, _, err := SplitCommand(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	for _, args := range [][]string{
		{"--listen", "127.0.0.1:8080"},
		{"--provider-base-url=https://example.com"},
	} {
		if _, command, err := SplitCommand(args); err != nil || len(command) != 0 {
			t.Errorf("standalone %q = %q, %v", args, command, err)
		}
	}
	prefix, command, err := SplitCommand([]string{"--grok", "--", "wrap", "codex"})
	if err != nil || !slices.Equal(prefix, []string{"--grok", "--"}) || !slices.Equal(command, []string{"wrap", "codex"}) {
		t.Fatalf("delimiter = %q, %q, %v", prefix, command, err)
	}
}
