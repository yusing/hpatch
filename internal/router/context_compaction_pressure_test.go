package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactionPressureContentIndependent(t *testing.T) {
	for _, size := range []int{252_000, 282_000} {
		for _, shape := range []string{"newest-unknown-output", "live-exec", "failed-exec", "unique-prose", "user", "agent", "dynamic-script"} {
			t.Run(fmt.Sprintf("%s/%d", shape, size), func(t *testing.T) {
				var text strings.Builder
				for index := range size / 8 {
					fmt.Fprintf(&text, "Historical observation %d: distinct ordinary content.\n", index)
				}
				_, _ = compactionVisibleStringTokens()
				_, pieces, err := compactionRetirementTokenCodec.Encode(text.String())
				if err != nil || len(pieces) < size {
					t.Fatalf("fixture tokenization: %d: %v", len(pieces), err)
				}
				bulk := strings.Join(pieces[:size], "")
				authority := compactionPressureMessage("developer", "Only change the router. Never run deployments.")
				request := compactionPressureMessage("user", "Continue the current task. The migration remains unresolved.")
				history := func(bulk string) []json.RawMessage {
					input := []json.RawMessage{authority, request}
					switch shape {
					case "newest-unknown-output":
						input = append(input, mustMarshalJSON(map[string]any{"type": "function_call", "name": "unfamiliar_tool", "call_id": "unknown", "arguments": `{"path":"current.go"}`}),
							mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "unknown", "output": bulk}))
					case "live-exec":
						input = append(input, compactTestCall("live", "long-running-command"), mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "live", "output": string(mustMarshalJSON(map[string]any{"output": bulk, "session_id": 1234, "exit_code": nil}))}))
					case "failed-exec":
						input = append(input, compactTestCall("failed", "python job.py"), compactTestOutput("failed", "Traceback (most recent call last):\n"+bulk+"\nRuntimeError: transaction remains unresolved", 1))
					case "unique-prose":
						for index := range 20 {
							start, end := index*len(bulk)/20, (index+1)*len(bulk)/20
							input = append(input, compactionPressureMessage("assistant", bulk[start:end]))
						}
					case "user":
						input = append(input, compactionPressureMessage("user", "Current task constraints:\n"+bulk+"\nDo not change the public API."))
					case "agent":
						input = append(input, mustMarshalJSON(map[string]any{"type": "agent_message", "content": bulk}))
					case "dynamic-script":
						input = append(input, mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "dynamic", "input": "const result = await tools.chooseDynamically();\n" + bulk}),
							mustMarshalJSON(map[string]any{"type": "custom_tool_call_output", "call_id": "dynamic", "output": "Status unknown; do not assume success"}))
					}
					return input
				}
				input := history(bulk)
				before, ok := compactionVisibleStringTokens(input...)
				if !ok {
					t.Fatal("cannot measure input history")
				}
				original := mustMarshalJSON(input)
				parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input}))
				if err != nil {
					t.Fatal(err)
				}
				compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
				capsule, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
				if err != nil {
					t.Fatal(err)
				}
				// A fresh router instance must restore only the selected history,
				// including when the client also carries original no-ID messages.
				var carried []json.RawMessage
				for _, item := range input {
					if compactionCarriedMessage(item) {
						carried = append(carried, item)
					}
				}
				resumed := &contextCompactor{keyPath: compactor.keyPath}
				restored, err := resumed.restore(t.Context(), append(carried, capsule))
				if err != nil {
					t.Fatal(err)
				}
				after, ok := compactionVisibleStringTokens(restored...)
				if !ok || after < 20_000 || after > compactionTargetTokens+compactionOvershootTokens {
					t.Fatalf("budget missed: %d -> %d", before, after)
				}
				if !bytes.Equal(mustMarshalJSON(restored), parsed.fields["input"]) {
					t.Fatal("carried messages resurrected discarded content")
				}
				if !slices.ContainsFunc(restored, func(raw json.RawMessage) bool { return bytes.Equal(raw, authority) }) {
					t.Fatal("small current authority was not retained exactly")
				}
				if shape != "user" && !slices.ContainsFunc(restored, func(raw json.RawMessage) bool { return bytes.Equal(raw, request) }) {
					t.Fatal("small current task was not retained exactly")
				}
				if shape == "live-exec" && !bytes.Contains(mustMarshalJSON(restored), []byte("1234")) {
					t.Fatal("live session handle was lost")
				}
				if shape == "failed-exec" && !bytes.Contains(mustMarshalJSON(restored), []byte("transaction remains unresolved")) {
					t.Fatal("failure conclusion was lost")
				}
				if !bytes.Equal(original, mustMarshalJSON(input)) {
					t.Fatal("pressure selection mutated original history")
				}
				t.Logf("visible-string tokens: %d -> %d", before, after)
			})
		}
	}
}

