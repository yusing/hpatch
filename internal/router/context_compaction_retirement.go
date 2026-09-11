package router

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// A recent suffix is a continuity buffer, not evidence that older work is
// irrelevant. The user-approved loss contract applies to finished operations
// within an open task; it does not require semantic task-closure inference.
const compactionRecentOperations = 8

type compactionRetirement struct {
	call, result             int
	operation                compactionOperation
	output                   json.RawMessage
	rows                     map[string]bool
	ranges                   [][2]string
	callRecord, resultRecord json.RawMessage
	eligible                 bool
}

func retireCompactionOperations(input []json.RawMessage) []json.RawMessage {
	return retireCompactionOperationsWithFrontier(input, compactionRecentOperations)
}

func retireCompactionOperationsWithFrontier(input []json.RawMessage, recent int) []json.RawMessage {
	recent = max(1, recent)
	fields := make([]map[string]json.RawMessage, len(input))
	calls, results := make(map[string][]int), make(map[string][]int)
	var callOrder []int
	for index, raw := range input {
		_ = json.Unmarshal(raw, &fields[index])
		id := jsonString(fields[index], "call_id")
		switch jsonString(fields[index], "type") {
		case "function_call", "custom_tool_call":
			calls[id] = append(calls[id], index)
			callOrder = append(callOrder, index)
		case "function_call_output", "custom_tool_call_output":
			results[id] = append(results[id], index)
		}
	}
	if len(callOrder) <= recent {
		return input
	}
	cutoff := callOrder[len(callOrder)-recent]
	plans := make(map[string]*compactionRetirement)
	for id, positions := range calls {
		if id == "" || len(positions) != 1 || len(results[id]) != 1 || !compactionCallID.MatchString(id) {
			continue
		}
		call, result := positions[0], results[id][0]
		if call >= cutoff || result >= cutoff || result <= call {
			continue
		}
		if jsonString(fields[result], "type") != jsonString(fields[call], "type")+"_output" {
			continue
		}
		operation, ok := compactionOperationCall(fields[call])
		if !ok {
			continue
		}
		output, ok := compactionRetiredOutput(fields[result]["output"], operation)
		if !ok {
			continue
		}
		plan := &compactionRetirement{call: call, result: result, operation: operation, output: output, eligible: true}
		plans[id] = plan
	}
	metadataReferences := contextCompactionMetadataReferences(input)
	// Reasoning and the calls it precedes form an atomic provider-history
	// group. Never leave opaque reasoning attached to partially retired calls.
	type group struct {
		start, end      int
		ids             []string
		reasoningRecord json.RawMessage
		blocked         bool
	}
	var groups []group
	groupAt := make([]int, len(input))
	for index := range groupAt {
		groupAt[index] = -1
	}
	current := -1
	for index, item := range fields {
		kind, role := jsonString(item, "type"), jsonString(item, "role")
		boundary := kind == "reasoning" || (kind == "message" && (role == "user" || role == "developer" || role == "system"))
		if boundary {
			if current >= 0 {
				groups[current].end = index
			}
			current = -1
		}
		if kind == "reasoning" {
			groups = append(groups, group{start: index, end: len(input)})
			current = len(groups) - 1
		}
		groupAt[index] = current
		if current < 0 {
			continue
		}
		switch kind {
		case "function_call", "custom_tool_call":
			groups[current].ids = append(groups[current].ids, jsonString(item, "call_id"))
		case "reasoning", "message", "agent_message", "function_call_output", "custom_tool_call_output":
		default:
			groups[current].blocked = true
		}
	}
	// Retirement must not trade a smaller envelope for more model-visible
	// context. Evaluate the same strings the replay metric counts, including
	// the reasoning fact that replaces opaque reasoning for a complete group.
	measurePlan := func(plan *compactionRetirement) (json.RawMessage, json.RawMessage, int, int, bool) {
		callRecord := compactionRetiredCallWithReferences(fields[plan.call], plan.operation, metadataReferences)
		resultRecord := compactionRetiredResultWithReferences(fields[plan.result], plan.output, metadataReferences)
		beforeTokens, beforeOK := compactionVisibleStringTokens(input[plan.call], input[plan.result])
		afterTokens, afterOK := compactionVisibleStringTokens(callRecord, resultRecord)
		return callRecord, resultRecord,
			len(input[plan.call]) + len(input[plan.result]) - len(callRecord) - len(resultRecord),
			beforeTokens - afterTokens, beforeOK && afterOK
	}
	measureGroup := func(g *group, cache bool) (int, int, bool) {
		bytesSaved, tokensSaved := 0, 0
		for _, id := range g.ids {
			plan := plans[id]
			if plan == nil || !plan.eligible {
				return 0, 0, false
			}
			callRecord, resultRecord, planBytes, planTokens, ok := measurePlan(plan)
			if !ok {
				return 0, 0, false
			}
			if cache {
				plan.callRecord, plan.resultRecord = callRecord, resultRecord
			}
			bytesSaved += planBytes

			tokensSaved += planTokens
		}
		reasoningRecord := compactionRetiredReasoningWithReferences(fields[g.start], metadataReferences)
		if cache {
			g.reasoningRecord = reasoningRecord
		}
		beforeTokens, beforeOK := compactionVisibleStringTokens(input[g.start])
		afterTokens, afterOK := compactionVisibleStringTokens(reasoningRecord)
		return bytesSaved + len(input[g.start]) - len(reasoningRecord),
			tokensSaved + beforeTokens - afterTokens, beforeOK && afterOK
	}
	profitable := func(bytesSaved, tokensSaved int, ok bool) bool {
		return ok && bytesSaved > 0 && tokensSaved >= 0
	}
	for id, plan := range plans {
		if groupAt[plan.call] >= 0 {
			continue
		}
		_, _, bytesSaved, tokensSaved, ok := measurePlan(plan)
		if !profitable(bytesSaved, tokensSaved, ok) {
			delete(plans, id)
		}
	}
	for index := range groups {
		g := &groups[index]
		if bytesSaved, tokensSaved, ok := measureGroup(g, false); !profitable(bytesSaved, tokensSaved, ok) {
			g.blocked = true
		}
	}
	type referenceText struct {
		raw   json.RawMessage
		owner string
		rows  bool
	}
	var queue []referenceText
	enqueueOriginal := func(id string, plan *compactionRetirement) {
		queue = append(queue, referenceText{fields[plan.call]["input"], id, true},
			referenceText{fields[plan.call]["arguments"], id, true},
			referenceText{fields[plan.result]["output"], id, true})
	}
	revision := 0
	var pin func(string)
	pin = func(id string) {
		plan := plans[id]
		if plan == nil || !plan.eligible {
			return
		}
		plan.eligible = false
		revision++
		enqueueOriginal(id, plan)
		if g := groupAt[plan.call]; g >= 0 && !groups[g].blocked {
			groups[g].blocked = true
			for _, related := range groups[g].ids {
				pin(related)
			}
		}
	}
	// Earlier reducers have already exchanged evidence for these references.
	// Pin their targets before candidate-output scanning can erase the notes,
	// including references within one otherwise-retirable reasoning/tool group.
	// Re-read the reduced input so newly generated source-row notes count too.
	for id := range contextCompactionReferencedResults(input) {
		pin(id)
	}
	for _, g := range groups {
		blocked := g.blocked || g.end > cutoff
		for index := g.start; index < g.end; index++ {
			kind := jsonString(fields[index], "type")
			if kind == "function_call" || kind == "custom_tool_call" || kind == "function_call_output" || kind == "custom_tool_call_output" {
				p := plans[jsonString(fields[index], "call_id")]
				if p == nil || !p.eligible || p.call < g.start || p.result >= g.end {
					blocked = true
				}
			}
		}
		if blocked {
			for _, id := range g.ids {
				pin(id)
			}
		}
	}
	// Native response item IDs are aliases for their operation unit. They are
	// transport bookkeeping in retired records, but an explicit retained
	// reference to one must keep the original operation and reasoning group.
	aliasOwners := make(map[string][]string)
	addAlias := func(alias string, owners ...string) {
		if !compactionCallID.MatchString(alias) {
			return
		}
		for _, owner := range owners {
			if owner != "" {
				aliasOwners[alias] = append(aliasOwners[alias], owner)
			}
		}
	}
	for id, plan := range plans {
		addAlias(jsonString(fields[plan.call], "id"), id)
		addAlias(jsonString(fields[plan.result], "id"), id)
	}
	for _, g := range groups {
		addAlias(jsonString(fields[g.start], "id"), g.ids...)
	}
	unitOwners := make(map[string]map[string]bool)
	for id := range plans {
		unitOwners[id] = map[string]bool{id: true}
	}
	for _, g := range groups {
		for _, owner := range g.ids {
			if unitOwners[owner] == nil {
				continue
			}
			for _, related := range g.ids {
				unitOwners[owner][related] = true
			}
		}
	}

	// Explicit call references pin their original operation. Verified-row
	// references instead travel inside eligible factual completion records.
	// Do not infer that a filename mention needs every historical body.
	rowOwners := make(map[string][]string)
	unsafeRowOwners := make(map[string]bool)
	scriptOwners := make(map[string][]string)
	for id, plan := range plans {
		evidence := fields[plan.result]["output"]
		if plan.operation.notice != nil {
			// The verified notice is a reference consumer, not a source-row
			// producer. Only the execution result can supply its evidence.
			var parts []map[string]json.RawMessage
			_ = json.Unmarshal(evidence, &parts)
			evidence = parts[2]["text"]
		}
		unsafeEncoding := false
		compactionVisitReferenceStrings(evidence, func(text string) {
			compactionSourceVisitDecodedReferences(text, func(decoded string) {
				for _, row := range compactionRowReference.FindAllString(decoded, -1) {
					rowOwners[row] = append(rowOwners[row], id)
				}
				for _, reference := range compactionScriptReference.FindAllString(decoded, -1) {
					scriptOwners[reference] = append(scriptOwners[reference], id)
				}
			}, &unsafeEncoding)
			if unsafeEncoding {
				unsafeRowOwners[id] = true
			}
		})
		if plan.operation.notice != nil {
			queue = append(queue, referenceText{mustMarshalJSON(*plan.operation.notice), id, true})
		}
		if plan.eligible {
			queue = append(queue, referenceText{plan.operation.arguments, id, true}, referenceText{plan.output, id, false})
		}
	}
	for _, item := range fields {
		kind, id := jsonString(item, "type"), jsonString(item, "call_id")
		switch kind {
		case "function_call", "custom_tool_call":
			if p := plans[id]; p == nil || !p.eligible {
				// A retained failed/live carrier still contains the decoded
				// notice's references, even if execution never emitted it.
				if operation, ok := compactionOperationCall(item); ok && operation.notice != nil {
					queue = append(queue, referenceText{mustMarshalJSON(*operation.notice), id, true})
				}
				queue = append(queue, referenceText{item["input"], id, true}, referenceText{item["arguments"], id, true})
			}
		case "function_call_output", "custom_tool_call_output":
			if p := plans[id]; p == nil || !p.eligible {
				queue = append(queue, referenceText{item["output"], id, true})
			}
		default:
			queue = append(queue, referenceText{item["content"], "", true}, referenceText{item["summary"], "", true})
		}
	}
	retainRow := func(id, row string) {
		plan := plans[id]
		if plan == nil || !plan.eligible {
			return
		}
		if plan.rows == nil {
			plan.rows = make(map[string]bool)
		}
		plan.rows[row] = true
	}
	retainRange := func(id string, rowRange [2]string) {
		plan := plans[id]
		if plan == nil || !plan.eligible {
			return
		}
		plan.ranges = append(plan.ranges, rowRange)
	}

	seenIDs, seenRows, seenScripts := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	nextReference := 0
	visitReference := func(reference referenceText, text string) {
		idText := compactionRowReference.ReplaceAllString(text, "")
		for _, word := range compactionReferenceWord.FindAllString(idText, -1) {
			targets := append([]string{word}, aliasOwners[word]...)
			for _, id := range targets {
				if !unitOwners[reference.owner][id] && !seenIDs[id] {
					seenIDs[id] = true
					pin(id)
				}
			}
		}
		if !reference.rows {
			return
		}
		for _, script := range compactionScriptReference.FindAllString(text, -1) {
			if !seenScripts[script] {
				seenScripts[script] = true
				for _, id := range scriptOwners[script] {
					pin(id)
				}
			}
		}
		for _, match := range compactionSourceRangeReference.FindAllStringSubmatch(text, -1) {
			rowRange := [2]string{match[1], match[2]}
			for row, owners := range rowOwners {
				if !compactionSourceRowReferenced(row, nil, [][2]string{rowRange}) {
					continue
				}
				for _, id := range owners {
					retainRange(id, rowRange)
				}
			}
			for id := range unsafeRowOwners {
				retainRange(id, rowRange)
			}
		}
		for _, row := range compactionRowReference.FindAllString(text, -1) {
			if !seenRows[row] {
				seenRows[row] = true
				for _, id := range rowOwners[row] {
					retainRow(id, row)
				}
			}
			for id := range unsafeRowOwners {
				retainRow(id, row)
			}
		}
	}
	drainReferences := func() {
		for nextReference < len(queue) {
			reference := queue[nextReference]
			nextReference++
			unsafeEncoding := false
			compactionVisitReferenceStrings(reference.raw, func(text string) {
				compactionSourceVisitDecodedReferences(text, func(decoded string) {
					visitReference(reference, decoded)
				}, &unsafeEncoding)
			})
			if unsafeEncoding {
				// An unsupported colon or slash encoding can conceal any call,
				// row, range, or script identity. Keep every other candidate
				// rather than guess which evidence the retained text names.
				for id := range plans {
					if !unitOwners[reference.owner][id] {
						pin(id)
					}
				}
			}
		}
	}

	// Pinning and row restoration are monotonic. Repeat reference closure when
	// either exposes references that were absent from the proposed factual
	// record. At most one pass per retired candidate can restore content, so
	// this reaches a bounded stable result.
	for {
		beforeRevision := revision
		drainReferences()
		for id, plan := range plans {
			if !plan.eligible || (len(plan.rows) == 0 && len(plan.ranges) == 0) {
				continue
			}
			retained, ok := compactionRetiredOutputKeepingRows(
				fields[plan.result]["output"], plan.operation, plan.rows, plan.ranges)
			if !ok {
				return input
			}
			if string(plan.output) != string(retained) {
				plan.output = retained
				queue = append(queue, referenceText{retained, id, true})
				revision++
			}
		}

		for id, plan := range plans {
			if !plan.eligible || groupAt[plan.call] >= 0 {
				continue
			}
			callRecord, resultRecord, bytesSaved, tokensSaved, ok := measurePlan(plan)
			if !profitable(bytesSaved, tokensSaved, ok) {
				pin(id)
				continue
			}
			plan.callRecord, plan.resultRecord = callRecord, resultRecord
		}
		for index := range groups {
			g := &groups[index]
			if g.blocked || g.end > cutoff {
				continue
			}
			bytesSaved, tokensSaved, ok := measureGroup(g, true)
			if !profitable(bytesSaved, tokensSaved, ok) {
				g.blocked = true
				for _, id := range g.ids {
					pin(id)
				}
			}
		}
		if revision == beforeRevision {
			break
		}
	}

	output := slices.Clone(input)
	for _, plan := range plans {
		if plan.eligible {
			output[plan.call] = plan.callRecord
			output[plan.result] = plan.resultRecord
		}
	}
	for _, g := range groups {
		if !g.blocked && g.end <= cutoff && len(g.ids) > 0 {
			output[g.start] = g.reasoningRecord
		}
	}
	return output
}

