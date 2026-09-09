package router

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

type compactionOperation struct {
	tool        string
	arguments   json.RawMessage
	notice      *string
	patchReport string
}

// Decode only static, result-preserving carriers. Never run a script or infer
// success from an arbitrary script's own description of its effects.
func compactionOperationCall(fields map[string]json.RawMessage) (compactionOperation, bool) {
	name := strings.TrimPrefix(jsonString(fields, "name"), "functions.")
	switch jsonString(fields, "type") {
	case "function_call":
		if name == "exec_command" || name == "write_stdin" {
			arguments := json.RawMessage(jsonString(fields, "arguments"))
			var object map[string]json.RawMessage
			if json.Unmarshal(arguments, &object) == nil && object != nil {
				return compactionOperation{tool: name, arguments: arguments}, true
			}
		}
	case "custom_tool_call":
		if name == "shell" {
			return compactionOperation{tool: "shell", arguments: mustMarshalJSON(map[string]any{"input": jsonString(fields, "input")})}, true
		}
		if name == "exec" {
			return compactionCodeModeOperation(jsonString(fields, "input"))
		}
	}
	return compactionOperation{}, false
}

func compactionCodeModeOperation(source string) (compactionOperation, bool) {
	parser := sitter.NewParser()
	defer parser.Close()
	if parser.SetLanguage(codeModeJavaScriptLanguage) != nil {
		return compactionOperation{}, false
	}
	bytes := []byte(source)
	tree := parser.Parse(bytes, nil)
	if tree == nil {
		return compactionOperation{}, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return compactionOperation{}, false
	}
	var statements []*sitter.Node
	for index := range root.NamedChildCount() {
		child := root.NamedChild(uint(index))
		if child.Kind() != "comment" {
			statements = append(statements, child)
		}
	}
	argument := func(node *sitter.Node, callee string) *sitter.Node {
		if node == nil || node.Kind() != "call_expression" {
			return nil
		}
		function, args := node.ChildByFieldName("function"), node.ChildByFieldName("arguments")
		if function == nil || function.Utf8Text(bytes) != callee || args == nil || args.NamedChildCount() != 1 {
			return nil
		}
		return args.NamedChild(0)
	}
	unquote := func(node *sitter.Node) (string, bool) {
		if node == nil || node.Kind() != "string" {
			return "", false
		}
		var value string
		literal := node.Utf8Text(bytes)
		if json.Unmarshal([]byte(literal), &value) == nil {
			return value, true
		}
		value, err := strconv.Unquote(literal)
		return value, err == nil
	}
	// This is the router's own translated-patch carrier. Its awaited host
	// error propagates before the report; see hpatchHistory.carrierInput.
	if strings.HasPrefix(source, hpatchApplyExecMarker) && len(statements) == 2 {
		first, second := statements[0], statements[1]
		if first.Kind() != "expression_statement" || second.Kind() != "expression_statement" {
			return compactionOperation{}, false
		}
		await := first.NamedChild(0)
		if await == nil || await.Kind() != "await_expression" {
			return compactionOperation{}, false
		}
		patch, ok := unquote(argument(await.NamedChild(0), "tools.apply_patch"))
		report, reportOK := unquote(argument(second.NamedChild(0), "text"))
		if !ok || !reportOK || report == "" || !strings.HasPrefix(patch, "*** Begin Patch\n") || !strings.HasSuffix(strings.TrimSpace(patch), "*** End Patch") {
			return compactionOperation{}, false
		}
		var targets []map[string]string
		for line := range strings.SplitSeq(patch, "\n") {
			for _, operation := range []string{"Add File", "Update File", "Delete File", "Move to"} {
				if path, found := strings.CutPrefix(line, "*** "+operation+": "); found && path != "" {
					targets = append(targets, map[string]string{"operation": operation, "path": path})
				}
			}
		}
		if len(targets) == 0 {
			return compactionOperation{}, false
		}
		return compactionOperation{tool: "apply_patch", patchReport: report, arguments: mustMarshalJSON(map[string]any{
			"targets": targets, "patch_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(patch))),
			"patch_body": "retired; historical details are not currently retrievable",
		})}, true
	}
	// Shell carriers may emit a literal progress notice between the awaited
	// execution and its faithful result projection. Preserve and verify it.
	var notice *string
	if len(statements) == 3 && statements[1].Kind() == "expression_statement" {
		value, ok := unquote(argument(statements[1].NamedChild(0), "text"))
		if !ok {
			return compactionOperation{}, false
		}
		notice = &value
		statements = []*sitter.Node{statements[0], statements[2]}
	}
	// Support a direct projection or the generated const-result projection.
	var awaited *sitter.Node
	projectionAddsMetadata := false
	switch len(statements) {
	case 1:
		if statements[0].Kind() != "expression_statement" {
			return compactionOperation{}, false
		}
		projected := argument(statements[0].NamedChild(0), "text")
		if inner := argument(projected, "JSON.stringify"); inner != nil {
			projected = inner
		}
		awaited = projected
	case 2:
		if statements[0].Kind() != "lexical_declaration" || statements[0].NamedChildCount() != 1 || statements[1].Kind() != "expression_statement" {
			return compactionOperation{}, false
		}
		declaration := statements[0].NamedChild(0)
		name, value := declaration.ChildByFieldName("name"), declaration.ChildByFieldName("value")
		if name == nil || name.Kind() != "identifier" || value == nil {
			return compactionOperation{}, false
		}
		projected := argument(statements[1].NamedChild(0), "text")
		if inner := argument(projected, "JSON.stringify"); inner != nil {
			projected = inner
		}
		if projected != nil && projected.Kind() == "call_expression" {
			projectionAddsMetadata = true

			function, args := projected.ChildByFieldName("function"), projected.ChildByFieldName("arguments")
			if function == nil || function.Utf8Text(bytes) != "Object.assign" || args == nil || args.NamedChildCount() != 3 ||
				args.NamedChild(0).Utf8Text(bytes) != "{}" {
				return compactionOperation{}, false
			}
			rawMetadata, ok := compactionStaticJSONObject(args.NamedChild(2), bytes)
			if !ok {
				return compactionOperation{}, false
			}
			var metadata map[string]json.RawMessage
			if json.Unmarshal(rawMetadata, &metadata) != nil || metadata == nil {
				return compactionOperation{}, false
			}
			for key := range metadata {
				if key != "retained" && key != "script_ref" {
					return compactionOperation{}, false
				}
			}
			projected = args.NamedChild(1)
		}
		if projected == nil || projected.Kind() != "identifier" || projected.Utf8Text(bytes) != name.Utf8Text(bytes) {
			return compactionOperation{}, false
		}
		awaited = value
	default:
		return compactionOperation{}, false
	}
	if awaited == nil || awaited.Kind() != "await_expression" || awaited.NamedChildCount() != 1 {
		return compactionOperation{}, false
	}
	for _, tool := range []string{"exec_command", "write_stdin"} {
		args := argument(awaited.NamedChild(0), "tools."+tool)
		if args == nil {
			continue
		}
		raw, ok := compactionStaticJSONObject(args, bytes)
		if ok {
			return compactionOperation{tool: tool, arguments: raw, notice: notice}, true
		}
	}
	if notice == nil && !projectionAddsMetadata {
		for _, tool := range []string{
			compactionDocsSearchTool,
			compactionDocsFetchTool,
			compactionDocsOpenAPITool,
		} {
			args := argument(awaited.NamedChild(0), "tools."+tool)
			if args == nil {
				continue
			}

			raw, ok := compactionStaticJSONObject(args, bytes)
			if ok {
				return compactionOperation{tool: tool, arguments: raw}, true
			}
		}
	}
	return compactionOperation{}, false
}