func TestCompactionPressureCarriedTruncationAndRepeatedCompaction(t *testing.T) {
	user := compactionPressureMessage("user", "user-prefix "+strings.Repeat("large request ", 140_000)+" user-suffix")
	agent := mustMarshalJSON(map[string]any{"type": "agent_message", "content": "agent-prefix " + strings.Repeat("large report ", 50_000) + " agent-suffix"})
	input := []json.RawMessage{user, agent}
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
	if err != nil {
		t.Fatal(err)
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.sealSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	truncated := compactionPressureMessage("user", "user-prefix …200000 tokens truncated… user-suffix")
	fresh := compactionPressureMessage("developer", "Fresh restriction: do not deploy.")
	for iteration := range 3 {
		parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": []json.RawMessage{truncated, fresh, agent, capsule, compactionPressureMessage("assistant", strings.Repeat("more unique history ", 90_000))}}))
		if err != nil {
			t.Fatal(err)
		}
		capsule, err = compactor.prepare(t.Context(), &parsed, http.Header{}, true)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		restored, err := compactor.restore(t.Context(), []json.RawMessage{truncated, agent, capsule})
		if err != nil {
			t.Fatalf("iteration %d continuation: %v", iteration, err)
		}
		count, ok := compactionVisibleStringTokens(restored...)
		if !ok || count > compactionTargetTokens+compactionOvershootTokens {
			t.Fatalf("iteration %d resurrected history: %d", iteration, count)
		}
		if !bytes.Contains(mustMarshalJSON(restored), []byte("Fresh restriction: do not deploy.")) {
			t.Fatal("fresh authority disappeared")
		}
	}
}

func TestCompactionPressureManySmallMessages(t *testing.T) {
	var input []json.RawMessage
	for index := range 5000 {
		input = append(input, compactionPressureMessage("user", fmt.Sprintf("Historical instruction %d %s", index, strings.Repeat("detail ", 60))))
	}
	input = append(input, compactionPressureMessage("user", "Current request: finish the router change."))
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) >= len(input) || len(snapshot.Carried) == 0 {
		t.Fatal("pressure did not drop old items")
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.sealSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := compactor.restore(t.Context(), append(slices.Clone(input), capsule))
	if err != nil || !bytes.Equal(mustMarshalJSON(restored), mustMarshalJSON(snapshot.Items)) {
		t.Fatalf("dropped carried messages were not reconciled: %v", err)
	}
}

func TestCompactionPressureCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := pressureCompactionWorkingSet(ctx, []json.RawMessage{compactionPressureMessage("user", "request")}, compactionTargetTokens); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pressure compaction: %v", err)
	}
}

func TestCompactionPressureRequiredInstructions(t *testing.T) {
	for _, role := range []string{"system", "developer", "agents"} {
		t.Run(role, func(t *testing.T) {
			instruction := func(size int) json.RawMessage {
				text := strings.Repeat("required ", size)
				if role == "agents" {
					return compactionPressureMessage("user", "# AGENTS.md instructions\n<INSTRUCTIONS>\n"+text+"\n</INSTRUCTIONS>")
				}
				return compactionPressureMessage(role, text)
			}
			required := instruction(60_000)
			input := []json.RawMessage{required, compactionPressureMessage("user", "Current task "+strings.Repeat("history ", 252_000))}
			snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(snapshot.Items, func(raw json.RawMessage) bool { return bytes.Equal(raw, required) }) {
				t.Fatal("required instructions were not retained byte-exact")
			}
			if count, ok := compactionVisibleStringTokens(snapshot.Items...); !ok || count > compactionTargetTokens+compactionOvershootTokens {
				t.Fatalf("instruction floor escaped ceiling: %d", count)
			}
			input[0] = instruction(90_000)
			parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input}))
			if err != nil {
				t.Fatal(err)
			}
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			if _, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true); err == nil || !strings.Contains(err.Error(), "required instructions were not truncated") {
				t.Fatalf("oversized required instructions: %v", err)
			}
			if compactor.aead != nil {
				t.Fatal("inadmissible required instructions reached sealing")
			}
		})
	}
}

