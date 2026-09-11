package router

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const testDocsSearchTool = "mcp__openaiDeveloperDocs__search_openai_docs"

func readToolSource(tool, arguments string) string {
	return "const result = await tools." + tool + "(" + arguments + "); text(result);"
}

func readToolResultOutput(id, header string, result map[string]any) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": id,
		"output": []any{
			map[string]any{"type": "input_text", "text": header},
			map[string]any{"type": "input_text", "text": string(mustMarshalJSON(result))},
		},
	})
}

func readToolHistory(t *testing.T, operationIndex int, tool, source string, output json.RawMessage) []json.RawMessage {
	t.Helper()
	id := fmt.Sprintf("operation_%02d", operationIndex)
	items := retirementHistory()
	foundCall, foundOutput := false, false
	for index, raw := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || jsonString(fields, "call_id") != id {
			continue
		}
		switch jsonString(fields, "type") {
		case "function_call", "custom_tool_call":
			items[index] = mustMarshalJSON(map[string]any{
				"type": "custom_tool_call", "name": "exec", "call_id": id, "input": source,
			})
			foundCall = true
		case "function_call_output", "custom_tool_call_output":
			items[index] = output
			foundOutput = true
		}
	}
	if !foundCall || !foundOutput {
		t.Fatalf("history did not contain call/result pair %q", id)
	}
	return items
}

func TestCompactionCodeModeRecognizesOnlyFaithfulDocumentationReads(t *testing.T) {
	tools := []string{
		testDocsSearchTool,
		"mcp__openaiDeveloperDocs__fetch_openai_doc",
		"mcp__openaiDeveloperDocs__get_openapi_spec",
	}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			arguments := `{"query":"Responses API"}`
			for _, source := range []string{
				readToolSource(tool, arguments),
				"text(JSON.stringify(await tools." + tool + "(" + arguments + ")));",
			} {
				operation, ok := compactionCodeModeOperation(source)
				if !ok || operation.tool != tool || string(operation.arguments) != arguments {
					t.Fatalf("faithful static documentation read was not recognized: %#v", operation)
				}
			}
		})
	}

	for _, source := range []string{
		readToolSource("mcp__openaiDeveloperDocs__other", `{"query":"Responses API"}`),
		`const result = await tools.` + testDocsSearchTool + `({"query":"Responses API"}); text(result.content.map(value => value.text));`,
		`const result = await tools.` + testDocsSearchTool + `({"query":"Responses API"}); text(JSON.stringify(Object.assign({}, result, {"retained":true})));`,
		`const result = await tools.` + testDocsSearchTool + `({"query":"Responses API"}); await tools.other(); text(result);`,
	} {
		if _, ok := compactionCodeModeOperation(source); ok {
			t.Fatalf("unknown, transformed, or procedural documentation read was accepted: %s", source)
		}
	}
}