func compactionStaticJSONObject(node *sitter.Node, source []byte) (json.RawMessage, bool) {
	if node == nil || node.Kind() != "object" {
		return nil, false
	}

	object := make(map[string]json.RawMessage)
	pairs := 0
	for index := range node.NamedChildCount() {
		pair := node.NamedChild(uint(index))
		if pair.Kind() != "pair" {
			return nil, false
		}
		keyNode, valueNode := pair.ChildByFieldName("key"), pair.ChildByFieldName("value")
		key, ok := compactionStaticJSONKey(keyNode, source)
		if !ok || key == "__proto__" {
			return nil, false
		}
		if _, duplicate := object[key]; duplicate {
			return nil, false
		}
		value, ok := compactionStaticJSONValue(valueNode, source)
		if !ok {
			return nil, false
		}
		object[key] = value
		pairs++
	}
	if !compactionStaticJSONCommaCount(node, pairs) {
		return nil, false
	}
	return mustMarshalJSON(object), true
}

func compactionStaticJSONKey(node *sitter.Node, source []byte) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind() {
	case "string":
		literal := node.Utf8Text(source)
		if !compactionStaticJSONSurrogatesPaired(literal) {
			return "", false
		}
		var key string
		if json.Unmarshal([]byte(literal), &key) != nil {
			return "", false
		}
		return key, true
	case "property_identifier":
		key := node.Utf8Text(source)
		for index, character := range key {
			if character == '_' || character == '$' ||
				character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				index > 0 && character >= '0' && character <= '9' {
				continue
			}
			return "", false
		}
		return key, key != ""
	default:
		return "", false
	}
}