func TestCompactionPressureDroppedReceiptOrder(t *testing.T) {
	a := compactionPressureMessage("user", "Old request A")
	b := compactionPressureMessage("user", "Old request B")
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	first, err := compactor.sealSnapshot(t.Context(), compactionSnapshot{
		Items:   []json.RawMessage{b},
		Carried: []compactionCarriedItem{{Originals: []json.RawMessage{a}, Index: 0, Removed: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []json.RawMessage{first}
	for index := range 1000 {
		input = append(input, compactionPressureMessage("user", fmt.Sprintf("New request %d %s", index, strings.Repeat("detail ", 200))))
	}
	parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := compactor.restore(t.Context(), []json.RawMessage{a, b, second})
	if err != nil || !bytes.Equal(mustMarshalJSON(restored), parsed.fields["input"]) {
		t.Fatalf("consecutive dropped receipts reordered or resurrected history: %v", err)
	}
}

func TestCompactionPressureRejectsUnreconciledOlderCapsule(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	first, err := compactor.seal(t.Context(), []json.RawMessage{compactionPressureMessage("assistant", strings.Repeat("previous reasoning ", 20_000))})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": []json.RawMessage{first, compactionPressureMessage("user", strings.Repeat("current task ", 140_000))}}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if restored, err := compactor.restore(t.Context(), []json.RawMessage{first, second}); err == nil || len(restored) != 0 || !strings.Contains(err.Error(), "overlapping compaction envelopes") {
		t.Fatalf("old capsule resurrected discarded history: %v", err)
	}
}

func TestCompactionEvidencePlanPreservesDroppedAnchor(t *testing.T) {
	a := compactionPressureMessage("user", "Dropped historical user request")
	b := mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "assistant_transport_old", "content": "A historical decision."})
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	first, err := compactor.sealSnapshot(t.Context(), compactionSnapshot{
		Items:   []json.RawMessage{b},
		Carried: []compactionCarriedItem{{Originals: []json.RawMessage{a}, Index: 0, Removed: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []json.RawMessage{first}
	for index := range 9 {
		id := fmt.Sprintf("pwd_%d", index)
		input = append(input, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}
	parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
	if err != nil {
		t.Fatal(err)
	}
	fresh := compactionPressureMessage("developer", "Fresh restriction before the historical decision.")
	restored, err := compactor.restore(t.Context(), []json.RawMessage{fresh, a, second})
	if err != nil || len(restored) < 2 || !bytes.Equal(restored[0], fresh) || !bytes.Contains(restored[1], []byte("A historical decision.")) {
		t.Fatalf("metadata cleanup displaced the fresh instruction anchor: %v", err)
	}
}

func TestCompactionPressureMultipartClassification(t *testing.T) {
	var dense strings.Builder
	for index := range 20_000 {
		fmt.Fprintf(&dense, "Distinct message detail %d has value %x.\n", index, index*7919)
	}
	for _, kinds := range [][]string{{"user.text", "user.text"}, {"user.text", "unknown"}} {
		t.Run(strings.Join(kinds, "+"), func(t *testing.T) {
			input := []json.RawMessage{mustMarshalJSON(map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]string{"type": "input_text", "text": "first\n" + dense.String()}, map[string]string{"type": "input_text", "text": "second\n" + dense.String()}},
				"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": kinds, "trace": "preserved"},
			})}
			snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
			if err != nil {
				t.Fatal(err)
			}
			selected := snapshot.Items[len(snapshot.Items)-1]
			var message, metadata map[string]json.RawMessage
			_ = json.Unmarshal(selected, &message)
			_ = json.Unmarshal(message["internal_chat_message_metadata_passthrough"], &metadata)
			var selectedKinds []string
			_ = json.Unmarshal(metadata["content_item_kinds"], &selectedKinds)
			wantKind := "user.text"
			if kinds[1] == "unknown" {
				wantKind = "unknown"
			}
			if len(selectedKinds) != 1 || selectedKinds[0] != wantKind || jsonString(metadata, "trace") != "preserved" {
				t.Fatal("excerpt classifications no longer describe its content")
			}
			text := contextCompactionNarrationTexts(message["content"])[0]
			message["content"] = mustMarshalJSON([]any{map[string]string{"type": "input_text", "text": text[:80] + "…40000 tokens truncated…" + text[len(text)-80:]}})
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			capsule, err := compactor.sealSnapshot(t.Context(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := compactor.restore(t.Context(), []json.RawMessage{mustMarshalJSON(message), capsule})
			if err != nil || !bytes.Equal(mustMarshalJSON(restored), mustMarshalJSON(snapshot.Items)) {
				t.Fatalf("client-truncated multipart excerpt did not reconcile: %v", err)
			}
		})
	}
}

func TestCompactionPressureRepetitionPreservesContinuationEvidence(t *testing.T) {
	middle := "Correction: use the staging database, not production."
	request := compactionPressureMessage("user", "Run the tests and report the unresolved failures.\n"+
		strings.Repeat("ordinary repeated payload ", 60_000)+"\n"+middle+"\n"+
		strings.Repeat("different repeated payload ", 60_000)+"\nNever deploy.")
	var testOutput strings.Builder
	for index := range 100 {
		fmt.Fprintf(&testOutput, "case %d: checked distinct assertion %d\n", index, index*13)
	}
	testOutput.WriteString("FAIL: migration_schema remains unresolved\nFAIL: migration_lock remains unresolved\n")
	input := []json.RawMessage{
		compactionPressureMessage("developer", "Only change the router."),
		request,
		compactTestCall("tests", "unfamiliar-test-runner"),
		compactTestOutput("tests", testOutput.String(), 1),
		compactTestCall("live", "unfamiliar-worker"),
		mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "live",
			"output": string(mustMarshalJSON(map[string]any{"output": "Still running; next poll owns the result.", "session_id": 4567, "exit_code": nil}))}),
	}
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Report == nil || len(snapshot.Report.Items) != len(input) ||
		snapshot.Report.Items[0].Disposition != "exact" || snapshot.Report.Items[1].Disposition != "repetition_reduced" ||
		snapshot.Report.Items[3].Disposition != "historical" || snapshot.Report.Items[3].Excerpt {
		t.Fatalf("incorrect loss report: %+v", snapshot.Report)
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.sealSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	opened, local, err := compactor.openSnapshot(t.Context(), capsule)
	if err != nil || !local || !bytes.Equal(mustMarshalJSON(opened.Report), mustMarshalJSON(snapshot.Report)) {
		t.Fatalf("loss report did not survive encrypted envelope: %v", err)
	}
	restored, err := compactor.restore(t.Context(), []json.RawMessage{capsule})
	if err != nil || !bytes.Equal(mustMarshalJSON(restored), mustMarshalJSON(snapshot.Items)) {
		t.Fatalf("diagnostics changed model input: %v", err)
	}
	t.Logf("pressure selection report: %s", mustMarshalJSON(snapshot.Report))
	count, _ := compactionVisibleStringTokens(snapshot.Items...)
	if count != snapshot.Report.After {
		t.Fatalf("loss report count %d differs from replay %d", snapshot.Report.After, count)
	}
	if count > 8000 {
		t.Fatalf("repetition crowded out evidence: %d tokens", count)
	}
	var texts []string
	for _, raw := range snapshot.Items {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		texts = append(texts, contextCompactionNarrationTexts(fields["content"])...)
	}
	visible := strings.Join(texts, "\n")
	for _, fact := range []string{middle, "Never deploy.", testOutput.String(), "4567", "Still running; next poll owns the result."} {
		if !strings.Contains(visible, fact) {
			t.Fatalf("lost continuation fact %q", fact)
		}
	}
	if !bytes.Equal(snapshot.Items[1], input[0]) || !strings.Contains(visible, "[mekugi repetition:") ||
		strings.Count(visible, "ordinary repeated payload") > 4 || strings.Count(visible, "different repeated payload") > 4 {
		t.Fatal("required instructions changed or repetitive bulk survived")
	}
}

func TestCompactionPressureDenseFairAllocation(t *testing.T) {
	var dense strings.Builder
	for index := range 25_000 {
		fmt.Fprintf(&dense, "observation %d has distinct value %x\n", index, index*7919)
	}
	current := compactionPressureMessage("user", "Current task: reconcile these observations.\n"+dense.String()+
		"\nCorrection: preserve the public API.\n"+dense.String()+"\nNext step: investigate the two failures.")
	input := []json.RawMessage{current}
	for index := range 12 {
		input = append(input, compactionPressureMessage("assistant", fmt.Sprintf("Decision %d remains unresolved.\n", index)+dense.String()))
	}
	var evidence strings.Builder
	for index := range 100 {
		fmt.Fprintf(&evidence, "assertion %d checked path %d\n", index, index*17)
	}
	evidence.WriteString("FAIL: parser_boundary\nFAIL: lock_timeout\nsession_id=9876 remains live\n")
	input = append(input, compactTestCall("check", "custom-check"), compactTestOutput("check", evidence.String(), 1))
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
	if err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	for _, raw := range snapshot.Items {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		for _, text := range contextCompactionNarrationTexts(fields["content"]) {
			visible.WriteString(text)
		}
	}
	for _, fact := range []string{"Correction: preserve the public API.", "Next step: investigate the two failures.", evidence.String()} {
		if !strings.Contains(visible.String(), fact) {
			t.Fatalf("large latest request displaced useful evidence: missing %q", fact)
		}
	}
	for index := range 12 {
		if !strings.Contains(visible.String(), fmt.Sprintf("Decision %d remains unresolved.", index)) {
			t.Fatalf("decision %d received no coverage", index)
		}
	}
	count, _ := compactionVisibleStringTokens(snapshot.Items...)
	if count > compactionTargetTokens {
		t.Fatalf("fair allocation escaped target: %d", count)
	}
}

func TestCompactionPressureRepetitions(t *testing.T) {
	for _, unit := range []string{"x ", "ordinary repeated phrase\n", "前提条件を保つ。", "failure: alpha\nwarning: beta\n"} {
		t.Run(unit, func(t *testing.T) {
			input := "Before\n" + strings.Repeat(unit, 2000) + "\nCorrection: keep this unique middle fact.\n" +
				strings.Repeat(unit, 2000) + "\nAfter"
			output, err := compactionPressureRepetitions(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output, "Before\n") || !strings.HasSuffix(output, "\nAfter") ||
				!strings.Contains(output, "Correction: keep this unique middle fact.") ||
				len(output) > len(input)/4 || !utf8.ValidString(output) {
				t.Fatal("repetition reduction lost distinct text or retained bulk")
			}
		})
	}
	var distinct strings.Builder
	for index := range 2000 {
		fmt.Fprintf(&distinct, "failure: alpha_%d\nwarning: beta_%d\n", index, index)
	}
	output, err := compactionPressureRepetitions(t.Context(), distinct.String())
	if err != nil || output != distinct.String() {
		t.Fatal("near-duplicate diagnostics were treated as repetitions")
	}
}

func TestCompactionPressureRepetitionPreservesParts(t *testing.T) {
	for _, kind := range []string{"message", "agent_message"} {
		t.Run(kind, func(t *testing.T) {
			imagePart := mustMarshalJSON(map[string]string{"type": "input_image", "image_url": "data:image/png;base64,cHJvYmU="})
			unknownPart := mustMarshalJSON(map[string]string{"type": "future_part", "payload": "distinct evidence"})
			metadata := mustMarshalJSON(map[string]any{"content_item_kinds": []string{"user.text", "user.image", "unknown"}, "trace": "unchanged"})
			placeholder := mustMarshalJSON(map[string]string{"type": "input_text", "text": "[Image]"})
			selectedMetadata := mustMarshalJSON(map[string]any{"content_item_kinds": []string{"user.text", "unknown", "unknown"}, "trace": "unchanged"})
			input := []json.RawMessage{mustMarshalJSON(map[string]any{
				"type": kind, "role": "user", "id": "preserved-message-id",
				"content": []json.RawMessage{mustMarshalJSON(map[string]string{"type": "input_text", "text": strings.Repeat("repeated text ", 140_000), "annotation": "unchanged"}), imagePart, unknownPart},
				"internal_chat_message_metadata_passthrough": metadata,
			})}
			snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
			if err != nil {
				t.Fatal(err)
			}
			var message map[string]json.RawMessage
			_ = json.Unmarshal(snapshot.Items[1], &message)
			var parts []json.RawMessage
			_ = json.Unmarshal(message["content"], &parts)
			if len(parts) != 3 || !bytes.Equal(parts[1], placeholder) || !bytes.Equal(parts[2], unknownPart) ||
				!bytes.Equal(message["internal_chat_message_metadata_passthrough"], selectedMetadata) ||
				jsonString(message, "id") != "preserved-message-id" {
				t.Fatal("repetition reduction lost distinct parts or identity metadata")
			}
			var first map[string]json.RawMessage
			_ = json.Unmarshal(parts[0], &first)
			if jsonString(first, "annotation") != "unchanged" || !snapshot.Report.Items[0].Repetition || snapshot.Report.Items[0].Excerpt {
				t.Fatal("repetition-only metadata or diagnostics are incorrect")
			}
		})
	}
}

func TestCompactionPressureRepetitionRequiresTokenSavings(t *testing.T) {
	input := strings.Repeat(" ", 1024)
	output, err := compactionPressureRepetitions(t.Context(), input)
	if err != nil || output != input {
		t.Fatalf("already token-efficient whitespace was changed: %v", err)
	}
}

func TestCompactionPressureRequestChainSurvivesToolFlood(t *testing.T) {
	request := compactionPressureMessage("user", "Add a stash-message popup for the selected file. Open the external editor at the selected changed line, not at the file beginning.")
	decision := compactionPressureMessage("assistant", "Agreed plan: use a status-aware sidebar and Unstaged/Staged tabs. Keep Git-changing actions blocked until status and diff refresh finish.")
	correction := compactionPressureMessage("user", "Study the reference implementation first. Only local commits are approved.")
	input := []json.RawMessage{compactionPressureMessage("developer", "Preserve the current public API."), request, decision, correction}
	var output strings.Builder
	for index := range 250 {
		fmt.Fprintf(&output, "result row %d has unique value %x\n", index, index*7919)
	}
	for index := range 180 {
		id := fmt.Sprintf("unknown-%d", index)
		input = append(input,
			mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "unfamiliar", "call_id": id, "input": "inspect the current code"}),
			mustMarshalJSON(map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": output.String()}))
	}
	ack := compactionPressureMessage("user", "yea")
	input = append(input, ack)
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []json.RawMessage{request, decision, correction, ack} {
		if !slices.ContainsFunc(snapshot.Items, func(raw json.RawMessage) bool { return bytes.Equal(raw, required) }) {
			t.Fatal("tool history displaced a small request, correction, decision, or acknowledgement")
		}
	}
	if count, ok := compactionVisibleStringTokens(snapshot.Items...); !ok || count > compactionTargetTokens {
		t.Fatalf("request-chain coverage escaped budget: %d", count)
	}
	if snapshot.Report.Items[1].Family != "requests" || snapshot.Report.Items[2].Family != "discussion" ||
		snapshot.Report.Items[4].Family != "execution" {
		t.Fatal("loss report does not distinguish task context from execution")
	}
}

func TestCompactionNativeImageMetricAndPlaceholders(t *testing.T) {
	image := func(url string) json.RawMessage {
		return mustMarshalJSON(map[string]string{"type": "input_image", "image_url": url})
	}
	message := func(role, text, url string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "message", "role": role,
			"content": []json.RawMessage{mustMarshalJSON(map[string]string{"type": "input_text", "text": text}), image(url)}})
	}
	short := message("user", "Interpret the screenshot.", "data:image/png;base64,YQ==")
	long := message("user", "Interpret the screenshot.", "data:image/png;base64,"+strings.Repeat("YQ==", 10000))
	a, _ := compactionVisibleStringTokens(short)
	b, _ := compactionVisibleStringTokens(long)
	if a != b {
		t.Fatal("image transport bytes were counted as text tokens")
	}
	plain, _ := compactionVisibleStringTokens(compactionPressureMessage("user", strings.Repeat("YQ==", 10000)))
	if plain <= b {
		t.Fatal("ordinary visible text bypassed the text budget")
	}
	// A lookalike image object in arbitrary metadata is not a native image part.
	lookalike := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "request",
		"unknown_metadata": map[string]any{"type": "message", "content": []json.RawMessage{image("data:image/png;base64," + strings.Repeat("YQ==", 10000))}}})
	lookalikeCost, _ := compactionVisibleStringTokens(lookalike)
	if lookalikeCost <= b {
		t.Fatal("unknown metadata was mistaken for native image transport")
	}

	var input []json.RawMessage
	for index := range 6 {
		input = append(input, message("user", fmt.Sprintf("Requirement %d remains in force.", index), fmt.Sprintf("https://example.invalid/image-%d.png", index)))
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "loopback", "input": input}))
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := compactor.restore(t.Context(), append(slices.Clone(input), capsule))
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := compactionImageUsage(restored); count != 0 {
		t.Fatalf("historical images were restored: %d", count)
	}
	visible := string(mustMarshalJSON(restored))
	for index := range 6 {
		if !strings.Contains(visible, fmt.Sprintf("Requirement %d remains in force.", index)) {
			t.Fatalf("image pruning removed request %d", index)
		}
	}
	if strings.Count(visible, "[Image]") != 6 || strings.Contains(visible, "example.invalid/image-") {
		t.Fatal("historical images were not replaced with one marker each")
	}
}