func compactionVisitReferenceStrings(raw json.RawMessage, visit func(string)) {
	var root any
	if len(raw) == 0 || json.Unmarshal(raw, &root) != nil {
		return
	}

	queue := []any{root}
	decoded := make(map[string]bool)
	for len(queue) > 0 {
		value := queue[0]
		queue = queue[1:]
		switch value := value.(type) {
		case string:
			visit(value)
			if decoded[value] {
				continue
			}
			decoded[value] = true
			var nested any
			if json.Unmarshal([]byte(value), &nested) == nil {
				queue = append(queue, nested)
			}
		case []any:
			queue = append(queue, value...)
		case map[string]any:
			for _, nested := range value {
				queue = append(queue, nested)
			}
		}
	}
}

var (
	compactionCallID             = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	compactionReferenceWord      = regexp.MustCompile(`[A-Za-z0-9_-]+`)
	compactionScriptReference    = regexp.MustCompile(`@shell/[A-Za-z0-9_./:-]+`)
	compactionRowReference       = regexp.MustCompile(`\b[1-9][0-9]*:[0-9a-f]{4}\b`)
	compactionDiagnostic         = regexp.MustCompile(`(?i)\b(error|fail|failed|failure|warning|warn|skipped|skip|timeout|panic|fatal|denied|coverage|cancelled|canceled)\b|timed out|no matches|not found`)
	compactionTestOutcome        = regexp.MustCompile(`^(ok[ \t]|PASS$|FAIL|[?][ \t]|Go test:|Tests:|Test Files:|Test Suites:|Ran [0-9]+ tests|[0-9]+ (passed|failed|skipped))`)
	compactionPythonTraceback    = regexp.MustCompile(`^Traceback \(most recent call last\):[ \t]*\r?\n?$`)
	compactionNativeFailedResult = regexp.MustCompile(`(?s)\A((?:Chunk ID: [^\r\n]+\n)?Wall time: [0-9]+(?:\.[0-9]+)? seconds\nProcess exited with code (?:-[0-9]+|[1-9][0-9]*)\n(?:Original token count: [0-9]+\n)?(?:Output|Final output):\n)(.*)\z`)
)

