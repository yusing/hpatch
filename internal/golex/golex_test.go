package golex

import "testing"

func TestIdentifierAndLiteral(t *testing.T) {
	for _, value := range []string{"alpha", "_alpha", "世界9"} {
		if !IsIdentifier(value) {
			t.Errorf("IsIdentifier(%q) = false", value)
		}
	}
	for _, value := range []string{"", "9alpha", "func", "a-b"} {
		if IsIdentifier(value) {
			t.Errorf("IsIdentifier(%q) = true", value)
		}
	}
	if got, err := DecodeStringLiteral(`"example.com/pkg"`); err != nil || got != "example.com/pkg" {
		t.Fatalf("DecodeStringLiteral = %q, %v", got, err)
	}
	if got, err := DecodeStringLiteral("`line\rbreak`"); err != nil || got != "linebreak" {
		t.Fatalf("DecodeStringLiteral raw = %q, %v", got, err)
	}
}
