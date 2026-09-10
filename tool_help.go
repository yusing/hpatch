package mekugi

import (
	_ "embed"

	codexinstructions "github.com/yusing/mekugi/contrib/codex"
)

// ToolDescription returns the concise model-visible call contract.
func ToolDescription() string {
	return codexinstructions.MekugiToolDescription
}

//go:embed tool_grammar.lark
var toolGrammar string

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}