func compactionRetiredText(text string) string {
	return compactionRetiredTextKeepingRows(text, nil, nil)
}

// compactionFailedOutput accepts only terminal nonzero shell results. A live
// handle or unknown completion shape is not evidence that an operation ended.
func compactionFailedOutput(raw json.RawMessage) (func(string) json.RawMessage, string, bool) {
	rewriteEnvelope := func(rawEnvelope json.RawMessage) (map[string]json.RawMessage, string, bool) {
		var serialized string
		if json.Unmarshal(rawEnvelope, &serialized) != nil {
			return nil, "", false
		}
		var result map[string]json.RawMessage
		if json.Unmarshal([]byte(serialized), &result) != nil || result == nil {
			return nil, "", false
		}
		var exitCode *int
		var output string
		if json.Unmarshal(result["exit_code"], &exitCode) != nil || exitCode == nil || *exitCode == 0 ||
			json.Unmarshal(result["output"], &output) != nil {
			return nil, "", false
		}
		for _, key := range []string{"session_id", "cell_id"} {
			if value, exists := result[key]; exists && string(value) != "null" {
				return nil, "", false
			}
		}
		return result, output, true
	}

	if result, output, ok := rewriteEnvelope(raw); ok {
		return func(text string) json.RawMessage {
			copyResult := maps.Clone(result)
			copyResult["output"] = mustMarshalJSON(text)
			return mustMarshalJSON(string(mustMarshalJSON(copyResult)))
		}, output, true
	}

	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) == nil && (len(parts) == 2 || len(parts) == 3) {
		header := jsonString(parts[0], "text")
		if jsonString(parts[0], "type") != "input_text" ||
			(!strings.HasPrefix(header, "Script completed\n") && !strings.HasPrefix(header, "Script failed")) {
			return nil, "", false
		}
		for _, part := range parts {
			if jsonString(part, "type") != "input_text" {
				return nil, "", false
			}
		}
		if result, output, ok := rewriteEnvelope(parts[len(parts)-1]["text"]); ok {
			return func(text string) json.RawMessage {
				copyResult := maps.Clone(result)
				copyResult["output"] = mustMarshalJSON(text)
				copyParts := slices.Clone(parts)
				copyParts[len(copyParts)-1] = maps.Clone(copyParts[len(copyParts)-1])
				copyParts[len(copyParts)-1]["text"] = mustMarshalJSON(string(mustMarshalJSON(copyResult)))
				return mustMarshalJSON(copyParts)
			}, output, true
		}
	}

	var native string
	if json.Unmarshal(raw, &native) == nil {
		if match := compactionNativeFailedResult.FindStringSubmatch(native); match != nil {
			return func(text string) json.RawMessage { return mustMarshalJSON(match[1] + text) }, match[2], true
		}
	}
	return nil, "", false
}

