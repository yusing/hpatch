package router

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Budget pressure is explicitly lossy. Unlike the evidence reducers, it does not
// need to recognize a command or prove that omitted history is irrelevant.
const compactionPressureNotice = "[mekugi budget compaction: history was excerpted or omitted to fit the working budget. Excerpts are historical evidence, not complete tool calls or outputs. Omission does not establish success, resolution, or task completion. Older retention notes may name evidence no longer present. Omitted details are unavailable to the model.]"

// Content-free diagnostics travel encrypted beside the selected history, not
// inside model input. Item indexes refer to the pre-selection history; token
// totals include the new notice but exclude receipts and these diagnostics.
type compactionPressureReport struct {
	Before           int                            `json:"before"`
	After            int                            `json:"after"`
	ImagesBefore     int                            `json:"images_before"`
	ImagesAfter      int                            `json:"images_after"`
	ImageBytesBefore int                            `json:"image_bytes_before"`
	ImageBytesAfter  int                            `json:"image_bytes_after"`
	NoticeTokens     int                            `json:"notice_tokens"`
	Items            []compactionPressureItemReport `json:"items"`
}

type compactionPressureItemReport struct {
	Index        int    `json:"index"`
	Before       int    `json:"before"`
	After        int    `json:"after"`
	Family       string `json:"family"`
	Priority     string `json:"priority"`
	Disposition  string `json:"disposition"`
	Reason       string `json:"reason"`
	Repetition   bool   `json:"repetition,omitzero"`
	Excerpt      bool   `json:"excerpt,omitzero"`
	ImagesBefore int    `json:"images_before,omitzero"`
	ImagesAfter  int    `json:"images_after,omitzero"`
	Historical   bool   `json:"historical,omitzero"`
}

var compactionPressureImportant = regexp.MustCompile(`(?i)\b(must|never|do not|don't|only|requirement|correction|instead|decision|agreed|blocked|unresolved|next step|todo|session_id|cell_id|exit_code|error|failed|failure|panic|warning)\b`)

