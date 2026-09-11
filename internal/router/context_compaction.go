package router

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// reduceContextCompaction retains authority and the active frontier while
// reducing redundant evidence and retiring eligible finished operations under
// the explicit lossy retention policy. Token admission belongs to the selector.
func reduceContextCompaction(input []json.RawMessage) []json.RawMessage {
	return reduceContextCompactionWithPlan(input, compactionRetentionPlan{compactionRecentOperations, compactionRecentOperations})
}

func reduceContextCompactionWithPlan(input []json.RawMessage, plan compactionRetentionPlan) []json.RawMessage {
	// Clean transport metadata while stable native IDs are still present.
	// Narration reduction may remove an unreferenced ordinary-assistant ID,
	// after which the metadata pass must conservatively leave that item alone.
	input = reduceContextCompactionMetadata(input)
	input = reduceContextCompactionNarration(input)
	protected := contextCompactionReferencedResults(input)
	output := slices.Clone(input)
	type item struct {
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		CallID    string          `json:"call_id"`
		Arguments string          `json:"arguments"`
		Input     string          `json:"input"`
		Output    json.RawMessage `json:"output"`
	}
	items := make([]item, len(input))
	calls := make(map[string]int)
	duplicates := make(map[string]bool)
	lastResult := -1
	for index, raw := range input {
		if json.Unmarshal(raw, &items[index]) != nil {
			continue
		}
		current := items[index]
		switch current.Type {
		case "function_call", "custom_tool_call":
			if _, exists := calls[current.CallID]; exists {
				duplicates[current.CallID] = true
			}
			calls[current.CallID] = index
		case "function_call_output", "custom_tool_call_output":
			lastResult = index
		}
	}
	for index, current := range items {
		if index == lastResult || protected[current.CallID] || (current.Type != "function_call_output" && current.Type != "custom_tool_call_output") {
			continue
		}
		callIndex, exists := calls[current.CallID]
		if !exists || current.CallID == "" || duplicates[current.CallID] || callIndex >= index {
			continue
		}
		call := items[callIndex]
		command := call.Input
		switch {
		case call.Type == "function_call" && (call.Name == "exec_command" || call.Name == "functions.exec_command"):
			var args struct {
				Command string `json:"cmd"`
				Shell   string `json:"shell"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) != nil || (args.Shell != "" && args.Shell != "bash" && args.Shell != "/bin/bash") {
				continue
			}
			command = args.Command
		case call.Type == "custom_tool_call" && (call.Name == "shell" || call.Name == "functions.shell"):
			// Interpreter/header semantics belong to the shell runtime. Unknown
			// headers must not be interpreted as plain Bash by this reducer.
			if strings.HasPrefix(command, "#!") {
				continue
			}
		default:
			continue
		}
		kind := contextCompactionCommand(command)
		if kind == "" {
			continue
		}
		encode, text, ok := contextCompactionOutput(current.Output)
		if !ok {
			continue
		}
		reduced := text
		switch kind {
		case "go-test":
			var kept strings.Builder
			removed := 0
			for line := range strings.SplitAfterSeq(text, "\n") {
				if contextCompactionGoRoutine.MatchString(strings.TrimSuffix(line, "\n")) {
					removed++
				} else {
					kept.WriteString(line)
				}
			}
			if removed > 0 {
				reduced = fmt.Sprintf("[mekugi: omitted %d Go test progress/pass lines]\n%s", removed, kept.String())
			}
		case "search":
			// A later byte-identical output is explicit replacement evidence,
			// not an assumption that rerunning a search gives its old answer.
			// Keep the referenced read intact in this and subsequent compactions.
			if len(text) < 256 {
				continue
			}
			for later := index + 1; later < len(items); later++ {
				candidate := items[later]
				if candidate.Type != "function_call_output" || candidate.CallID == "" || duplicates[candidate.CallID] {
					continue
				}
				sourceIndex, exists := calls[candidate.CallID]
				if !exists || sourceIndex <= index || sourceIndex >= later {
					continue
				}
				source := items[sourceIndex]
				if source.Type != "function_call" || (source.Name != "exec_command" && source.Name != "functions.exec_command") {
					continue
				}
				var args struct {
					Command string `json:"cmd"`
					Shell   string `json:"shell"`
				}
				if json.Unmarshal([]byte(source.Arguments), &args) != nil || args.Shell != "" {
					continue
				}
				if contextCompactionCommand(args.Command) != "read" {
					continue
				}
				_, evidence, ok := contextCompactionOutput(candidate.Output)
				if ok && evidence == text {
					protected[candidate.CallID] = true
					reduced = fmt.Sprintf("[mekugi compaction: matching search listing retained verbatim in tool result %q; original command and successful exit status retained]\n", candidate.CallID)
					break
				}
			}
		}
		if len(reduced) >= len(text) {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(input[index], &fields) != nil {
			continue
		}
		fields["output"] = encode(reduced)
		output[index] = mustMarshalJSON(fields)
	}
	retained := reduceContextCompactionSourceWithFrontier(input,
		retireCompactionOperationsWithFrontier(reduceRepeatedCompactionRows(output, protected), plan.operations), plan.outputs)
	return consolidateContextCompactionRecords(input, retained)
}

// Accept structured results or Codex's native completed-exec header. Unknown
// formats, failures, and live handles stay untouched rather than guessing state.
// Native header source: Codex core/src/tools/context.rs response_header.
func contextCompactionOutput(raw json.RawMessage) (func(string) json.RawMessage, string, bool) {
	var serialized string
	if json.Unmarshal(raw, &serialized) != nil {
		return nil, "", false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(serialized), &envelope) != nil || envelope == nil {
		matches := contextCompactionNativeResult.FindStringSubmatch(serialized)
		if matches == nil {
			return nil, "", false
		}
		return func(text string) json.RawMessage { return mustMarshalJSON(matches[1] + text) }, matches[2], true
	}

	var exitCode *int
	var output string
	if json.Unmarshal(envelope["exit_code"], &exitCode) != nil || exitCode == nil || *exitCode != 0 ||
		json.Unmarshal(envelope["output"], &output) != nil {
		return nil, "", false
	}
	for _, key := range []string{"session_id", "cell_id"} {
		if value, exists := envelope[key]; exists && string(value) != "null" {
			return nil, "", false
		}
	}
	return func(text string) json.RawMessage {
		envelope["output"] = mustMarshalJSON(text)
		return mustMarshalJSON(string(mustMarshalJSON(envelope)))
	}, output, true

}

var contextCompactionNativeResult = regexp.MustCompile(`(?s)\A((?:Chunk ID: [^\r\n]+\n)?Wall time: [0-9]+(?:\.[0-9]+)? seconds\nProcess exited with code 0\n(?:Original token count: [0-9]+\n)?(?:Output|Final output):\n)(.*)\z`)

var contextCompactionGoRoutine = regexp.MustCompile(`^(=== (RUN|PAUSE|CONT) +\S+|[ \t]*--- PASS: \S+ \([0-9]+(\.[0-9]+)?s\))$`)

// Parse, never execute or expand dynamic shell syntax. Compound commands,
// redirections, wrappers, assignments, and substitutions are outside this
// initial reducer's evidence contract, even if they contain a familiar name.
func contextCompactionCommand(command string) string {
	program, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil || len(program.Stmts) != 1 {
		return ""
	}
	statement := program.Stmts[0]
	call, ok := statement.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) != 0 || len(statement.Redirs) != 0 ||
		statement.Negated || statement.Background || statement.Coprocess || statement.Disown {
		return ""
	}
	args := make([]string, len(call.Args))
	for index, word := range call.Args {
		static := true
		syntax.Walk(word, func(node syntax.Node) bool {
			switch node.(type) {
			case nil, *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
				return true
			default:
				static = false
				return false
			}
		})
		if !static {
			return ""
		}
		args[index], err = expand.Literal(nil, word)
		if err != nil {
			return ""
		}
	}
	switch args[0] {
	case "go":
		if len(args) > 1 && args[1] == "test" {
			return "go-test"
		}
	case "rg", "hgrep":
		for _, arg := range args[1:] {
			if arg == "--pre" || strings.HasPrefix(arg, "--pre=") {
				return ""
			}
		}
		return "search"
	case "find":
		for _, arg := range args[1:] {
			switch arg {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf":
				return ""
			}
		}
		return "search"

	case "cat", "hread":
		return "read"
	}
	return ""
}
