package hpatch

import (
	_ "embed"

	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

// ToolDescription returns the concise model-visible call contract.
func ToolDescription() string {
	return codexinstructions.HPatchToolDescription
}

//go:embed tool_grammar.lark
var toolGrammar string

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}