// Failed operations retain every unclassified line. Unreferenced verified
// source rows are the only historical bulk removed by the generic reducer.
func compactionRetiredFailedText(text string, referenced map[string]bool, ranges [][2]string) string {
	var result strings.Builder
	removed := 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		if compactionCompleteSourceRow.MatchString(line) && !compactionTextReferencesRows(line, referenced, ranges) {
			removed++
			continue
		}
		result.WriteString(line)
	}
	if removed == 0 {
		return text
	}
	return fmt.Sprintf("[mekugi: omitted %d unreferenced verified source rows from terminal failed output; failure remains unresolved]\n%s", removed, result.String())
}

func compactionRetiredTextKeepingRows(text string, referenced map[string]bool, ranges [][2]string) string {
	lines := strings.SplitAfter(text, "\n")
	keep := make([]bool, len(lines))
	for index, line := range lines {
		sourceRow := compactionCompleteSourceRow.MatchString(line)
		// Replacement notes remain dependencies even when their consumer retires.
		if compactionRetainedReference.MatchString(line) || compactionTextReferencesRows(line, referenced, ranges) {
			keep[index] = true
		}
		if sourceRow {
			continue
		}
		if compactionDiagnostic.MatchString(line) {
			for nearby := max(0, index-2); nearby < min(len(lines), index+3); nearby++ {
				keep[nearby] = true
			}
		}
		if compactionTestOutcome.MatchString(line) {
			keep[index] = true
		}
	}
	// Python exception messages, notes, and chains have no reliable generic
	// end marker. Keep the suffix rather than dropping an actionable detail.
	for start, line := range lines {
		if !compactionPythonTraceback.MatchString(line) {
			continue
		}
		for index := start; index < len(lines); index++ {
			keep[index] = true
		}
		break
	}

	var result strings.Builder
	fmt.Fprintf(&result, "[mekugi: output details omitted; unavailable; original bytes=%d]\n", len(text))
	for index, line := range lines {
		if keep[index] {
			result.WriteString(line)
		}
	}
	return result.String()
}

