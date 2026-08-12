package hpatch

import (
	_ "embed"

	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

// ToolDescription returns the concise model-visible call contract.
func ToolDescription() string {
	return codexinstructions.HPatchToolDescription
}

// ToolHelp returns the complete model-facing HPATCH/2 reference.
func ToolHelp() string {
	return codexinstructions.HPatchToolHelp()
}

//go:embed tool_grammar.lark
var toolGrammar string

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}