func TestCompactionRequiredImagesRemainExact(t *testing.T) {
	for _, role := range []string{"developer", "system", "user"} {
		t.Run(role, func(t *testing.T) {
			var parts []map[string]string
			var kinds []string
			for range 5 {
				parts = append(parts, map[string]string{"type": "input_image", "image_url": "required-image"})
				kinds = append(kinds, "context.instructions")
			}
			if role == "developer" {
				parts[0]["image_url"] = strings.Repeat("x", (8<<20)+1)
			}
			required := mustMarshalJSON(map[string]any{"type": "message", "role": role, "content": parts,
				"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": kinds}})
			ordinary := mustMarshalJSON(map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]string{"type": "input_image", "image_url": "ordinary-image"}}})
			input := []json.RawMessage{required, ordinary}
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "loopback", "input": input}))
			if err != nil {
				t.Fatal(err)
			}
			capsule, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := compactor.restore(t.Context(), []json.RawMessage{capsule})
			if err != nil || len(restored) != 2 || !bytes.Equal(restored[0], required) {
				t.Fatalf("mandatory instruction images changed: %v", err)
			}
			if count, _ := compactionImageUsage(restored); count != 5 {
				t.Fatal("required images were limited or the ordinary image survived")
			}
		})
	}
}

func TestCompactionPressureImagesBecomePlaceholdersInTextExcerpts(t *testing.T) {
	var dense strings.Builder
	for index := range 25000 {
		fmt.Fprintf(&dense, "distinct observation %d has value %x\n", index, index*7919)
	}
	image := mustMarshalJSON(map[string]string{"type": "input_image", "image_url": "data:image/png;base64,cHJvYmU=", "detail": "original"})
	for _, kind := range []string{"message", "custom_tool_call_output", "function_call_output"} {
		t.Run(kind, func(t *testing.T) {
			key := "output"
			if kind == "message" {
				key = "content"
			}
			input := []json.RawMessage{mustMarshalJSON(map[string]any{"type": kind, "role": "user", "call_id": "picture",
				key: []json.RawMessage{mustMarshalJSON(map[string]string{"type": "input_text", "text": dense.String()}), image}})}
			snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
			if err != nil {
				t.Fatal(err)
			}
			if count, _ := compactionImageUsage(snapshot.Items); count != 0 {
				t.Fatal("text pressure retained a historical image")
			}
			var selected map[string]json.RawMessage
			_ = json.Unmarshal(snapshot.Items[1], &selected)
			if !strings.Contains(strings.Join(contextCompactionNarrationTexts(selected["content"]), "\n"), "[Image]") {
				t.Fatal("image placeholder was lost from the retained excerpt")
			}
			if kind != "message" && jsonString(selected, "role") != "assistant" {
				t.Fatal("historical tool observation changed attribution")
			}
			if strings.Contains(strings.Join(contextCompactionNarrationTexts(selected["content"]), "\n"), "cHJvYmU=") {
				t.Fatal("historical wrapping leaked base64 into visible text")
			}
			if snapshot.Report.ImagesBefore != 1 || snapshot.Report.ImagesAfter != 0 || !snapshot.Report.Items[0].Excerpt {
				t.Fatal("image/text diagnostics are incorrect")
			}
		})
	}
}

