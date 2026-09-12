package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type journalDelivery struct {
	thread    string
	revisions map[string]uint64
}

func (t *mekugiResponseTransform) prepareJournalDelivery(terminal bool) ([]map[string]json.RawMessage, error) {
	if !t.journalActive {
		return nil, nil
	}
	// Journal writes atomically replace the file. Avoid decoding an unchanged
	// multi-megabyte journal for every provider delta when no live notices remain.
	// A concurrent change is observed at the next event; terminals always refresh.
	if !terminal && t.journalQuietFile != nil && t.proxy.replayStore != nil {
		info, err := os.Lstat(filepath.Join(t.proxy.replayStore.directory, journalFilename(t.directory, t.shellThreadID)))
		if err == nil && os.SameFile(info, t.journalQuietFile) &&
			info.Size() == t.journalQuietFile.Size() && info.ModTime() == t.journalQuietFile.ModTime() {
			return nil, nil
		}
	}
	t.journalQuietFile = nil
	release, err := t.proxy.journals.lockDelivery(t.ctx, t.proxy.replayStore)
	if err != nil {
		return nil, err
	}
	t.journalDeliveryRelease = release
	var journal threadJournal
	t.proxy.journals.mu.Lock()
	read := func() error {
		var exists bool
		if t.proxy.replayStore != nil {
			if !terminal {
				t.journalQuietFile, _ = os.Lstat(filepath.Join(t.proxy.replayStore.directory, journalFilename(t.directory, t.shellThreadID)))
			}
			journal, exists, err = readThreadJournal(t.proxy.replayStore, t.directory, t.shellThreadID)
		} else {
			journal, exists = t.proxy.journals.memory[journalKey(t.directory, t.shellThreadID)]
			journal = journal.clone()
		}
		if err != nil {
			return err
		}
		if !exists {
			journal = threadJournal{Author: "/root"}
			if t.commentaryAuthor != "" {
				journal.Author = t.commentaryAuthor
			}
		}
		return nil
	}
	if t.proxy.replayStore != nil {
		err = t.proxy.replayStore.locked(t.ctx, read)
	} else {
		err = read()
	}
	t.proxy.journals.mu.Unlock()
	if err != nil {
		t.ReleaseDelivery()
		return nil, err
	}
	for _, item := range journal.Items {
		if item.ReportNow && !item.Reported && len(attributedCommentary(t.commentaryAuthor, item.Text)) <= maxCommentaryPublicationBytes-t.journalLiveBytes {
			t.journalQuietFile = nil
			break
		}
	}
	for _, retraction := range journal.Retractions {
		if len(attributedCommentary(t.commentaryAuthor, "Retracted `"+retraction.ID+"`.")) <= maxCommentaryPublicationBytes-t.journalLiveBytes {
			t.journalQuietFile = nil
			break
		}
	}
	var messages []map[string]json.RawMessage
	var retentionErr error
	emit := func(text string, revisions map[string]uint64, source string) {
		id := commentaryMessageID("journal\x00" + t.directory + "\x00" + t.shellThreadID + "\x00" + source)
		message := assistantCommentaryMessage(id, text)
		if len(t.retainCommentary(message)) == 0 {
			if terminal {
				retentionErr = errors.New("cannot retain journal terminal delivery")
			}
			return
		}
		if t.journalDeliveries == nil {
			t.journalDeliveries = make(map[string]journalDelivery)
		}
		t.journalDeliveries[id] = journalDelivery{thread: t.shellThreadID, revisions: revisions}
		traceSource := "report_now"
		if terminal {
			traceSource = "terminal_flush"
		}
		t.featureTrace.record("journal", traceSource, "render", "prepared", "", id)

		messages = append(messages, message)
	}
	for _, retraction := range journal.Retractions {
		text := attributedCommentary(t.commentaryAuthor, "Retracted `"+retraction.ID+"`.")
		if !terminal && len(text) > maxCommentaryPublicationBytes-t.journalLiveBytes {
			continue
		}
		emit(text, map[string]uint64{retraction.ID: retraction.Sequence}, fmt.Sprintf("retract:%d", retraction.Sequence))
		if !terminal {
			t.journalLiveBytes += len(text)
		}
	}
	if terminal {
		var text strings.Builder
		text.WriteString("Journal " + commentaryCode(journal.Author))
		revisions := make(map[string]uint64)
		shown := 0
		for _, item := range journal.Items {
			if item.Reported {
				shown++
				continue
			}
			text.WriteString("\n- " + commentaryCode(item.ID) + " " + item.Text)
			revisions[item.ID] = item.Updated
		}
		t.journalNewCount, t.journalShownCount = len(revisions), shown
		if len(revisions) != 0 {
			if text.Len() > maxJournalFlushBytes {
				t.ReleaseDelivery()
				return nil, fmt.Errorf("journal flush exceeds terminal capacity")
			}
			emit(text.String(), revisions, fmt.Sprintf("flush:%d", journal.Sequence))
		}
	} else {
		for _, item := range journal.Items {
			if item.Reported || !item.ReportNow {
				continue
			}
			text := attributedCommentary(t.commentaryAuthor, item.Text)
			if len(text) > maxCommentaryPublicationBytes-t.journalLiveBytes {
				continue
			}
			emit(text, map[string]uint64{item.ID: item.Updated}, fmt.Sprintf("item:%d", item.Updated))
			t.journalLiveBytes += len(text)
		}
	}
	if retentionErr != nil {
		t.ReleaseDelivery()
		return nil, retentionErr
	}
	if len(messages) == 0 {
		t.ReleaseDelivery()
	}
	return messages, nil
}