func compactionTextReferencesRows(text string, referenced map[string]bool, ranges [][2]string) bool {
	if len(referenced) == 0 && len(ranges) == 0 {
		return false
	}
	found, unsafeEncoding := false, false
	compactionVisitReferenceStrings(mustMarshalJSON(text), func(value string) {
		compactionSourceVisitDecodedReferences(value, func(decoded string) {
			for _, row := range compactionRowReference.FindAllString(decoded, -1) {
				if compactionSourceRowReferenced(row, referenced, ranges) {
					found = true
				}
			}
		}, &unsafeEncoding)
	})
	return found || unsafeEncoding
}

func compactionRetiredOutput(raw json.RawMessage, operation compactionOperation) (json.RawMessage, bool) {
	return compactionRetiredOutputKeepingRows(raw, operation, nil, nil)
}

func compactionRetiredOutputKeepingRows(raw json.RawMessage, operation compactionOperation, referenced map[string]bool, ranges [][2]string) (json.RawMessage, bool) {
	if compactionReadTool(operation.tool) {
		reduced, ok := compactionRetiredReadToolOutput(raw, operation.tool)
		if ok && (len(referenced) > 0 || len(ranges) > 0) {
			return raw, true
		}
		return reduced, ok
	}

	if _, text, ok := contextCompactionOutput(raw); ok && strings.HasPrefix(text, contextCompactionGoTestSummaryPrefix) {
		return raw, true
	}
	if _, text, ok := compactionFailedOutput(raw); ok && strings.HasPrefix(text, contextCompactionGoTestSummaryPrefix) {
		return raw, true
	}
	if operation.patchReport == "" && operation.notice == nil {
		if encode, text, ok := contextCompactionOutput(raw); ok {
			return encode(compactionRetiredTextKeepingRows(text, referenced, ranges)), true
		}
		if encode, text, ok := compactionFailedOutput(raw); ok {
			return encode(compactionRetiredFailedText(text, referenced, ranges)), true
		}
	}
	var parts []map[string]json.RawMessage
	resultIndex := 1
	if operation.notice != nil {
		resultIndex = 2
	}
	if json.Unmarshal(raw, &parts) != nil || len(parts) != resultIndex+1 ||
		jsonString(parts[0], "type") != "input_text" || !strings.HasPrefix(jsonString(parts[0], "text"), "Script completed\n") ||
		jsonString(parts[resultIndex], "type") != "input_text" {
		return nil, false
	}
	if operation.notice != nil && (jsonString(parts[1], "type") != "input_text" || jsonString(parts[1], "text") != *operation.notice) {
		return nil, false
	}
	if operation.patchReport != "" {
		// The generated report is emitted only after awaited application.
		if jsonString(parts[1], "text") != operation.patchReport {
			return nil, false
		}
		parts[1] = maps.Clone(parts[1])
		parts[1]["text"] = mustMarshalJSON(compactionRetiredPatchReport(operation.patchReport, referenced, ranges))
		return mustMarshalJSON(parts), true
	}
	encode, text, ok := contextCompactionOutput(parts[resultIndex]["text"])
	if !ok {
		return nil, false
	}
	parts[resultIndex]["text"] = encode(compactionRetiredTextKeepingRows(text, referenced, ranges))
	return mustMarshalJSON(parts), true
}

