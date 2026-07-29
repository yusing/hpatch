package hpatch

import _ "embed"

//go:embed tool_description.md
var toolDescription string

//go:embed tool_grammar.lark
var toolGrammar string

// ToolDescription returns the authoritative model guidance and examples.
func ToolDescription() string {
	return toolDescription
}

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}
