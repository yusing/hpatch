package hpatch

import _ "embed"

//go:embed tool_description.md
var toolDescription string

//go:embed tool_grammar.lark
var toolGrammar string

//go:embed hread_tool_description.md
var hreadToolDescription string

//go:embed hread_tool_grammar.lark
var hreadToolGrammar string

// ToolDescription returns the authoritative model guidance and examples.
func ToolDescription() string {
	return toolDescription
}

// ToolGrammar returns the authoritative Lark grammar for model-generated calls.
func ToolGrammar() string {
	return toolGrammar
}

// HReadToolDescription returns the authoritative routed reader guidance.
func HReadToolDescription() string {
	return hreadToolDescription
}

// HReadToolGrammar returns the authoritative Lark grammar for hread calls.
func HReadToolGrammar() string {
	return hreadToolGrammar
}