func TestCompactionCodeModeDecodesStaticObjectLiteralArguments(t *testing.T) {
	tests := []struct {
		name       string
		quoted     string
		unquoted   string
		wantTool   string
		wantResult string
	}{
		{
			name:       "write stdin",
			quoted:     `text(await tools.write_stdin({"session_id":42,"chars":""}));`,
			unquoted:   `text(await tools.write_stdin({session_id:42,chars:""}));`,
			wantTool:   "write_stdin",
			wantResult: `{"chars":"","session_id":42}`,
		},
		{
			name:       "documentation query",
			quoted:     `text(await tools.` + testDocsSearchTool + `({"query":"Responses API","limit":10}));`,
			unquoted:   `text(await tools.` + testDocsSearchTool + `({query:"Responses API",limit:10}));`,
			wantTool:   testDocsSearchTool,
			wantResult: `{"limit":10,"query":"Responses API"}`,
		},
		{
			name:       "nested JSON literals",
			quoted:     `text(await tools.exec_command({"cmd":"inspect","meta":{"enabled":true,"values":[1,null,false]}}));`,
			unquoted:   `text(await tools.exec_command({cmd:"inspect",meta:{enabled:true,values:[1,null,false]}}));`,
			wantTool:   "exec_command",
			wantResult: `{"cmd":"inspect","meta":{"enabled":true,"values":[1,null,false]}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, source := range []string{test.quoted, test.unquoted} {
				operation, ok := compactionCodeModeOperation(source)
				if !ok || operation.tool != test.wantTool || string(operation.arguments) != test.wantResult {
					t.Fatalf("static object literal was not decoded faithfully: %#v", operation)
				}
			}
		})
	}

	metadata := `const result = await tools.exec_command({cmd:"pwd"}); ` +
		`text(JSON.stringify(Object.assign({}, result, {retained:true,script_ref:"@shell/static"})));`
	if operation, ok := compactionCodeModeOperation(metadata); !ok ||
		operation.tool != "exec_command" || string(operation.arguments) != `{"cmd":"pwd"}` {
		t.Fatalf("static generated metadata was not decoded: %#v", operation)
	}

	for _, arguments := range []string{
		`{query:"x",...other}`,
		`{["query"]:"x"}`,
		`{get query(){return "x"}}`,
		`{query(){return "x"}}`,
		`{query}`,
		`{query:buildQuery()}`,
		"{query:`value-${suffix}`}",
		`{query:"first",query:"second"}`,
		`{__proto__:{polluted:true},query:"x"}`,
		`{query:{value:one}}`,
		`{query:'single quoted'}`,
	} {
		source := `text(await tools.` + testDocsSearchTool + `(` + arguments + `));`
		if _, ok := compactionCodeModeOperation(source); ok {
			t.Fatalf("dynamic or special-semantics object literal was accepted: %s", arguments)
		}
	}
}
func TestCompactionCodeModeRejectsNonfaithfulStaticJSONLiterals(t *testing.T) {
	for _, arguments := range []string{
		`{value:0.1}`,
		`{value:1.5}`,
		`{value:1e3}`,
		`{"\ud83d\ude00":"paired surrogate"}`,
	} {
		source := `text(await tools.` + testDocsSearchTool + `(` + arguments + `));`
		if _, ok := compactionCodeModeOperation(source); !ok {
			t.Fatalf("ordinary static literal was rejected: %s", arguments)
		}
	}

	for _, arguments := range []string{
		`{value:1.0000000000000001}`,
		`{value:9007199254740993}`,
		`{value:1e400}`,
		`{"\ud800":"lone high surrogate"}`,
		`{"\udc00":"lone low surrogate"}`,
	} {
		source := `text(await tools.` + testDocsSearchTool + `(` + arguments + `));`
		if _, ok := compactionCodeModeOperation(source); ok {
			t.Fatalf("nonfaithful static literal was accepted: %s", arguments)
		}
	}
}

func TestCompactionRetiresSuccessfulDocumentationSearch(t *testing.T) {
	arguments := `{"query":"Responses API","limit":10}`
	source := readToolSource(testDocsSearchTool, arguments)
	body := strings.Repeat("large unmarked search snippet\n", 1200)
	result := map[string]any{
		"isError":           false,
		"_meta":             map[string]any{"request_id": "docs-request-17", "source": "OpenAI developer documentation"},
		"structuredContent": map[string]any{"count": 1, "next_cursor": "cursor-2"},
		"content": []any{map[string]any{
			"type":        "text",
			"annotations": map[string]any{"audience": []string{"assistant"}, "priority": 0.8},
			"text": string(mustMarshalJSON(map[string]any{
				"count": 1, "outcome": "success",
				"results": []any{map[string]any{
					"id": "responses-guide", "title": "Responses API guide",
					"url":      "https://developers.openai.com/api/docs/guides/responses",
					"citation": "【responses-guide】", "snippet": body,
					"snippets": []any{map[string]any{
						"source_url": "https://developers.openai.com/api/docs/reference/responses",
						"citation":   "【responses-reference】",
						"text":       body + "\nWARNING: preview search index",
					}},
				}},
			})),
		}},
	}
	output := readToolResultOutput("operation_00", "Script completed\nWall time 0.1 seconds\nOutput:\n", result)
	items := readToolHistory(t, 0, testDocsSearchTool, source, output)
	got := retireCompactionOperations(items)
	wire := string(mustMarshalJSON(got))

	if string(got[2]) == string(items[2]) || string(got[3]) == string(items[3]) {
		t.Fatal("eligible documentation read was not retired")
	}
	for _, evidence := range []string{
		"successful read-tool return", "Responses API", "docs-request-17", "isError",
		"OpenAI developer documentation", "cursor-2", "responses-guide",
		"https://developers.openai.com/api/docs/guides/responses", "【responses-guide】",
		"https://developers.openai.com/api/docs/reference/responses", "【responses-reference】",
		"WARNING: preview search index", "priority", "0.8",
	} {
		if !strings.Contains(wire, evidence) {
			t.Fatalf("documentation read evidence %q was lost", evidence)
		}
	}
	if strings.Count(wire, "large unmarked search snippet") > 4 {
		t.Fatal("bulky search snippet was not retired")
	}

	operation, ok := compactionCodeModeOperation(source)
	if !ok || contextCompactionCanonicalJSON(operation.arguments) != contextCompactionCanonicalJSON(json.RawMessage(arguments)) {
		t.Fatal("exact documentation read invocation was not retained")
	}
}
func TestCompactionRetiresActualDocumentationSearchSchema(t *testing.T) {
	body := strings.Repeat("unmarked Algolia search body\n", 900)
	search := map[string]any{
		"hits": []any{map[string]any{
			"url":                "https://developers.openai.com/api/docs/guides/responses#streaming",
			"url_without_anchor": "https://developers.openai.com/api/docs/guides/responses",
			"anchor":             "streaming",
			"content":            body,
			"type":               "lvl2",
			"hierarchy":          map[string]any{"lvl0": "Guides", "lvl1": "Responses", "lvl2": "Streaming"},
			"objectID":           "docs-responses-streaming",
			"_snippetResult": map[string]any{
				"content": map[string]any{
					"value":      body + "WARNING: streaming preview behavior\n",
					"matchLevel": "full", "matchedWords": []string{"streaming"},
					"annotation_extension": "snippet-metadata",
				},
			},
			"_highlightResult": map[string]any{
				"content": map[string]any{
					"value": body, "matchLevel": "partial", "fullyHighlighted": false,
				},
				"hierarchy": map[string]any{
					"lvl0": map[string]any{"value": "Guides", "matchLevel": "none"},
					"lvl1": map[string]any{"value": "Responses", "matchLevel": "full"},
				},
			},
			"unknown_hit_metadata": map[string]any{"rank": 7, "source": "developer-docs-index"},
		}},
		"nbHits": 1, "page": 2, "nextCursor": "algolia-cursor-3",
		"unknown_root_metadata": map[string]any{"processingTimeMS": 4},
	}
	result := map[string]any{
		"_meta": map[string]any{"request_id": "algolia-request-4"},
		"content": []any{map[string]any{
			"type":        "text",
			"annotations": map[string]any{"audience": []string{"assistant"}, "priority": 0.9},
			"text":        string(mustMarshalJSON(search)),
		}},
	}
	source := readToolSource(testDocsSearchTool, `{"query":"streaming","cursor":"algolia-cursor-2"}`)
	items := readToolHistory(t, 0, testDocsSearchTool, source,
		readToolResultOutput("operation_00", "Script completed\nWall time 0.1 seconds\nOutput:\n", result))
	wire := string(mustMarshalJSON(retireCompactionOperations(items)))

	for _, evidence := range []string{
		"successful read-tool return",
		"https://developers.openai.com/api/docs/guides/responses#streaming",
		"https://developers.openai.com/api/docs/guides/responses",
		"streaming", "lvl2", "Guides", "Responses", "docs-responses-streaming",
		"_snippetResult", "_highlightResult", "matchLevel", "matchedWords",
		"fullyHighlighted", "snippet-metadata", "unknown_hit_metadata",
		"developer-docs-index", "nbHits", "page", "algolia-cursor-3",
		"unknown_root_metadata", "processingTimeMS", "algolia-request-4",
		"annotations", "audience", "priority", "WARNING: streaming preview behavior",
	} {
		if !strings.Contains(wire, evidence) {
			t.Fatalf("actual search-schema evidence %q was lost", evidence)
		}
	}
	if strings.Count(wire, "unmarked Algolia search body") > 6 {
		t.Fatal("actual search-schema body and annotation copies were not retired")
	}
}

func TestCompactionSearchBodyThresholdsAndIdentities(t *testing.T) {
	t.Run("decoded short escaped string", func(t *testing.T) {
		items := []map[string]json.RawMessage{{
			"id":      mustMarshalJSON("result-1"),
			"snippet": mustMarshalJSON(strings.Repeat("\"", 200)),
		}}
		if _, changed := compactionRetiredSearchItemMaps(items); changed {
			t.Fatal("short decoded snippet was retired because its JSON encoding was large")
		}
	})

	t.Run("mixed string array", func(t *testing.T) {
		const short = "keep this short snippet"
		items := []map[string]json.RawMessage{{
			"id":       mustMarshalJSON("result-1"),
			"snippets": mustMarshalJSON([]string{strings.Repeat("large body ", 40), short}),
		}}
		reduced, changed := compactionRetiredSearchItemMaps(items)
		if !changed {
			t.Fatal("large snippet was not retired")
		}
		var decoded []map[string]json.RawMessage
		var snippets []string
		if json.Unmarshal(reduced, &decoded) != nil || json.Unmarshal(decoded[0]["snippets"], &snippets) != nil ||
			len(snippets) != 2 || snippets[1] != short || !strings.Contains(snippets[0], "historical document body retired") {
			t.Fatalf("mixed snippets were not reduced conservatively: %s", reduced)
		}
	})

	t.Run("invalid identity", func(t *testing.T) {
		for _, identity := range []any{"", false, map[string]any{"url": "opaque"}} {
			items := []map[string]json.RawMessage{{
				"url":     mustMarshalJSON(identity),
				"snippet": mustMarshalJSON(strings.Repeat("unidentified body ", 40)),
			}}
			if _, changed := compactionRetiredSearchItemMaps(items); changed {
				t.Fatalf("body with unusable identity %v was retired", identity)
			}
		}
	})
}

func TestCompactionSuccessfulReadWithoutBodyReductionStaysEligible(t *testing.T) {
	tool := compactionDocsFetchTool
	source := readToolSource(tool, `{"url":"https://developers.openai.com/api/docs/index"}`)
	result := map[string]any{
		"isError": false,
		"_meta":   map[string]any{"request_id": "short-read-2"},
		"content": []any{map[string]any{
			"type": "text", "text": "Short successful documentation result.", "annotations": map[string]any{"priority": 1},
		}},
	}
	output := readToolResultOutput("operation_00", "Script completed\nWall time 0.1 seconds\nOutput:\n", result)

	var fields map[string]json.RawMessage
	if json.Unmarshal(output, &fields) != nil {
		t.Fatal("invalid test output")
	}
	reduced, ok := compactionRetiredReadToolOutput(fields["output"], tool)
	if !ok || string(reduced) != string(fields["output"]) {
		t.Fatal("known successful short read did not remain eligible with its exact output")
	}

	items := readToolHistory(t, 0, tool, source, output)
	items = append(items[:4], append([]json.RawMessage{
		compactTestCall("large_companion", "hread large.go"),
		compactTestOutput("large_companion", strings.Repeat("unmarked companion body\n", 600), 0),
	}, items[4:]...)...)
	got := retireCompactionOperations(items)
	for _, index := range []int{2, 3, 4, 5} {
		if string(got[index]) == string(items[index]) {
			t.Fatalf("profitable complete group was only partially retired at %d", index)
		}
	}
	wire := string(mustMarshalJSON(got))
	for _, evidence := range []string{
		"Short successful documentation result.", "short-read-2",
		"https://developers.openai.com/api/docs/index", "large_companion",
	} {
		if !strings.Contains(wire, evidence) {
			t.Fatalf("profitable group lost successful read evidence %q", evidence)
		}
	}
}

func TestCompactionRetiresDocumentationBodiesConservatively(t *testing.T) {
	t.Run("fetched markdown", func(t *testing.T) {
		tool := "mcp__openaiDeveloperDocs__fetch_openai_doc"
		source := readToolSource(tool, `{"url":"https://developers.openai.com/api/docs/guides/responses"}`)
		markdown := "# Responses API\nSource: OpenAI developer documentation\n" +
			"https://developers.openai.com/api/docs/guides/responses\n" +
			"Citation: 【responses-fetch】\n\n## Create a response\n" +
			strings.Repeat("technical example body with request fields\n", 1200) +
			"WARNING: preview behavior may change\n"
		result := map[string]any{
			"_meta":   map[string]any{"request_id": "fetch-request-9"},
			"content": []any{map[string]any{"type": "text", "text": markdown}},
		}
		items := readToolHistory(t, 0, tool, source,
			readToolResultOutput("operation_00", "Script completed\nWall time 0.1 seconds\nOutput:\n", result))
		wire := string(mustMarshalJSON(retireCompactionOperations(items)))
		for _, evidence := range []string{
			"successful read-tool return", "# Responses API", "Source: OpenAI developer documentation",
			"https://developers.openai.com/api/docs/guides/responses", "【responses-fetch】",
			"## Create a response", "WARNING: preview behavior may change", "fetch-request-9",
		} {
			if !strings.Contains(wire, evidence) {
				t.Fatalf("fetched-document evidence %q was lost", evidence)
			}
		}
		if strings.Count(wire, "technical example body") > 2 {
			t.Fatal("fetched technical body was not retired")
		}
	})

	t.Run("OpenAPI document", func(t *testing.T) {
		tool := "mcp__openaiDeveloperDocs__get_openapi_spec"
		source := readToolSource(tool, `{"url":"https://api.openai.com/v1/responses","languages":["javascript"]}`)
		spec := map[string]any{
			"openapi": "3.1.0",
			"info":    map[string]any{"title": "OpenAI API", "version": "2026-09-01"},
			"servers": []any{map[string]any{"url": "https://api.openai.com/v1"}},
			"paths": map[string]any{
				"/responses": map[string]any{"post": map[string]any{
					"operationId": "createResponse", "summary": "Create a response",
					"description": strings.Repeat("large endpoint description ", 1200),
					"responses":   map[string]any{"200": map[string]any{"description": strings.Repeat("large schema ", 1200)}},
				}},
			},
			"components": map[string]any{"schemas": map[string]any{"Response": map[string]any{
				"description": strings.Repeat("large component schema ", 1200),
			}}},
		}
		result := map[string]any{
			"_meta":   map[string]any{"source": "OpenAI OpenAPI"},
			"content": []any{map[string]any{"type": "text", "text": string(mustMarshalJSON(spec))}},
		}
		items := readToolHistory(t, 0, tool, source,
			readToolResultOutput("operation_00", "Script completed\nWall time 0.1 seconds\nOutput:\n", result))
		wire := string(mustMarshalJSON(retireCompactionOperations(items)))
		for _, evidence := range []string{
			"successful read-tool return", "3.1.0", "OpenAI API", "2026-09-01",
			"https://api.openai.com/v1", "/responses", "createResponse", "Create a response",
			"OpenAI OpenAPI",
		} {
			if !strings.Contains(wire, evidence) {
				t.Fatalf("OpenAPI evidence %q was lost", evidence)
			}
		}
		for _, body := range []string{"large endpoint description", "large schema", "large component schema"} {
			if strings.Contains(wire, body) {
				t.Fatalf("OpenAPI body %q was not retired", body)
			}
		}
	})
}
func TestCompactionDocumentationReadFailuresStayNative(t *testing.T) {
	goodSource := readToolSource(testDocsSearchTool, `{"query":"Responses API"}`)
	goodResult := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": `{"results":[{"url":"https://developers.openai.com","snippet":"body"}]}`}},
	}
	cases := []struct {
		name   string
		source string
		output json.RawMessage
	}{
		{"reported error", goodSource, readToolResultOutput("operation_00", "Script completed\n", map[string]any{
			"isError": true, "content": []any{map[string]any{"type": "text", "text": "permission error: citation unavailable"}},
		})},
		{"unknown error flag", goodSource, readToolResultOutput("operation_00", "Script completed\n", map[string]any{
			"isError": "false", "content": []any{map[string]any{"type": "text", "text": "ambiguous status"}},
		})},
		{"null error flag", goodSource, readToolResultOutput("operation_00", "Script completed\n", map[string]any{
			"isError": nil, "content": []any{map[string]any{"type": "text", "text": "ambiguous null status"}},
		})},
		{"media", goodSource, readToolResultOutput("operation_00", "Script completed\n", map[string]any{
			"content": []any{map[string]any{"type": "image", "data": "opaque-media"}},
		})},
		{"failed header", goodSource, readToolResultOutput("operation_00", "Script failed\n", goodResult)},
		{"live header", goodSource, readToolResultOutput("operation_00", "Script running\n", goodResult)},
		{"malformed result", goodSource, mustMarshalJSON(map[string]any{
			"type": "custom_tool_call_output", "call_id": "operation_00",
			"output": []any{
				map[string]any{"type": "input_text", "text": "Script completed\n"},
				map[string]any{"type": "input_text", "text": "not a CallToolResult"},
			},
		})},
		{"unknown tool", readToolSource("mcp__openaiDeveloperDocs__other", `{"query":"Responses API"}`),
			readToolResultOutput("operation_00", "Script completed\n", goodResult)},
		{"mapped projection", `const result = await tools.` + testDocsSearchTool + `({"query":"Responses API"}); text(result.content.map(value => value.text));`,
			readToolResultOutput("operation_00", "Script completed\n", goodResult)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			items := readToolHistory(t, 0, testDocsSearchTool, test.source, test.output)
			got := retireCompactionOperations(items)
			if string(got[2]) != string(items[2]) || string(got[3]) != string(items[3]) {
				t.Fatal("unsupported or unsuccessful documentation result was retired")
			}
		})
	}
}
func TestCompactionTruncatedDocumentationSearchProjectionStaysNative(t *testing.T) {
	source := readToolSource(testDocsSearchTool, `{"query":"responses streaming"}`)
	truncated := "Warning: truncated output (original token count exceeded)\nTotal output lines: 1\n\n" +
		`{"content":[{"type":"text","text":"{\"hits\":[{\"url\":\"https://developers.openai.com/api/docs/guides/responses#streaming\",` +
		`\"url_without_anchor\":\"https://developers.openai.com/api/docs/guides/responses\",\"anchor\":\"streaming\",` +
		`\"content\":\"` + strings.Repeat("truncated historical search body ", 1200) +
		`\",\"type\":\"lvl2\",\"hierarchy\":{\"lvl0\":\"Guides\"},\"objectID\":\"responses-streaming\",` +
		`\"_snippetResult\":{\"content\":{\"value\":\"incomplete`
	output := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00",
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": truncated},
		},
	})
	items := readToolHistory(t, 0, testDocsSearchTool, source, output)
	got := retireCompactionOperations(items)
	if string(got[2]) != string(items[2]) || string(got[3]) != string(items[3]) {
		t.Fatal("truncated non-JSON documentation projection was retired")
	}
	for _, evidence := range []string{
		"Warning: truncated output", "Total output lines: 1",
		"url_without_anchor", "_snippetResult", "truncated historical search body",
	} {
		if !strings.Contains(string(got[3]), evidence) {
			t.Fatalf("truncated projection evidence %q was lost", evidence)
		}
	}
}

func TestCompactionTruncatedDocumentationSearchRetiresCompleteBodySpan(t *testing.T) {
	source := readToolSource(testDocsSearchTool, `{"query":"responses streaming"}`)
	truncated := "Warning: truncated output (original token count exceeded)\nTotal output lines: 1\n\n" +
		`{"content":[{"type":"text","text":"{\"hits\":[{\"url\":\"https://developers.openai.com/api/docs/guides/responses\",` +
		`\"content\":\"# Responses guide\\n` + strings.Repeat(`unmarked historical body\\n`, 500) +
		`uncertain boundary\` + `…500 tokens truncated…` + `ncontinues here\\n` +
		strings.Repeat(`more unmarked historical body\\n`, 500) +
		`WARNING: retained limitation\\n\",\"future\":\"exact unknown metadata\"}]}"}],"future_result":"keep exact"}`
	output := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00",
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": truncated},
		},
	})
	items := readToolHistory(t, 0, testDocsSearchTool, source, output)
	got := retireCompactionOperations(items)
	if string(got[2]) == string(items[2]) || string(got[3]) == string(items[3]) {
		t.Fatal("complete recognized body span around client truncation stayed native")
	}
	wire := string(got[3])
	for _, evidence := range []string{
		"Warning: truncated output", "Total output lines: 1", "developers.openai.com",
		"uncertain boundary", "continues here", "WARNING: retained limitation",
		"exact unknown metadata", "future_result", "keep exact",
	} {
		if !strings.Contains(wire, evidence) {
			t.Fatalf("truncated projection evidence %q was lost", evidence)
		}
	}
	if strings.Count(wire, "unmarked historical body") > 2 {
		t.Fatal("unambiguous truncated historical body span was not retired")
	}
}

func TestCompactionReadBodyEvidenceBoundsOversizedLines(t *testing.T) {
	body := "# " + strings.Repeat("oversized documentation evidence ", 400) +
		"https://developers.openai.com END"
	evidence, ok := compactionRetiredDocumentText(body)
	if !ok || len(evidence) >= len(body)/2 ||
		!strings.Contains(evidence, "oversized retained documentation evidence line truncated") ||
		!strings.HasPrefix(evidence, "# ") || !strings.HasSuffix(evidence, " END") {
		t.Fatalf("oversized evidence line was not bounded with its edges intact: %d of %d bytes", len(evidence), len(body))
	}
}

func TestCompactionDocumentationReadProtectionAndRecentFrontier(t *testing.T) {
	result := map[string]any{
		"content": []any{map[string]any{
			"type": "text",
			"text": `{"results":[{"id":"citation-17","url":"https://developers.openai.com","snippet":"` +
				strings.Repeat("historical referenced body ", 500) + `"}]}`,
		}},
	}
	source := readToolSource(testDocsSearchTool, `{"query":"Responses API"}`)

	t.Run("referenced call", func(t *testing.T) {
		items := readToolHistory(t, 0, testDocsSearchTool, source,
			readToolResultOutput("operation_00", "Script completed\n", result))
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "content": "Continue from operation_00.",
		}))
		got := retireCompactionOperations(items)
		if string(got[2]) != string(items[2]) || string(got[3]) != string(items[3]) {
			t.Fatal("explicitly referenced documentation call was retired")
		}
	})

	t.Run("newest eight", func(t *testing.T) {
		items := readToolHistory(t, 16, testDocsSearchTool, source,
			readToolResultOutput("operation_16", "Script completed\n", result))
		before := string(mustMarshalJSON(items))
		got := retireCompactionOperations(items)
		for index, raw := range items {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil && jsonString(fields, "call_id") == "operation_16" &&
				string(got[index]) != string(raw) {
				t.Fatal("documentation read inside newest-eight frontier changed")
			}
		}
		if !strings.Contains(before, "historical referenced body") {
			t.Fatal("test setup lost the native body")
		}
	})
}
