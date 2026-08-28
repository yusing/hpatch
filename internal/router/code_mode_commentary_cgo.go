//go:build cgo

package router

import (
	"errors"

	sitter "github.com/tree-sitter/go-tree-sitter"
	treeSitterJavaScript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
)

var codeModeJavaScriptLanguage = sitter.NewLanguage(treeSitterJavaScript.Language())

const codeModeCommentaryParserAvailable = true

func findCodeModeCommentaryCalls(source string) ([]codeModeCommentaryCall, error) {
	sourceBytes := []byte(source)
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(codeModeJavaScriptLanguage); err != nil {
		return nil, err
	}
	tree := parser.Parse(sourceBytes, nil)
	if tree == nil {
		return nil, errors.New("parse Code Mode commentary program")
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return nil, errors.New("Code Mode commentary program has invalid JavaScript syntax")
	}
	var calls []codeModeCommentaryCall
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == "await_expression" && node.NamedChildCount() == 1 {
			call := node.NamedChild(0)
			if call != nil && call.Kind() == "call_expression" {
				callee := call.ChildByFieldName("function")
				arguments := call.ChildByFieldName("arguments")
				if callee != nil && callee.Kind() == "identifier" && callee.Utf8Text(sourceBytes) == "commentary" &&
					arguments != nil && arguments.NamedChildCount() == 1 {
					argument := arguments.NamedChild(0)
					start, end := int(node.StartByte()), int(node.EndByte())
					calls = append(calls, codeModeCommentaryCall{
						start: start, end: end, argumentStart: int(argument.StartByte()), argumentEnd: int(argument.EndByte()),
						form: codeModeCommentaryForm(source, start, end),
					})
				}
			}
		}
		for index := uint(0); index < node.NamedChildCount(); index++ {
			if child := node.NamedChild(index); child != nil {
				walk(child)
			}
		}
	}
	walk(root)
	return calls, nil
}