func compactionStaticJSONValue(node *sitter.Node, source []byte) (json.RawMessage, bool) {
	if node == nil {
		return nil, false
	}
	switch node.Kind() {
	case "object":
		return compactionStaticJSONObject(node, source)
	case "array":
		values := make([]json.RawMessage, 0, node.NamedChildCount())
		for index := range node.NamedChildCount() {
			value, ok := compactionStaticJSONValue(node.NamedChild(uint(index)), source)
			if !ok {
				return nil, false
			}
			values = append(values, value)
		}
		if !compactionStaticJSONCommaCount(node, len(values)) {
			return nil, false
		}
		return mustMarshalJSON(values), true
	case "string", "true", "false", "null":
		raw := json.RawMessage(node.Utf8Text(source))
		if !json.Valid(raw) {
			return nil, false
		}
		return append(json.RawMessage(nil), raw...), true
	case "number", "unary_expression":
		raw := json.RawMessage(node.Utf8Text(source))
		if !json.Valid(raw) || !compactionStaticJSONNumberIsFaithful(string(raw)) {
			return nil, false
		}
		return append(json.RawMessage(nil), raw...), true
	default:
		return nil, false
	}
}

func compactionStaticJSONNumberIsFaithful(raw string) bool {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return false
	}
	canonical := strconv.FormatFloat(value, 'g', -1, 64)
	originalValue, originalOK := new(big.Rat).SetString(raw)
	canonicalValue, canonicalOK := new(big.Rat).SetString(canonical)
	return originalOK && canonicalOK && originalValue.Cmp(canonicalValue) == 0
}

func compactionStaticJSONSurrogatesPaired(literal string) bool {
	for index := 1; index < len(literal)-1; {
		if literal[index] != '\\' {
			index++
			continue
		}
		if index+1 >= len(literal)-1 || literal[index+1] != 'u' {
			index += 2
			continue
		}
		if index+6 > len(literal)-1 {
			return false
		}
		value, err := strconv.ParseUint(literal[index+2:index+6], 16, 16)
		if err != nil {
			return false
		}
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			index += 6
			continue
		}
		next := index + 6
		if next+6 > len(literal)-1 || literal[next] != '\\' || literal[next+1] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(literal[next+2:next+6], 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index = next + 6
	}
	return true
}

func compactionStaticJSONCommaCount(node *sitter.Node, values int) bool {
	commas := 0
	for index := range node.ChildCount() {
		child := node.Child(uint(index))
		if child != nil && child.Kind() == "," {
			commas++
		}
	}
	return commas == max(0, values-1)
}