func compactionRetiredPatchReport(text string, referenced map[string]bool, ranges [][2]string) string {
	var result strings.Builder
	removed := 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		if compactionCompleteSourceRow.MatchString(line) && !compactionTextReferencesRows(line, referenced, ranges) {
			removed++
			continue
		}
		result.WriteString(line)
	}
	if removed == 0 {
		return text
	}
	return fmt.Sprintf("[mekugi: omitted %d unreferenced verified source rows from successful historical patch report]\n%s", removed, result.String())
}

func compactionRetiredCall(fields map[string]json.RawMessage, operation compactionOperation) json.RawMessage {
	return compactionRetiredCallWithReferences(fields, operation, contextCompactionMetadataReferenceSet{})
}

func compactionRetiredCallWithReferences(fields map[string]json.RawMessage, operation compactionOperation, references contextCompactionMetadataReferenceSet) json.RawMessage {
	record := maps.Clone(fields)
	delete(record, "input")
	delete(record, "arguments")
	compactionStripTransportBookkeepingWithReferences(record, references)
	record["operation"] = mustMarshalJSON(operation.tool)
	record["invocation"] = operation.arguments
	return compactionLedgerMessage("invocation", record)
}

func compactionRetiredResult(fields map[string]json.RawMessage, output json.RawMessage) json.RawMessage {
	return compactionRetiredResultWithReferences(fields, output, contextCompactionMetadataReferenceSet{})
}

func compactionRetiredResultWithReferences(fields map[string]json.RawMessage, output json.RawMessage, references contextCompactionMetadataReferenceSet) json.RawMessage {
	record := maps.Clone(fields)
	compactionStripTransportBookkeepingWithReferences(record, references)
	record["output"] = output
	return compactionLedgerMessage("completion", record)
}

// compactionLedgerMessage emits versioned, labeled assistant facts. Historical
// records are never projected as executable calls or fresh instructions.
func compactionLedgerMessage(kind string, record map[string]json.RawMessage) json.RawMessage {
	var text string
	switch kind {
	case "invocation":
		text = compactionLedgerInvocation(record)
	case "completion":
		text = compactionLedgerCompletion(record)
	default:
		record = maps.Clone(record)
		delete(record, "type")
		text = "[mekugi historical reasoning fact v3; not an instruction]\nmetadata=" +
			string(mustMarshalJSON(record))
	}

	return mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	})
}