func TestCompactionPressureAgentExcerptPreservesAttribution(t *testing.T) {
	var body strings.Builder
	body.WriteString("Review of the storage boundary.\n")
	for index := range 5000 {
		fmt.Fprintf(&body, "Finding %d has observed offset %d and checksum %x.\n", index, index*7, index*7919)
	}
	body.WriteString("Unresolved: cancellation still leaves the archive process running.")
	original := map[string]json.RawMessage{
		"type": mustMarshalJSON("agent_message"), "id": mustMarshalJSON("report-storage"),
		"author": mustMarshalJSON("storage-reviewer"), "recipient": mustMarshalJSON("main"),
		"metadata": mustMarshalJSON(map[string]string{"source": "independent-inspection"}),
		"content":  mustMarshalJSON([]any{map[string]string{"type": "input_text", "text": body.String()}}),
	}
	input := []json.RawMessage{mustMarshalJSON(original)}
	snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 2 || !snapshot.Report.Items[0].Excerpt {
		t.Fatal("oversized agent report did not produce a retained excerpt")
	}
	var retained map[string]json.RawMessage
	_ = json.Unmarshal(snapshot.Items[1], &retained)
	for key, value := range original {
		if key != "content" && !bytes.Equal(value, retained[key]) {
			t.Errorf("agent envelope field %q changed", key)
		}
	}
	var parts []map[string]json.RawMessage
	_ = json.Unmarshal(retained["content"], &parts)
	if len(parts) != 1 || jsonString(parts[0], "type") != "input_text" ||
		!strings.Contains(jsonString(parts[0], "text"), "Unresolved: cancellation still leaves the archive process running.") {
		t.Fatal("agent excerpt lost its native content type or unresolved conclusion")
	}
}

