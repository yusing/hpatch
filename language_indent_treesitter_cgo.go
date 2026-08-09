//go:build cgo

package hpatch

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	treeSitterJavaScript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	treeSitterPython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	treeSitterTypeScript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

const treeSitterIndentationAvailable = true

var (
	pythonTreeSitterLanguage     = sitter.NewLanguage(treeSitterPython.Language())
	javaScriptTreeSitterLanguage = sitter.NewLanguage(treeSitterJavaScript.Language())
	typeScriptTreeSitterLanguage = sitter.NewLanguage(treeSitterTypeScript.LanguageTypescript())
)

func parseIndentationTree(source string, language indentationWrapperLanguage) *sitter.Tree {
	var parserLanguage *sitter.Language
	switch language {
	case indentationLanguagePython:
		parserLanguage = pythonTreeSitterLanguage
	case indentationLanguageTypeScript:
		parserLanguage = typeScriptTreeSitterLanguage
	case indentationLanguageJavaScript:
		parserLanguage = javaScriptTreeSitterLanguage
	default:
		return nil
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(parserLanguage); err != nil {
		return nil
	}
	return parser.Parse([]byte(source), nil)
}

func inferIndentationUnit(source string, language indentationWrapperLanguage) string {
	tree := parseIndentationTree(source, language)
	if tree == nil {
		return ""
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return ""
	}
	units := make(map[string]struct{})
	collectIndentationUnits(root, source, language, units)
	if len(units) != 1 {
		return ""
	}
	for unit := range units {
		return unit
	}
	return ""
}

func collectIndentationUnits(node *sitter.Node, source string, language indentationWrapperLanguage, units map[string]struct{}) {
	if isIndentationBlock(node, language) {
		owner := node.Parent()
		if owner != nil {
			parentIndent := leadingIndentAtByte(source, owner.StartByte())
			parentRow := owner.StartPosition().Row
			for index := uint(0); index < node.NamedChildCount(); index++ {
				child := node.NamedChild(index)
				if child == nil || child.IsExtra() || child.Kind() == "comment" ||
					child.StartPosition().Row <= parentRow {
					continue
				}
				childIndent := leadingIndentAtByte(source, child.StartByte())
				if !strings.HasPrefix(childIndent, parentIndent) || len(childIndent) == len(parentIndent) {
					continue
				}
				suffix := childIndent[len(parentIndent):]
				if isUniformIndentUnit(suffix) {
					units[suffix] = struct{}{}
				}
			}
		}
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child != nil {
			collectIndentationUnits(child, source, language, units)
		}
	}
}

func isIndentationBlock(node *sitter.Node, language indentationWrapperLanguage) bool {
	if language == indentationLanguagePython {
		return node.Kind() == "block"
	}
	return node.Kind() == "statement_block"
}

func isUniformIndentUnit(value string) bool {
	if value == "" || (value[0] != ' ' && value[0] != '\t') {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] != value[0] {
			return false
		}
	}
	return true
}

func leadingIndentAtByte(source string, offset uint) string {
	if offset > uint(len(source)) {
		offset = uint(len(source))
	}
	start := int(offset)
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	indent, _ := splitIndent(source[start:])
	return indent
}

func proveWrapperMemberships(source string, probes []indentationWrapperProbe, language indentationWrapperLanguage) []bool {
	proven := make([]bool, len(probes))
	tree := parseIndentationTree(source, language)
	if tree == nil {
		return proven
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return proven
	}
	for index, probe := range probes {
		proven[index] = proveWrapperMembership(root, source, probe, language)
	}
	return proven
}

func proveWrapperMembership(root *sitter.Node, source string, probe indentationWrapperProbe, language indentationWrapperLanguage) bool {
	if probe.childStart < 0 || probe.childEnd > len(source) || probe.childStart >= probe.childEnd {
		return false
	}
	node := root.NamedDescendantForByteRange(uint(probe.childStart), uint(probe.childEnd))
	for node != nil {
		block := node.Parent()
		if block != nil && isIndentationBlock(block, language) {
			owner := block.Parent()
			if owner != nil &&
				owner.Kind() == "if_statement" &&
				owner.StartByte() >= uint(probe.replacementStart) &&
				owner.EndByte() <= uint(probe.replacementEnd) &&
				block.StartByte() >= uint(probe.replacementStart) &&
				block.EndByte() <= uint(probe.replacementEnd) &&
				node.StartByte() == uint(probe.childStart) &&
				node.EndByte() <= uint(probe.childEnd) &&
				source[node.StartByte():node.EndByte()] == probe.candidate.preserved {
				return true
			}
		}
		node = node.Parent()
	}
	return false
}

func findLanguageSyntaxFailure(source string, language indentationWrapperLanguage) (languageSyntaxFailure, bool) {
	tree := parseIndentationTree(source, language)
	if tree == nil {
		return languageSyntaxFailure{}, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || !root.HasError() {
		return languageSyntaxFailure{}, false
	}

	var earliest *sitter.Node
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if node == nil {
			return
		}
		if (node.IsError() || node.IsMissing()) && syntaxNodePrecedes(node, earliest) {
			earliest = node
		}
		for index := uint(0); index < node.ChildCount(); index++ {
			visit(node.Child(index))
		}
	}
	visit(root)
	if earliest == nil {
		return languageSyntaxFailure{}, false
	}
	position := earliest.StartPosition()
	return languageSyntaxFailure{
		line:    int(position.Row) + 1,
		column:  int(position.Column) + 1,
		kind:    earliest.Kind(),
		missing: earliest.IsMissing(),
	}, true
}

func syntaxNodePrecedes(candidate, current *sitter.Node) bool {
	if current == nil {
		return true
	}
	if candidate.StartByte() != current.StartByte() {
		return candidate.StartByte() < current.StartByte()
	}
	if candidate.IsMissing() != current.IsMissing() {
		return !candidate.IsMissing()
	}
	if candidate.EndByte() != current.EndByte() {
		return candidate.EndByte() < current.EndByte()
	}
	return candidate.Kind() < current.Kind()
}
