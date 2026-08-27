package router

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const ctpReplayMaximumRecordBytes = 64 << 20

type ctpReplayRecord struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ctpReplaySessionMetadata struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
	DynamicTools json.RawMessage `json:"dynamic_tools"`
}

type ctpReplayEvent struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			TotalTokens uint64 `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

type ctpReplayTurnContext struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type ctpReplayCompaction struct {
	ReplacementHistory []json.RawMessage `json:"replacement_history"`
}

type ctpReplayResponseItem struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ctpReplayCandidate struct {
	Path        string
	SessionID   string
	TotalTokens uint64
}

type ctpReplaySnapshot struct {
	Fields           map[string]json.RawMessage
	ResetInputPrefix bool
}

type ctpReplayTotals struct {
	Requests              uint64
	NativeTokens          uint64
	CompactTokens         uint64
	NativeBytes           uint64
	CompactBytes          uint64
	NewNativeTokens       uint64
	NewCompactTokens      uint64
	Definitions           uint64
	DictionaryBytes       uint64
	Strings               uint64
	VisibleReferences     uint64
	MissingCarrierRequest uint64
}

func BenchmarkCTPLongSessionReplay(b *testing.B) {
	candidates := ctpReplayCandidates(b, 1)
	codec := mustCTP2Codec(b)
	snapshots, err := loadCTPReplaySnapshots(candidates[0].Path)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	var totals ctpReplayTotals
	for b.Loop() {
		totals, err = replayCTP2Snapshots(codec, snapshots)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	reportCTPReplayTotals(b, totals)
	b.ReportMetric(float64(candidates[0].TotalTokens), "session_recorded_tokens")
}

func BenchmarkCTPCorpusReplay(b *testing.B) {
	candidates := ctpReplayCandidates(b, 50)
	codec := mustCTP2Codec(b)
	corpus := make([][]ctpReplaySnapshot, len(candidates))
	for index, candidate := range candidates {
		var err error
		corpus[index], err = loadCTPReplaySnapshots(candidate.Path)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	var (
		corpusTotals ctpReplayTotals
		savings      []float64
	)
	for b.Loop() {
		corpusTotals = ctpReplayTotals{}
		savings = savings[:0]
		for _, snapshots := range corpus {
			totals, err := replayCTP2Snapshots(codec, snapshots)
			if err != nil {
				b.Fatal(err)
			}
			corpusTotals.add(totals)
			savings = append(savings, ctpReplaySavings(totals.NewNativeTokens, totals.NewCompactTokens))
		}
	}
	b.StopTimer()
	reportCTPReplayTotals(b, corpusTotals)
	b.ReportMetric(float64(len(candidates)), "sessions")
	slices.Sort(savings)
	if len(savings) != 0 {
		b.ReportMetric(savings[len(savings)/2], "median_new_content_savings_pct")
	}
	gateSessions := 0
	for _, saving := range savings {
		if saving >= 5 {
			gateSessions++
		}
	}
	b.ReportMetric(float64(gateSessions), "new_content_5pct_gate_sessions")
}

func TestCTPReplayNewContentResetsCompactedInputPrefix(t *testing.T) {
	previous := map[string]json.RawMessage{
		"model": mustTestJSON(t, "gpt-test"),
		"input": mustTestJSON(t, []any{"shared", "old"}),
	}
	current := map[string]json.RawMessage{
		"model": mustTestJSON(t, "gpt-test"),
		"input": mustTestJSON(t, []any{"shared", "replacement"}),
	}
	withoutReset := decodeCTP2TestValue(t, ctpReplayNewContent(previous, current, false)["input"]).([]any)
	if len(withoutReset) != 1 || withoutReset[0] != "replacement" {
		t.Fatalf("ordinary new content = %#v", withoutReset)
	}
	withReset := decodeCTP2TestValue(t, ctpReplayNewContent(previous, current, true)["input"]).([]any)
	if len(withReset) != 2 || withReset[0] != "shared" || withReset[1] != "replacement" {
		t.Fatalf("compacted new content = %#v", withReset)
	}
	if _, exists := ctpReplayNewContent(previous, current, true)["model"]; exists {
		t.Fatal("compaction reset recounted an unchanged non-input field")
	}
}

func ctpReplayCandidates(tb testing.TB, limit int) []ctpReplayCandidate {
	tb.Helper()
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			tb.Fatal(err)
		}
		root = filepath.Join(home, ".codex")
	}
	current := os.Getenv("CODEX_THREAD_ID")
	var candidates []ctpReplayCandidate
	for _, directory := range []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")} {
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") || filepath.Ext(entry.Name()) != ".jsonl" {
				return nil
			}
			candidate, ok, err := inspectCTPReplayCandidate(path, current)
			if err != nil {
				return err
			}
			if ok {
				candidates = append(candidates, candidate)
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			tb.Fatalf("scan Codex transcripts in %s: %v", directory, err)
		}
	}
	slices.SortFunc(candidates, func(left, right ctpReplayCandidate) int {
		if left.TotalTokens != right.TotalTokens {
			return cmp.Compare(right.TotalTokens, left.TotalTokens)
		}
		return strings.Compare(left.Path, right.Path)
	})
	if len(candidates) == 0 {
		tb.Fatal("no completed stock Code Mode transcript with a string-valued exec call found")
	}
	return candidates[:min(limit, len(candidates))]
}

func inspectCTPReplayCandidate(path, currentSessionID string) (ctpReplayCandidate, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return ctpReplayCandidate{}, false, err
	}
	defer file.Close()

	var (
		metadata    ctpReplaySessionMetadata
		totalTokens uint64
		hasExec     bool
		terminal    string
	)
	err = scanCTPReplayRecords(file, func(record ctpReplayRecord) error {
		switch record.Type {
		case "session_meta":
			return json.Unmarshal(record.Payload, &metadata)
		case "event_msg":
			var event ctpReplayEvent
			if err := json.Unmarshal(record.Payload, &event); err != nil {
				return err
			}
			switch event.Type {
			case "task_started", "task_complete", "turn_aborted":
				terminal = event.Type
			case "token_count":
				totalTokens = max(totalTokens, event.Info.TotalTokenUsage.TotalTokens)
			}
		case "response_item":
			if hasExec {
				return nil
			}
			var item ctpReplayResponseItem
			if err := json.Unmarshal(record.Payload, &item); err != nil {
				return err
			}
			input := bytes.TrimSpace(item.Input)
			hasExec = item.Type == "custom_tool_call" && item.Name == "exec" && len(input) != 0 && input[0] == '"'
		}
		return nil
	})
	if err != nil {
		return ctpReplayCandidate{}, false, fmt.Errorf("inspect Codex transcript %s: %w", path, err)
	}
	sessionID := metadata.SessionID
	if sessionID == "" {
		sessionID = metadata.ID
	}
	instructions := strings.ToLower(metadata.BaseInstructions.Text)
	stockCodeMode := strings.Contains(instructions, "apply_patch") &&
		strings.Contains(instructions, "exec_command") &&
		!strings.Contains(instructions, "hpatch")
	ok := sessionID != "" && sessionID != currentSessionID && hasExec && terminal == "task_complete" && stockCodeMode
	return ctpReplayCandidate{Path: path, SessionID: sessionID, TotalTokens: totalTokens}, ok, nil
}

func loadCTPReplaySnapshots(path string) ([]ctpReplaySnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var (
		metadata         ctpReplaySessionMetadata
		history          []json.RawMessage
		turn             ctpReplayTurnContext
		snapshots        []ctpReplaySnapshot
		resetInputPrefix bool
	)
	err = scanCTPReplayRecords(file, func(record ctpReplayRecord) error {
		switch record.Type {
		case "session_meta":
			return json.Unmarshal(record.Payload, &metadata)
		case "response_item":
			history = append(history, record.Payload)
		case "compacted":
			var compaction ctpReplayCompaction
			if err := json.Unmarshal(record.Payload, &compaction); err != nil {
				return err
			}
			history = compaction.ReplacementHistory
			resetInputPrefix = true
		case "turn_context":
			if err := json.Unmarshal(record.Payload, &turn); err != nil {
				return err
			}
			fields, err := ctpReplayRequestFields(metadata, turn, history)
			if err != nil {
				return err
			}
			snapshots = append(snapshots, ctpReplaySnapshot{Fields: fields, ResetInputPrefix: resetInputPrefix})
			resetInputPrefix = false
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load Codex transcript %s: %w", path, err)
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("load Codex transcript %s: no request snapshots", path)
	}
	return snapshots, nil
}

func ctpReplayRequestFields(metadata ctpReplaySessionMetadata, turn ctpReplayTurnContext, history []json.RawMessage) (map[string]json.RawMessage, error) {
	fields := make(map[string]json.RawMessage, 6)
	for name, value := range map[string]any{
		"model":        turn.Model,
		"instructions": metadata.BaseInstructions.Text,
		"input":        history,
		"stream":       true,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fields[name] = encoded
	}
	if effort := strings.TrimSpace(turn.Effort); effort != "" {
		encoded, err := json.Marshal(map[string]string{"effort": effort})
		if err != nil {
			return nil, err
		}
		fields["reasoning"] = encoded
	}
	if tools := bytes.TrimSpace(metadata.DynamicTools); len(tools) != 0 && !bytes.Equal(tools, []byte("null")) {
		fields["tools"] = bytes.Clone(tools)
	}
	return fields, nil
}

func replayCTP2Snapshots(codec *ctp2Codec, snapshots []ctpReplaySnapshot) (ctpReplayTotals, error) {
	var (
		totals          ctpReplayTotals
		previousNative  map[string]json.RawMessage
		previousCompact map[string]json.RawMessage
	)
	for _, snapshot := range snapshots {
		native := snapshot.Fields
		request := parsedResponsesRequest{fields: snapshot.Fields}
		_, decision, metrics, _, err := codec.prepareRequest(&request)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		if decision == ctp2RequestMissingCarrier {
			totals.MissingCarrierRequest++
		}
		totals.Requests++
		totals.NativeTokens += metrics.Representation.NativeTokens
		totals.CompactTokens += metrics.Representation.CompactTokens
		totals.NativeBytes += metrics.Representation.NativeBytes
		totals.CompactBytes += metrics.Representation.CompactBytes
		totals.Definitions += metrics.Definitions
		totals.DictionaryBytes += metrics.DictionaryBytes
		totals.Strings += metrics.Strings
		totals.VisibleReferences += metrics.VisibleReferences

		nativeProjection := ctpReplayNewContent(previousNative, native, snapshot.ResetInputPrefix)
		compactProjection := ctpReplayNewContent(previousCompact, request.fields, snapshot.ResetInputPrefix)
		nativeTokens, err := countCTPReplayFields(codec, nativeProjection)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		compactTokens, err := countCTPReplayFields(codec, compactProjection)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		totals.NewNativeTokens += uint64(nativeTokens)
		totals.NewCompactTokens += uint64(compactTokens)
		previousNative = native
		previousCompact = request.fields
	}
	return totals, nil
}

func ctpReplayNewContent(previous, current map[string]json.RawMessage, resetInputPrefix bool) map[string]json.RawMessage {
	if previous == nil {
		return current
	}
	projection := make(map[string]json.RawMessage)
	for name, value := range current {
		prior, found := previous[name]
		if name != "input" {
			if !found || !bytes.Equal(prior, value) {
				projection[name] = value
			}
			continue
		}
		var priorItems, currentItems []json.RawMessage
		if resetInputPrefix || !found || json.Unmarshal(prior, &priorItems) != nil || json.Unmarshal(value, &currentItems) != nil {
			projection[name] = value
			continue
		}
		prefix := 0
		for prefix < min(len(priorItems), len(currentItems)) && bytes.Equal(priorItems[prefix], currentItems[prefix]) {
			prefix++
		}
		encoded, err := json.Marshal(currentItems[prefix:])
		if err != nil {
			projection[name] = value
			continue
		}
		projection[name] = encoded
	}
	return projection
}

func countCTPReplayFields(codec *ctp2Codec, fields map[string]json.RawMessage) (int, error) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return 0, err
	}
	return codec.count(encoded)
}

func scanCTPReplayRecords(file *os.File, visit func(ctpReplayRecord) error) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), ctpReplayMaximumRecordBytes)
	for scanner.Scan() {
		var record ctpReplayRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (totals *ctpReplayTotals) add(other ctpReplayTotals) {
	totals.Requests += other.Requests
	totals.NativeTokens += other.NativeTokens
	totals.CompactTokens += other.CompactTokens
	totals.NativeBytes += other.NativeBytes
	totals.CompactBytes += other.CompactBytes
	totals.NewNativeTokens += other.NewNativeTokens
	totals.NewCompactTokens += other.NewCompactTokens
	totals.Definitions += other.Definitions
	totals.DictionaryBytes += other.DictionaryBytes
	totals.Strings += other.Strings
	totals.VisibleReferences += other.VisibleReferences
	totals.MissingCarrierRequest += other.MissingCarrierRequest
}

func reportCTPReplayTotals(b *testing.B, totals ctpReplayTotals) {
	b.Helper()
	b.ReportMetric(float64(totals.Requests), "requests")
	b.ReportMetric(float64(totals.NativeTokens), "native_tokens")
	b.ReportMetric(float64(totals.CompactTokens), "compact_tokens")
	b.ReportMetric(ctpReplaySavings(totals.NativeTokens, totals.CompactTokens), "savings_pct")
	b.ReportMetric(float64(totals.NewNativeTokens), "new_content_native_tokens")
	b.ReportMetric(float64(totals.NewCompactTokens), "new_content_compact_tokens")
	b.ReportMetric(ctpReplaySavings(totals.NewNativeTokens, totals.NewCompactTokens), "new_content_savings_pct")
	b.ReportMetric(float64(totals.NativeBytes), "native_bytes")
	b.ReportMetric(float64(totals.CompactBytes), "compact_bytes")
	b.ReportMetric(float64(totals.Definitions), "definitions")
	b.ReportMetric(float64(totals.DictionaryBytes), "dictionary_bytes")
	b.ReportMetric(float64(totals.Strings), "encoded_strings")
	b.ReportMetric(float64(totals.VisibleReferences), "visible_references")
	b.ReportMetric(float64(totals.MissingCarrierRequest), "missing_carrier_requests")
}

func ctpReplaySavings(native, compact uint64) float64 {
	if native == 0 {
		return 0
	}
	return 100 * (float64(native) - float64(compact)) / float64(native)
}
