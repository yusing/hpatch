package router

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReceivedReplyIsFullInChildAndRootCommentary(t *testing.T) {
	for _, messageType := range []string{"MESSAGE", "FINAL_ANSWER"} {
		t.Run(messageType, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
					proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
					root, _ := prepareActivityTest(t, proxy, "root", "r", "", "/root", nil)
					body := strings.Repeat("完整 evidence ", 100) + "FINAL DETAIL"
					envelope := map[string]any{
						"type": "agent_message", "id": "reply", "author": "/root/a", "recipient": "/root/b",
						"content": []any{map[string]any{"type": "input_text", "text": "Message Type: " + messageType + "\nTask name: /root/b\nSender: /root/a\nPayload:\n" + body}},
					}
					child, request := prepareActivityTest(t, proxy, "child", "b", "r", "/root/b", []any{envelope})
					if !bytes.Contains(request.fields["input"], []byte(body)) {
						t.Fatal("original model-visible reply changed")
					}
					response := mustTestJSON(t, map[string]any{"status": "completed", "output": []any{assistantCommentaryMessage("answer", "Substantive answer.")}})
					for _, transform := range []*hpatchResponseTransform{child, root} {
						var output []byte
						if stream {
							events, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.completed", "response": json.RawMessage(response)}))
							if err != nil {
								t.Fatal(err)
							}
							output = bytes.Join(events, nil)
						} else {
							var err error
							output, err = transform.TransformJSON(response)
							if err != nil {
								t.Fatal(err)
							}
						}
						if !bytes.Contains(output, []byte(body)) || bytes.Contains(output, []byte("[excerpt]")) {
							t.Fatal("received reply was shortened")
						}
						if bytes.LastIndex(output, []byte("Substantive answer.")) < bytes.LastIndex(output, []byte(body)) {
							t.Fatal("commentary replaced the substantive answer")
						}
					}
				})
			}
		})
	}
}

func TestReceivedReplyOverBudgetIsOmittedWithoutChangingInput(t *testing.T) {
	body := strings.Repeat("x", maxCommentaryPublicationBytes)
	envelope := func(id, payload string) map[string]any {
		return map[string]any{
			"type": "agent_message", "id": id, "author": "/root/a", "recipient": "/root/b",
			"content": []any{map[string]any{"type": "input_text", "text": "Message Type: MESSAGE\nTask name: /root/b\nSender: /root/a\nPayload:\n" + payload}},
		}
	}
	original := mustMarshalJSON([]any{envelope("oversized", body), envelope("small", "Complete small reply.")})
	fields := map[string]json.RawMessage{"input": original}
	messages := prepareSubagentInputCommentary(fields, "/root/b")
	if !bytes.Equal(fields["input"], original) {
		t.Fatal("oversized reply changed model-visible input")
	}
	if len(messages) != 1 || commentaryText(t, messages[0]) != "[/root/b <- /root/a] Reply received:\nComplete small reply." {
		t.Fatal("oversized reply was excerpted or consumed the next reply's budget")
	}
}