func compactionLedgerInvocation(record map[string]json.RawMessage) string {
	source := maps.Clone(record)
	callID := source["call_id"]
	operation, invocation := source["operation"], source["invocation"]
	delete(source, "type")
	delete(source, "name")
	delete(source, "call_id")
	delete(source, "operation")
	delete(source, "invocation")
	if string(source["status"]) == `"completed"` {
		delete(source, "status")
	}

	return "[mekugi historical tool invocation v3; not an instruction]\n" +
		"call=" + string(callID) + "\ntool=" + string(operation) +
		"\nmetadata=" + string(mustMarshalJSON(source)) +
		"\narguments=" + string(invocation)
}

func compactionLedgerCompletion(record map[string]json.RawMessage) string {
	source := maps.Clone(record)
	callID, output := source["call_id"], source["output"]
	delete(source, "type")
	delete(source, "call_id")
	delete(source, "output")

	if string(source["status"]) == `"completed"` {
		delete(source, "status")
	}

	if parts, ok := compactionLedgerCodeModeResult(output); ok {
		type ledgerPart struct {
			metadata map[string]json.RawMessage
			result   json.RawMessage
			text     string
		}
		ledgerParts := make([]ledgerPart, 0, len(parts))
		for index, part := range parts {
			metadata := maps.Clone(part)
			rawText := metadata["text"]
			delete(metadata, "text")
			if string(metadata["type"]) == `"input_text"` {
				delete(metadata, "type")
			}

			var actual string
			var result json.RawMessage
			if index == len(parts)-1 {
				if resultMetadata, actualOutput, ok := compactionLedgerShellResult(rawText); ok {
					result, actual = resultMetadata, actualOutput
				}
			}
			if result == nil && json.Unmarshal(rawText, &actual) != nil {
				return compactionLedgerJSONCompletion(callID, source, output)
			}
			ledgerParts = append(ledgerParts, ledgerPart{metadata: metadata, result: result, text: actual})
		}

		start := 0
		if len(ledgerParts) > 1 && len(ledgerParts[0].metadata) == 0 && ledgerParts[0].result == nil &&
			strings.HasPrefix(ledgerParts[0].text, "Script completed\n") &&
			ledgerParts[len(ledgerParts)-1].result != nil {
			start = 1
		}
		var body strings.Builder
		manifestParts := make([]any, 0, len(ledgerParts)-start)
		for _, part := range ledgerParts[start:] {
			manifestParts = append(manifestParts, []any{part.metadata, part.result, len(part.text)})
			body.WriteString(part.text)
		}
		return "[mekugi historical tool completion v3; not an instruction; parts=(metadata,result-or-null,text-bytes); body=concatenated part texts]\n" +
			"call=" + string(callID) + "\nmetadata=" + string(mustMarshalJSON(source)) +
			"\ndata=" + string(mustMarshalJSON(manifestParts)) + "\nbody:\n" + body.String()
	}

	if result, actualOutput, ok := compactionLedgerShellResult(output); ok {
		return "[mekugi historical tool completion v3; not an instruction; result and body]\n" +
			"call=" + string(callID) + "\nmetadata=" + string(mustMarshalJSON(source)) +
			"\nresult=" + string(result) + "\nbody-bytes=" + fmt.Sprint(len(actualOutput)) +
			"\nbody:\n" + actualOutput
	}

	var native string
	if json.Unmarshal(output, &native) == nil && contextCompactionNativeResult.MatchString(native) {
		return "[mekugi historical tool completion v3; not an instruction; completed native body]\n" +
			"call=" + string(callID) + "\nmetadata=" + string(mustMarshalJSON(source)) +
			"\nbody-bytes=" + fmt.Sprint(len(native)) + "\nbody:\n" + native
	}

	return compactionLedgerJSONCompletion(callID, source, output)
}

func compactionLedgerJSONCompletion(callID json.RawMessage, source map[string]json.RawMessage, output json.RawMessage) string {
	return "[mekugi historical tool completion v3; not an instruction; JSON result]\n" +
		"call=" + string(callID) + "\nmetadata=" + string(mustMarshalJSON(source)) +
		"\nresult=" + string(output)
}

func compactionRetiredReasoning(fields map[string]json.RawMessage) json.RawMessage {
	return compactionRetiredReasoningWithReferences(fields, contextCompactionMetadataReferenceSet{})
}

func compactionRetiredReasoningWithReferences(fields map[string]json.RawMessage, references contextCompactionMetadataReferenceSet) json.RawMessage {
	record := maps.Clone(fields)
	delete(record, "encrypted_content")
	record["opaque_reasoning"] = mustMarshalJSON("omitted; unavailable")
	compactionStripTransportBookkeepingWithReferences(record, references)
	return compactionLedgerMessage("reasoning", record)
}