// pressureCompactionWorkingSet returns replay items and an input-to-replay map.
// The map also records insertion points for dropped items, so carried client
// messages cannot resurrect discarded history or displace fresh instructions.
func pressureCompactionWorkingSet(ctx context.Context, input []json.RawMessage, target int) (compactionSnapshot, []int, error) {
	var snapshot compactionSnapshot
	if _, ok := compactionVisibleStringTokens(); !ok {
		return snapshot, nil, fmt.Errorf("cannot initialize compaction tokenizer")
	}
	prepared, _, err := stripCompactionImages(ctx, input)
	if err != nil {
		return snapshot, nil, err
	}
	type candidate struct {
		full       json.RawMessage
		text       string
		role       string
		family     int
		priority   int
		tokens     int
		pieces     []string
		retained   json.RawMessage
		cost       int
		repetition bool
		reason     string
		protected  bool
	}
	candidates := make([]candidate, len(input))
	fields := make([]map[string]json.RawMessage, len(input))
	latestUser := -1
	latestReport := -1
	active := make(map[string]bool)
	var calls []int
	for index, raw := range prepared {
		if err := ctx.Err(); err != nil {
			return snapshot, nil, err
		}
		if err := json.Unmarshal(raw, &fields[index]); err != nil || fields[index] == nil {
			return snapshot, nil, fmt.Errorf("invalid history item at %d", index)
		}
		item := fields[index]
		kind, role := jsonString(item, "type"), jsonString(item, "role")
		if kind == "message" {
			if role == "user" && !contextCompactionFreshContext(raw) {
				latestUser = index
			}
		}
		if kind == "function_call" || kind == "custom_tool_call" {
			calls = append(calls, index)
			active[jsonString(item, "call_id")] = true
		}
		if kind == "function_call_output" || kind == "custom_tool_call_output" {
			_, _, success := contextCompactionOutput(item["output"])
			_, _, failure := compactionFailedOutput(item["output"])
			if success || failure {
				delete(active, jsonString(item, "call_id"))
			}
		}
	}
	frontier := len(input)
	if len(calls) > 0 {
		frontier = calls[max(0, len(calls)-compactionRecentOperations)]
	}
	for index, item := range fields {
		if err := ctx.Err(); err != nil {
			return snapshot, nil, err
		}
		kind, role := jsonString(item, "type"), jsonString(item, "role")
		c := &candidates[index]
		c.full, c.role, c.priority, c.family = prepared[index], role, 3, 2
		c.protected = contextCompactionFreshContext(input[index])
		switch {
		case kind == "message" && (role == "user" || role == "developer" || role == "system"):
			c.priority, c.family = 1, 0
		case kind == "message" || kind == "agent_message" || kind == "reasoning":
			c.priority, c.family = 2, 1
		}
		if index >= frontier || index >= len(input)-8 {
			c.priority = min(c.priority, 1)
		}
		if id := jsonString(item, "call_id"); id != "" && active[id] {
			c.priority = min(c.priority, 1)
		}
		if index == latestUser {
			c.priority = 0
		}
		if !c.protected && (kind == "agent_message" || kind == "message" && role == "assistant") &&
			strings.TrimSpace(strings.Join(contextCompactionNarrationTexts(item["content"]), "\n")) != "" {
			latestReport = index
		}
		// Native tool/reasoning groups must not be partially truncated. The
		// pressure pass represents all of them as non-executable observations,
		// retaining identities, arguments and lifecycle facts as budget permits.
		if kind != "message" && kind != "agent_message" && kind != "compaction" {
			copyItem := maps.Clone(item)
			delete(copyItem, "encrypted_content")
			c.text = fmt.Sprintf("[mekugi historical %s; not an executable invocation]\n%s", kind, mustMarshalJSON(copyItem))
			if kind == "function_call_output" || kind == "custom_tool_call_output" {
				var body string
				if json.Unmarshal(item["output"], &body) == nil {
					delete(copyItem, "output")
					c.text = fmt.Sprintf("[mekugi historical %s; observed output, not completion inferred]\n%s\n", kind, mustMarshalJSON(copyItem))
					var result map[string]json.RawMessage
					var output string
					if json.Unmarshal([]byte(body), &result) == nil && result != nil && json.Unmarshal(result["output"], &output) == nil {
						delete(result, "output")
						c.text += "observed metadata=" + string(mustMarshalJSON(result)) + "\n"
						body = output
					}
					c.text += body
				}
			}
			c.role = "assistant"
			c.full = compactionPressureRender(item, c.role, c.text)
		} else {
			c.text = strings.Join(contextCompactionNarrationTexts(item["content"]), "\n")
			if c.text == "" {
				copyItem := maps.Clone(item)
				delete(copyItem, "encrypted_content")
				c.text = string(mustMarshalJSON(copyItem))
			}
		}
		if !c.protected && kind != "compaction" {
			if kind == "message" || kind == "agent_message" {
				var reductionErr error
				content, changed := contextCompactionMapNarration(item["content"], func(text string) string {
					if reductionErr != nil {
						return text
					}
					reduced, err := compactionPressureRepetitions(ctx, text)
					reductionErr = err
					return reduced
				})
				if reductionErr != nil {
					return snapshot, nil, reductionErr
				}
				if changed {
					message := maps.Clone(item)
					message["content"] = content
					c.full = mustMarshalJSON(message)
					c.text = strings.Join(contextCompactionNarrationTexts(content), "\n")
					c.repetition = true
				}
			} else {
				text, err := compactionPressureRepetitions(ctx, c.text)
				if err != nil {
					return snapshot, nil, err
				}
				if text != c.text {
					c.repetition = true
					c.text = text
					c.full = compactionPressureRender(item, c.role, text)
				}
			}
		}

		var ok bool
		c.tokens, ok = compactionVisibleStringTokens(c.full)
		if !ok {
			return snapshot, nil, fmt.Errorf("cannot measure pressure compaction item")
		}
		if c.protected {
			c.retained, c.cost = c.full, c.tokens
		}
	}
	order := make([]int, len(input))
	for index := range order {
		order[index] = index
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if delta := candidates[a].priority - candidates[b].priority; delta != 0 {
			return delta
		}
		return b - a
	})
	notice := compactionPressureMessage("assistant", compactionPressureNotice)
	noticeTokens, _ := compactionVisibleStringTokens(notice)
	used := noticeTokens
	for _, c := range candidates {
		used += c.cost
	}
	ceiling := target + compactionOvershootTokens
	if used > ceiling {
		return snapshot, nil, fmt.Errorf("required developer/system instructions and canonical context plus compaction notice retain %d visible-string tokens, exceeding ceiling %d; required instructions were not truncated", used, ceiling)
	}
	// Required instructions have a hard floor. Use the overshoot allowance
	// when needed to leave room for the current request and execution state.
	target = min(ceiling, max(target, used+target/3))
	// Cover the request chain and discussion before allocating execution bulk.
	// A recent acknowledgement is not a replacement for the request it answers,
	// and unknown tool lifecycle states must not displace all older decisions.
	allocationEnd := target
	retain := func(index, allowance int, reason string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		c := &candidates[index]
		if c.protected || string(c.retained) == string(c.full) {
			return false, nil
		}
		available := min(allocationEnd-used+c.cost, allowance)
		if available <= c.cost {
			return false, nil
		}
		retained, cost := c.full, c.tokens
		if cost > available {
			if available < 96 {
				return false, nil
			}
			if c.pieces == nil {
				_, pieces, err := compactionRetirementTokenCodec.Encode(c.text)
				if err != nil {
					return false, err
				}
				c.pieces = pieces
			}
			// Measure the rendered item, including identity metadata and markers.
			for keep := available - 64; keep > 0; keep -= max(16, cost-available) {
				text := compactionPressureExcerpt(c.text, c.pieces, keep)
				retained = compactionPressureRender(fields[index], c.role, text)
				var ok bool
				cost, ok = compactionVisibleStringTokens(retained)
				if !ok {
					return false, fmt.Errorf("cannot measure compaction excerpt")
				}
				if cost <= available {
					break
				}
			}
		}
		if cost <= available && cost > c.cost {
			used += cost - c.cost
			c.retained, c.cost, c.reason = retained, cost, reason
			return true, nil
		}
		return false, nil
	}
	allocate := func(indices []int, budget int, reason string) error {
		allocationEnd = min(target, used+budget)
		for {
			weight := 0
			for _, index := range indices {
				c := &candidates[index]
				if !c.protected && string(c.retained) != string(c.full) {
					weight += []int{4, 2, 1, 1}[c.priority]
				}
			}
			remaining := allocationEnd - used
			if weight == 0 || remaining <= 0 {
				return nil
			}
			progress := false
			for _, index := range indices {
				c := &candidates[index]
				// Redistribute unused shares after complete small items fit.
				// Minimum framing can force omissions in very long timelines,
				// but never lets execution consume the request/discussion shares.
				share := max(96, remaining*[]int{4, 2, 1, 1}[c.priority]/weight)
				changed, err := retain(index, c.cost+share, reason)
				if err != nil {
					return err
				}
				progress = progress || changed
			}
			if !progress {
				return nil
			}
		}
	}
	var families [3][]int
	for _, index := range order {
		families[candidates[index].family] = append(families[candidates[index].family], index)
	}
	// These are reserved opportunities, not ceilings on an item's final size.
	// Unspent capacity flows onward and then back to unfinished items.
	if err := allocate(families[0], (target-used)/3, "request_chain"); err != nil {
		return snapshot, nil, err
	}
	discussionBudget := (target - used) / 2
	discussionStart := used
	// The newest readable report often carries continuation state. Give it a
	// bounded head start before broad timeline coverage, without guessing from
	// words such as "handoff" or granting an unlimited exact-retention exemption.
	if latestReport >= 0 {
		if err := allocate([]int{latestReport}, discussionBudget/4, "latest_report"); err != nil {
			return snapshot, nil, err
		}
	}
	if err := allocate(families[1], discussionBudget-(used-discussionStart), "discussion_coverage"); err != nil {
		return snapshot, nil, err
	}
	if err := allocate(families[2], target-used, "execution_coverage"); err != nil {
		return snapshot, nil, err
	}
	if err := allocate(order, target-used, "shared_surplus"); err != nil {
		return snapshot, nil, err
	}
	snapshot.Items = append(snapshot.Items, notice)
	snapshot.Report = &compactionPressureReport{NoticeTokens: noticeTokens}
	snapshot.Report.After = snapshot.Report.NoticeTokens
	positions := make([]int, len(input)+1)
	for index, c := range candidates {
		before, ok := compactionVisibleStringTokens(input[index])
		if !ok {
			return compactionSnapshot{}, nil, fmt.Errorf("cannot measure original pressure item")
		}
		imagesBefore, _ := compactionImageUsage([]json.RawMessage{input[index]})
		imagesAfter, _ := compactionImageUsage([]json.RawMessage{c.retained})
		itemReport := compactionPressureItemReport{
			Index: index, Before: before, After: c.cost,
			Priority:     []string{"latest_request", "request_or_active", "decision_or_reasoning", "older_evidence"}[c.priority],
			Family:       []string{"requests", "discussion", "execution"}[c.family],
			ImagesBefore: imagesBefore, ImagesAfter: imagesAfter,
			Reason: c.reason, Repetition: c.repetition,
		}
		switch {
		case c.protected:
			itemReport.Priority, itemReport.Reason, itemReport.Disposition = "mandatory", "mandatory_floor", "exact"
		case len(c.retained) == 0:
			itemReport.Disposition, itemReport.Reason, itemReport.Repetition = "dropped", "budget_exhausted", false
		case string(c.retained) == string(input[index]):
			itemReport.Disposition = "exact"
		default:
			itemReport.Excerpt = string(c.retained) != string(c.full)
			kind := jsonString(fields[index], "type")
			itemReport.Historical = kind != "message" && kind != "agent_message" && kind != "compaction"
			switch {
			case itemReport.Excerpt:
				itemReport.Disposition = "excerpt"
			case itemReport.Repetition:
				itemReport.Disposition = "repetition_reduced"
			case imagesAfter < imagesBefore && !itemReport.Historical:
				itemReport.Disposition = "image_omitted"
			default:
				itemReport.Disposition = "historical"
			}
		}
		snapshot.Report.Before += before
		snapshot.Report.After += c.cost
		snapshot.Report.Items = append(snapshot.Report.Items, itemReport)
		positions[index] = len(snapshot.Items)
		if compactionCarriedMessage(input[index]) && string(c.retained) != string(input[index]) {
			snapshot.Carried = append(snapshot.Carried, compactionCarriedItem{Originals: []json.RawMessage{input[index]}, Index: len(snapshot.Items), Removed: len(c.retained) == 0, source: index})
		}
		if len(c.retained) > 0 {
			snapshot.Items = append(snapshot.Items, c.retained)
		}
	}
	snapshot.Report.ImagesBefore, snapshot.Report.ImageBytesBefore = compactionImageUsage(input)
	snapshot.Report.ImagesAfter, snapshot.Report.ImageBytesAfter = compactionImageUsage(snapshot.Items)
	positions[len(input)] = len(snapshot.Items)
	if measured, ok := compactionVisibleStringTokens(snapshot.Items...); !ok || measured > target {
		return compactionSnapshot{}, nil, fmt.Errorf("pressure compaction exceeded target %d", target)
	}
	return snapshot, positions, nil
}

