package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startHpatchControlTest(t *testing.T) (*json.Encoder, *json.Decoder, <-chan error) {
	t.Helper()
	input, send, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	receive, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	done := make(chan error, 1)
	go func() {
		defer output.Close()
		done <- runHpatchControl(ctx, input, output)
	}()
	t.Cleanup(func() {
		cancel()
		input.Close()
		send.Close()
		receive.Close()
	})
	reader := bufio.NewReader(receive)
	ready, err := reader.ReadString('\n')
	if err != nil || ready != hpatchTranslationReady {
		t.Fatalf("ready=%q err=%v", ready, err)
	}
	return json.NewEncoder(send), json.NewDecoder(reader), done
}

func controlTestRequest(t *testing.T, encoder *json.Encoder, decoder *json.Decoder, request hpatchControlRequest) map[string]json.RawMessage {
	t.Helper()
	if err := encoder.Encode(request); err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	readHpatchControlReply(t, encoder, decoder, &response)
	return response
}

func readHpatchControlReply(t *testing.T, encoder *json.Encoder, decoder *json.Decoder, response any) {
	t.Helper()
	var output strings.Builder
	for {
		var frame struct {
			Data string `json:"data"`
			More bool   `json:"more"`
		}
		if err := decoder.Decode(&frame); err != nil {
			t.Fatal(err)
		}
		if len(frame.Data) > 16<<10 {
			t.Fatal("oversized host response frame")
		}
		output.WriteString(frame.Data)
		if !frame.More {
			break
		}
		if err := encoder.Encode(hpatchControlRequest{Operation: "next"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := json.Unmarshal([]byte(output.String()), response); err != nil {
		t.Fatal(err)
	}
}

func TestHpatchControlThreadBindingAndIsolation(t *testing.T) {
	transform, _ := mixedTestTransform(t)
	first, err := transform.retainMixedScript("", "", "shell true", nil)
	if err != nil {
		t.Fatal(err)
	}
	transform.directory = t.TempDir()
	second, err := transform.retainMixedScript("", "", "shell true", nil)
	if err != nil {
		t.Fatal(err)
	}
	one, repliesOne, doneOne := startHpatchControlTest(t)
	two, repliesTwo, doneTwo := startHpatchControlTest(t)
	controlTestRequest(t, one, repliesOne, hpatchControlRequest{Operation: "open", Handle: first.Handle})
	controlTestRequest(t, two, repliesTwo, hpatchControlRequest{Operation: "open", Handle: second.Handle})
	for _, channel := range []struct {
		send    *json.Encoder
		read    *json.Decoder
		root    string
		content string
	}{{one, repliesOne, first.Root, "first"}, {two, repliesTwo, second.Root, "second"}} {
		path := filepath.Join(channel.root, "target.txt")
		if err := os.WriteFile(path, []byte(channel.content), 0o600); err != nil {
			t.Fatal(err)
		}
		response := controlTestRequest(t, channel.send, channel.read, hpatchControlRequest{
			Operation: "translate", Source: "in target.txt\ntype \"" + channel.content + "\" \"changed\"",
		})
		if !strings.Contains(string(response["patch"]), "-"+channel.content) {
			t.Fatalf("translation used another workspace: %s", response["diagnostic"])
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != channel.content {
			t.Fatal("control channel applied workspace edits")
		}
	}
	// A request cannot switch an already-bound channel to another retained handle.
	if err := one.Encode(hpatchControlRequest{Operation: "open", Handle: second.Handle}); err != nil {
		t.Fatal(err)
	}
	if err := <-doneOne; err == nil {
		t.Fatal("channel rebound")
	}
	if err := two.Encode(hpatchControlRequest{Operation: "close"}); err != nil {
		t.Fatal(err)
	}
	if err := <-doneTwo; err != nil {
		t.Fatal(err)
	}
	// Another thread cannot open either handle even when it knows the ID.
	t.Setenv("CODEX_THREAD_ID", "unrelated-thread")
	if err := runHpatchControl(t.Context(), nil, io.Discard); err == nil {
		t.Fatal("missing thread storage accepted")
	}
}

func TestHpatchControlCheckpointAndStaleRevision(t *testing.T) {
	transform, _ := mixedTestTransform(t)
	state, err := transform.retainMixedScript("", "", "shell true", nil)
	if err != nil {
		t.Fatal(err)
	}
	send, receive, done := startHpatchControlTest(t)
	controlTestRequest(t, send, receive, hpatchControlRequest{Operation: "open", Handle: state.Handle})
	progress := json.RawMessage(`{"index":0,"results":[],"operations":[]}`)
	reply := controlTestRequest(t, send, receive, hpatchControlRequest{Operation: "checkpoint", Progress: progress})
	if string(reply["revision"]) != "1" {
		t.Fatalf("revision=%s", reply["revision"])
	}
	name, _ := mixedArtifactName(state.Handle)
	before, err := os.ReadFile(filepath.Join(transform.shellDirectory, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := send.Encode(hpatchControlRequest{Operation: "checkpoint", Progress: progress}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("stale revision accepted")
	}
	after, err := os.ReadFile(filepath.Join(transform.shellDirectory, name))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("stale checkpoint changed state")
	}
}

func TestHpatchControlCarrierHasOneBareCommand(t *testing.T) {
	transform, overrides := mixedTestTransform(t)
	history, err := transform.translate("control-display",
		"shell printf first\nnew out.txt\ntype \"data\\n\"\nshell printf last", nil)
	if err != nil || history.translationError != "" {
		t.Fatalf("translate: %v %s", err, history.translationError)
	}
	carrier := history.carrierInput() + `
if (displayedCommands.length !== 2 || !displayedCommands[0].includes('printf first') || !displayedCommands[1].includes('printf last')) throw new Error('original commands lost');
if (nextControlSession !== 900001 || controlSessions.size !== 0) throw new Error('control channel not opened once and closed');
`
	overrides += `
const displayedCommands = [];
const displayExec = tools.exec_command;
tools.exec_command = async args => {
  displayedCommands.push(args.cmd);
  if (args.cmd.includes('--hpatch-') || args.cmd.includes('mixed-M')) throw new Error('private metadata in command');
  return displayExec(args);
};
`
	var result mixedScriptResult
	runShellCatJavaScript(t, transform.proxy.registry.NodeExecutable, transform.directory, carrier, &result, overrides)
	if result.Sequence.Stopped != "" || result.Sequence.Started != 3 {
		t.Fatalf("result=%+v", result)
	}
}

func TestHpatchControlCloseDuringChunkedReply(t *testing.T) {
	transform, _ := mixedTestTransform(t)
	state, err := transform.retainMixedScript("", "", "shell true", nil)
	if err != nil {
		t.Fatal(err)
	}
	send, receive, done := startHpatchControlTest(t)
	controlTestRequest(t, send, receive, hpatchControlRequest{Operation: "open", Handle: state.Handle})
	source := "new chunked.txt\ntype \"" + strings.Repeat("x", 100000) + "\""
	if err := send.Encode(hpatchControlRequest{Operation: "translate", Source: source}); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Data string `json:"data"`
		More bool   `json:"more"`
	}
	if err := receive.Decode(&frame); err != nil || !frame.More || len(frame.Data) > 16<<10 {
		t.Fatalf("chunk: more=%v bytes=%d err=%v", frame.More, len(frame.Data), err)
	}
	if err := send.Encode(hpatchControlRequest{Operation: "close"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(transform.directory, "chunked.txt")); !os.IsNotExist(err) {
		t.Fatal("closing translation applied workspace edits")
	}
	// A discarded partial reply does not advance or destroy retained state.
	history, err := transform.translate("after-chunk-close", "resume "+state.Handle, nil)
	if err != nil || history.translationError != "" {
		t.Fatalf("resume after partial reply: %v %s", err, history.translationError)
	}
}