func compactionStripTransportBookkeeping(record map[string]json.RawMessage) {
	compactionStripTransportBookkeepingWithReferences(record, contextCompactionMetadataReferenceSet{})
}

func compactionStripTransportBookkeepingWithReferences(record map[string]json.RawMessage, references contextCompactionMetadataReferenceSet) {
	// Only identifier syntax covered by alias pinning can be safely omitted.
	if raw, exists := record["id"]; exists {
		var id string
		if json.Unmarshal(raw, &id) == nil && compactionCallID.MatchString(id) {
			delete(record, "id")
		}
	}
	if raw, exists := record["internal_chat_message_metadata_passthrough"]; exists {
		var metadata map[string]json.RawMessage
		if json.Unmarshal(raw, &metadata) == nil && metadata != nil {
			originalLen := len(metadata)
			_, hasTurnID := metadata["turn_id"]
			_, hasCreateTime := metadata["create_time"]
			if hasTurnID || hasCreateTime {
				metadata = maps.Clone(metadata)
			}
			for _, key := range []string{"turn_id", "create_time"} {
				value, exists := metadata[key]
				if exists && !compactionTransportMetadataReferenced(references, key, value) {
					delete(metadata, key)
				}
			}
			if len(metadata) != originalLen {
				if len(metadata) == 0 {
					delete(record, "internal_chat_message_metadata_passthrough")
				} else {
					record["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(metadata)
				}
			}
		}
	}
	for _, key := range []string{"turn_id", "create_time"} {
		value, exists := record[key]
		if exists && !compactionTransportMetadataReferenced(references, key, value) {
			delete(record, key)
		}
	}
}

func compactionTransportMetadataReferenced(references contextCompactionMetadataReferenceSet, key string, raw json.RawMessage) bool {
	reference, ok := contextCompactionMetadataReference(key, raw)
	if !ok || references.unsafe {
		return true
	}
	if slices.ContainsFunc(references.text, func(text string) bool {
		return strings.Contains(text, reference)
	}) {
		return true
	}
	return key == "create_time" && slices.Contains(references.numbers, reference)
}

func compactionLedgerShellResult(raw json.RawMessage) (json.RawMessage, string, bool) {
	var serialized string
	if json.Unmarshal(raw, &serialized) != nil {
		return nil, "", false
	}

	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(serialized), &result) != nil || result == nil {
		return nil, "", false
	}

	var exitCode *int
	var output string
	if json.Unmarshal(result["exit_code"], &exitCode) != nil || exitCode == nil ||
		json.Unmarshal(result["output"], &output) != nil {
		return nil, "", false
	}
	for _, key := range []string{"session_id", "cell_id"} {
		if value, exists := result[key]; exists && string(value) != "null" {
			return nil, "", false
		}
	}

	metadata := maps.Clone(result)
	delete(metadata, "output")
	return mustMarshalJSON(metadata), output, true
}

func compactionLedgerCodeModeResult(raw json.RawMessage) ([]map[string]json.RawMessage, bool) {
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || (len(parts) != 2 && len(parts) != 3) {
		return nil, false
	}
	for index, part := range parts {
		if jsonString(part, "type") != "input_text" {
			return nil, false
		}
		var text string
		if json.Unmarshal(part["text"], &text) != nil {
			return nil, false
		}
		if index == 0 && !strings.HasPrefix(text, "Script completed\n") {
			return nil, false
		}
	}
	return parts, true
}

var (
	compactionRetirementTokenOnce     sync.Once
	compactionRetirementTokenCodec    tokenizer.Codec
	compactionRetirementTokenCodecErr error
)

func compactionVisibleStringTokens(items ...json.RawMessage) (int, bool) {
	compactionRetirementTokenOnce.Do(func() {
		compactionRetirementTokenCodec, compactionRetirementTokenCodecErr = tokenizer.ForModel(tokenizer.GPT5)
	})
	if compactionRetirementTokenCodecErr != nil {

		return 0, false
	}
	total := 0
	for _, raw := range items {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return 0, false
		}
		var visit func(any) bool
		visit = func(current any) bool {
			switch current := current.(type) {
			case string:
				count, err := compactionRetirementTokenCodec.Count(current)
				if err != nil {
					return false
				}
				total += count
			case []any:
				for _, nested := range current {
					if !visit(nested) {
						return false
					}
				}
			case map[string]any:
				for key, nested := range current {
					if key != "encrypted_content" && !visit(nested) {
						return false
					}
				}
			}
			return true
		}
		if !visit(value) {
			return 0, false
		}
	}
	return total, true
}