// The transport confirms only after a successful downstream write/flush. The
// delivery lease remains held until the whole batch is finished or abandoned.
func (t *mekugiResponseTransform) Delivered(payload []byte) {
	var envelope struct {
		Type     string                       `json:"type"`
		Status   string                       `json:"status"`
		Item     map[string]json.RawMessage   `json:"item"`
		Output   []map[string]json.RawMessage `json:"output"`
		Response struct {
			Output []map[string]json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return
	}
	if envelope.Type == "response.completed" || envelope.Status == "completed" {
		t.finalAnswer.suppressed = nil
	}
	if len(t.journalDeliveries) == 0 && t.journalUsageID == "" {
		return
	}
	items := append(envelope.Output, envelope.Response.Output...)
	if envelope.Item != nil {
		items = append(items, envelope.Item)
	}
	for _, item := range items {
		id := jsonString(item, "id")
		if id == t.journalUsageID {
			t.proxy.activity.collect(t.threadID, id, "usage", commentaryMessageText(item))
			t.journalUsageID = ""
		}
		delivery, ok := t.journalDeliveries[id]
		if !ok {
			continue
		}
		if err := t.proxy.journals.acknowledge(t.ctx, t.proxy.replayStore, t.directory, delivery.thread, delivery.revisions); err != nil {
			continue
		}
		delete(t.journalDeliveries, id)
		kind := "journal"
		if strings.HasPrefix(commentaryMessageText(item), "Journal ") {
			kind = "journal_flush"
		}
		t.proxy.activity.collect(t.threadID, id, kind, commentaryMessageText(item))
	}
}

func commentaryMessageText(item map[string]json.RawMessage) string {
	var content []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(item["content"], &content) != nil {
		return ""
	}
	var text strings.Builder
	for _, part := range content {
		text.WriteString(part.Text)
	}
	return text.String()
}

func releaseResponseDelivery(transformer responseTransformer) {
	if delivery, ok := transformer.(interface{ ReleaseDelivery() }); ok {
		delivery.ReleaseDelivery()
	}
}

func confirmResponseDelivery(transformer responseTransformer, payload []byte) {
	if delivery, ok := transformer.(interface{ Delivered([]byte) }); ok {
		delivery.Delivered(payload)
	}
}

func (chain responseTransformerChain) ReleaseDelivery() {
	releaseResponseDelivery(chain.first)
	releaseResponseDelivery(chain.second)
}

func (chain responseTransformerChain) Delivered(payload []byte) {
	confirmResponseDelivery(chain.first, payload)
	confirmResponseDelivery(chain.second, payload)
}

func journalTerminalEligible(output []map[string]json.RawMessage) bool {
	hasFinal, hasOtherMessage := false, false
	for _, item := range output {
		if blocksTokenUsage(item) {
			return false
		}
		if !isFinalAnswerMessage(item) {
			hasOtherMessage = hasOtherMessage || jsonString(item, "type") == "message"
			continue
		}
		hasFinal = true
		var content []map[string]json.RawMessage
		if json.Unmarshal(item["content"], &content) != nil {
			return false
		}
		for _, part := range content {
			var text string
			if jsonString(part, "type") != "output_text" || json.Unmarshal(part["text"], &text) != nil {
				return false
			}
		}
	}
	return hasFinal || !hasOtherMessage
}

func (t *mekugiResponseTransform) journalTerminalMessages(response []byte) ([]map[string]json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	counts, observed := t.threadUsageCounts()
	substantive := t.finalAnswer.substantive || t.journalNewCount+t.journalShownCount != 0
	for _, item := range t.journalProviderOutput {
		substantive = substantive || isSubstantiveAnswer(item)
	}
	if usage := formatTokenUsageCommentary(response, counts, observed && t.usageObserved, "completed", substantive); usage != nil {
		retained := t.retainCommentary(usage)
		if len(retained) != 0 {
			t.journalUsageID = jsonString(usage, "id")
			messages = append(messages, retained...)
		}
	}
	if t.subagentTurn {
		id := commentaryMessageID("journal-summary\x00" + jsonResponseID(response))
		message := assistantCommentaryMessage(id, fmt.Sprintf("Journal flushed: %d new, %d already shown", t.journalNewCount, t.journalShownCount))
		message["phase"] = mustMarshalJSON("final_answer")
		retained := t.retainCommentary(message)
		if len(retained) == 0 {
			return nil, errors.New("cannot retain child journal terminal summary")
		}
		messages = append(messages, retained...)
	}
	return messages, nil
}

func jsonResponseID(response []byte) string {
	var identity struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(response, &identity)
	return identity.ID
}

func withoutProviderFinal(output []map[string]json.RawMessage, responseID string) []map[string]json.RawMessage {
	usageID := subagentCommentaryMessageID("usage\x00" + responseID)
	result := make([]map[string]json.RawMessage, 0, len(output))
	for _, item := range output {
		if isFinalAnswerMessage(item) || jsonString(item, "id") == usageID {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (t *mekugiResponseTransform) decorateJournalJSON(payload []byte) ([]byte, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	terminal := jsonString(response, "status") == "completed" && journalTerminalEligible(t.journalProviderOutput)
	messages, err := t.prepareJournalDelivery(terminal)
	if err != nil {
		return nil, err
	}
	var output []map[string]json.RawMessage
	if err := decodeJournalOutput(response["output"], &output); err != nil {
		t.ReleaseDelivery()
		return nil, err
	}
	if terminal {
		output = withoutProviderFinal(output, jsonString(response, "id"))
		output = append(output, messages...)
		terminalMessages, err := t.journalTerminalMessages(payload)
		if err != nil {
			t.ReleaseDelivery()
			return nil, err
		}
		output = append(output, terminalMessages...)
	} else {
		output = append(messages, output...)
	}
	response["output"] = mustMarshalJSON(output)
	return marshalProtocolJSON(response)
}

func (t *mekugiResponseTransform) decorateJournalSSE(original []byte, events [][]byte) ([][]byte, error) {
	if t.journalContinue {
		messages, err := t.prepareJournalDelivery(false)
		if err != nil {
			return nil, err
		}
		var notices [][]byte
		for _, message := range messages {
			notices = append(notices, assistantCommentaryDoneEvent(message))
		}
		return append(notices, events...), nil
	}

	messages, err := t.prepareJournalDelivery(t.journalTerminal)
	if err != nil {
		return nil, err
	}
	var notices [][]byte
	for _, message := range messages {
		notices = append(notices, assistantCommentaryDoneEvent(message))
	}
	if !t.journalTerminal {
		if len(events) != 0 {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(events[0], &event)
			if event.Type == "response.created" {
				return append(append(events[:1:1], notices...), events[1:]...), nil
			}
		}
		return append(notices, events...), nil
	}
	var provider struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(original, &provider); err != nil {
		return nil, err
	}
	terminalMessages, err := t.journalTerminalMessages(provider.Response)
	if err != nil {
		t.ReleaseDelivery()
		return nil, err
	}
	for _, message := range terminalMessages {
		notices = append(notices, assistantCommentaryDoneEvent(message))
	}
	var visible [][]byte
	for _, payload := range events {
		var event struct {
			Type     string                     `json:"type"`
			Item     map[string]json.RawMessage `json:"item"`
			Response map[string]json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		if event.Type == "response.output_item.done" &&
			jsonString(event.Item, "id") == subagentCommentaryMessageID("usage\x00"+jsonResponseID(provider.Response)) {
			continue
		}
		if event.Type == "response.completed" {
			var output []map[string]json.RawMessage
			if err := decodeJournalOutput(event.Response["output"], &output); err != nil {
				return nil, err
			}
			output = withoutProviderFinal(output, jsonString(event.Response, "id"))
			output = append(output, messages...)
			output = append(output, terminalMessages...)
			event.Response["output"] = mustMarshalJSON(output)
			payload, err = replaceRawField(payload, "response", mustMarshalJSON(event.Response))
			if err != nil {
				return nil, err
			}
			visible = append(visible, notices...)
		}
		visible = append(visible, payload)
	}
	return visible, nil
}

func decodeJournalOutput(raw json.RawMessage, output *[]map[string]json.RawMessage) error {
	if len(raw) == 0 {
		*output = nil
		return nil
	}
	return json.Unmarshal(raw, output)
}