func TestCompactionPressureLatestReportSurvivesDiscussionFlood(t *testing.T) {
	var older, current strings.Builder
	for index := range 500 {
		fmt.Fprintf(&older, "Historical investigation %d observed revision %x.\n", index, index*7919)
	}
	for index := range 80 {
		fmt.Fprintf(&current, "Current checkpoint %d retains artifact revision %x.\n", index, index*97)
	}
	current.WriteString("Do not rerun either agent or perform inference. No regrade has started. Cancellation remains unresolved.")
	for _, kind := range []string{"message", "agent_message"} {
		t.Run(kind, func(t *testing.T) {
			input := []json.RawMessage{compactionPressureMessage("user", "Regrade the saved candidates, preserving their measured timing.")}
			for range 200 {
				input = append(input, compactionPressureMessage("assistant", older.String()))
			}
			report := mustMarshalJSON(map[string]any{"type": kind, "role": "assistant", "author": "reviewer", "content": current.String()})
			input = append(input, report,
				mustMarshalJSON(map[string]any{"type": "agent_message", "author": "opaque-reviewer", "recipient": "main",
					"content": []any{map[string]string{"type": "encrypted_content", "encrypted_content": "opaque-report"}}}))
			snapshot, _, err := pressureCompactionWorkingSet(t.Context(), input, compactionTargetTokens)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(snapshot.Items, func(raw json.RawMessage) bool { return bytes.Equal(raw, report) }) {
				t.Fatal("historical discussion displaced the bounded current continuation report")
			}
			if count, ok := compactionVisibleStringTokens(snapshot.Items...); !ok || count > compactionTargetTokens {
				t.Fatal("latest-report reservation escaped the text budget")
			}
		})
	}
}