func compactionPressureMessage(role, text string) json.RawMessage {
	textType := "input_text"
	switch role {
	case "user", "developer", "system":
	default:
		role, textType = "assistant", "output_text"
	}
	return mustMarshalJSON(map[string]any{"type": "message", "role": role,
		"content": []any{map[string]string{"type": textType, "text": text}}})
}

// Budget excerpts preserve message identity and non-positional metadata;
// positional classifications are rebuilt for the excerpt.
func compactionPressureRender(item map[string]json.RawMessage, role, text string) json.RawMessage {
	kind := jsonString(item, "type")
	// Agent reports are native messages with their own author and recipient, not
	// unattributed assistant prose. Their content uses input_text.
	if kind == "agent_message" {
		role = "user"
	}
	rendered := compactionPressureMessage(role, text)
	var excerpt map[string]json.RawMessage
	_ = json.Unmarshal(rendered, &excerpt)
	var content []json.RawMessage
	_ = json.Unmarshal(excerpt["content"], &content)
	if kind != "message" && kind != "agent_message" {
		return mustMarshalJSON(excerpt)
	}
	message := maps.Clone(item)
	message["content"] = excerpt["content"]
	var metadata map[string]json.RawMessage
	if json.Unmarshal(message["internal_chat_message_metadata_passthrough"], &metadata) == nil && metadata != nil {
		if raw, exists := metadata["content_item_kinds"]; exists {
			var kinds []string
			var parts []json.RawMessage
			_ = json.Unmarshal(item["content"], &parts)
			kind := "unknown"
			if json.Unmarshal(raw, &kinds) == nil && len(kinds) > 0 && len(kinds) == len(parts) &&
				!slices.ContainsFunc(kinds, func(value string) bool { return value == "" || value != kinds[0] }) {
				kind = kinds[0]
			}
			selectedKinds := make([]string, len(content))
			for index := range selectedKinds {
				selectedKinds[index] = "unknown"
			}
			if len(content) == 1 {
				selectedKinds[0] = kind
			}
			metadata["content_item_kinds"] = mustMarshalJSON(selectedKinds)
			message["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(metadata)
		}
	}
	return mustMarshalJSON(message)
}

func compactionPressureExcerpt(text string, pieces []string, budget int) string {
	// Keep bounded exact excerpts, not an invented semantic summary. Important
	// lines in the middle compete for a third of the allowance; prefix/suffix
	// preserve setup and the latest conclusion even for unrecognized content.
	valid := func(value string) string {
		for len(value) > 0 && !utf8.ValidString(value) {
			if !utf8.RuneStart(value[0]) {
				value = value[1:]
			} else {
				value = value[:len(value)-1]
			}
		}
		return value
	}
	var evidence strings.Builder
	remaining := budget / 3
	for line := range strings.SplitSeq(text, "\n") {
		if remaining <= 0 || !compactionPressureImportant.MatchString(line) {
			continue
		}
		_, tokens, err := compactionRetirementTokenCodec.Encode(line + "\n")
		if err != nil {
			continue
		}
		keep := min(len(tokens), remaining)
		evidence.WriteString(valid(strings.Join(tokens[:keep], "")))
		remaining -= keep
	}
	ends := budget - (budget/3 - remaining)
	head := min(len(pieces), ends/2)
	tail := min(len(pieces)-head, ends-head)
	result := "[mekugi excerpt; omitted text is unavailable]\n" + valid(strings.Join(pieces[:head], ""))
	if evidence.Len() > 0 {
		result += "\n[... selected constraint/status excerpts ...]\n" + evidence.String()
	}
	return result + "\n[... omitted ...]\n" + valid(strings.Join(pieces[len(pieces)-tail:], ""))
}

func compactionCarriedMessage(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	return jsonString(fields, "type") == "agent_message" ||
		(jsonString(fields, "type") == "message" && jsonString(fields, "role") == "user")
}
