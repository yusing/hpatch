package router

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	treeSitterTypeScript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	"mvdan.cc/sh/v3/syntax"
)

const shellTypeScriptDiagnostic = "shell: [shell-typescript-misuse] Rejected before execution: the Bash body is invalid Bash but valid TypeScript/JavaScript. Use functions.exec for Code Mode helpers such as tools, ALL_TOOLS, and text when available; call collaboration tools directly. For an ordinary script, select an explicit interpreter with a shebang such as #!node or #!bun. No script or command template was executed."

const shellCodeModeRecoveryWarning = "shell: [shell-code-mode-recovered] Recovered Code Mode JavaScript submitted through functions.shell; use functions.exec directly next time"

// Recovery requires JavaScript syntax and a reference to the Code Mode runtime,
// not text that merely resembles a call. Explicit shell headers, directives,
// retained references, and valid Bash never opt into it.
func shellCodeModeRecovery(contribution toolContribution, input string) bool {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" {
		return false
	}
	program := strings.TrimLeft(input, " \t\r\n")
	if strings.HasPrefix(program, "#!") {
		return false
	}
	if _, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(program), ""); err == nil {
		return false
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(codeModeJavaScriptLanguage); err != nil {
		return false
	}
	source := []byte(program)
	tree := parser.Parse(source, nil)
	if tree == nil {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return false
	}

	// Conservatively exclude a runtime name if any binding or assignment in the
	// program owns it. This avoids inventing runtime references for local helpers
	// without trying to emulate JavaScript's lexical-scope or execution semantics.
	shadowed := make(map[string]bool)
	var bind func(*sitter.Node)
	bind = func(node *sitter.Node) {
		if node == nil {
			return
		}
		switch node.Kind() {
		case "identifier", "shorthand_property_identifier_pattern":
			switch name := node.Utf8Text(source); name {
			case "tools", "ALL_TOOLS", "text", "image", "audio", "generatedImage":
				shadowed[name] = true
			}
		}
		for i := range node.NamedChildCount() {
			bind(node.NamedChild(uint(i)))
		}
	}
	var bindings func(*sitter.Node)
	bindings = func(node *sitter.Node) {
		switch node.Kind() {
		case "variable_declarator", "function_declaration", "function_expression", "generator_function_declaration", "generator_function", "class_declaration", "class":
			bind(node.ChildByFieldName("name"))
		case "formal_parameters", "import_clause":
			bind(node)
		case "arrow_function", "catch_clause":
			bind(node.ChildByFieldName("parameter"))
		case "assignment_expression", "augmented_assignment_expression", "for_in_statement":
			left := node.ChildByFieldName("left")
			if left != nil && left.Kind() != "member_expression" && left.Kind() != "subscript_expression" {
				bind(left)
			}
		}
		for i := range node.NamedChildCount() {
			bindings(node.NamedChild(uint(i)))
		}
	}
	bindings(root)
	var usesRuntime func(*sitter.Node) bool
	usesRuntime = func(node *sitter.Node) bool {
		if node.Kind() == "identifier" && node.Utf8Text(source) == "ALL_TOOLS" && !shadowed["ALL_TOOLS"] {
			return true
		}
		if node.Kind() == "call_expression" {
			callee := node.ChildByFieldName("function")
			if callee != nil && callee.Kind() == "identifier" && !shadowed[callee.Utf8Text(source)] {
				switch callee.Utf8Text(source) {
				case "text", "image", "audio", "generatedImage":
					return true
				}
			}
			if callee != nil && (callee.Kind() == "member_expression" || callee.Kind() == "subscript_expression") {
				object := callee.ChildByFieldName("object")
				if object != nil && object.Kind() == "identifier" && object.Utf8Text(source) == "tools" && !shadowed["tools"] {
					return true
				}
			}
		}
		for i := range node.NamedChildCount() {
			if usesRuntime(node.NamedChild(uint(i))) {
				return true
			}
		}
		return false
	}
	return usesRuntime(root)
}

var shellTypeScriptLanguage = sitter.NewLanguage(treeSitterTypeScript.LanguageTypescript())

// Inspect the translator's normalized argv, so shebangs, directives, and retained
// scripts use the same interpreter and body as execution. Valid Bash wins even
// when it also parses as TypeScript; never reinterpret or execute rejected input.
func shellTypeScriptMisuse(contribution toolContribution, arguments []string) bool {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" ||
		len(arguments) < 2 || shellInterpreterName(arguments[0]) != "bash" {
		return false
	}
	body := arguments[len(arguments)-1]
	if _, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(body), ""); err == nil {
		return false
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(shellTypeScriptLanguage); err != nil {
		return false
	}
	tree := parser.Parse([]byte(body), nil)
	if tree == nil {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()
	return root != nil && !root.HasError()
}
