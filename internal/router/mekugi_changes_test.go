package router

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/yusing/mekugi"
)

func TestTrackedChangeIDsAndRanges(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ workspace, session, call, want string }{
		{"/w", "first", "one", "hp_a1"},
		{"/w", "second", "two", "hp_b1"},
		{"/w", "first", "three", "hp_a2"},
		{"/w", "fork", "one", "hp_a1"},
		{"/other", "first", "one", "hp_a1"},
	} {
		got, err := store.reserveChange(t.Context(), test.workspace, test.session, test.call)
		if err != nil || got != test.want {
			t.Fatalf("%+v: %q, %v", test, got, err)
		}
	}
	got, err := expandChangeRefs([]string{"hp_a1..hp_a3", "hp_a2", "hp_b1"})
	if err != nil || !reflect.DeepEqual(got, []string{"hp_a1", "hp_a2", "hp_a3", "hp_b1"}) {
		t.Fatalf("range = %q, %v", got, err)
	}
	for _, ref := range []string{"hp_a3..hp_a1", "hp_a1..hp_b3", "hp_a01", "hp_a0", "hp_a1..hp_a9999999999999999999999", "hp_a1..hp_a257", "../hp_a1"} {
		if _, err := expandChangeRefs([]string{ref}); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
	if changeStreamName(25) != "z" || changeStreamName(26) != "aa" {
		t.Fatal("invalid stream names")
	}
}

func TestTrackedChangeConcurrentReservation(t *testing.T) {
	directory := t.TempDir()
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			store, err := openMekugiReplayStore(directory)
			if err != nil {
				t.Error(err)
				return
			}
			id, err := store.reserveChange(t.Context(), "/w", "agent", "call")
			if err != nil || id != "hp_a1" {
				t.Errorf("id = %q, %v", id, err)
			}
		})
	}
	workers.Wait()
}

func TestTrackedRecoveryReadAfterRestart(t *testing.T) {
	transform, proxy, _, workspace := newMekugiTestTransform(t, newInProcessMekugiTranslator(t.TempDir()))
	storeDirectory := t.TempDir()
	store, err := openMekugiReplayStore(storeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	proxy.replayStore = store
	file := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(file, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := transform.translate("original", "in file.txt\ntype \"missing\" \"new\"\n", nil)
	if err != nil || first.changeID != "hp_a1" || !strings.HasPrefix(first.translationError, "change hp_a1\n") {
		t.Fatalf("first = %+v, %v", first, err)
	}
	invalid, err := transform.translateRecovery("invalid", `type "not present" "old"`, nil)
	if err != nil || invalid.changeID != first.changeID || !invalid.unevaluated {
		t.Fatalf("invalid = %+v, %v", invalid, err)
	}
	fixed, err := transform.translateRecovery("fixed", `type "missing" "old"`, nil)
	if err != nil || fixed.changeID != first.changeID || !strings.HasPrefix(fixed.report, "change hp_a1\n") {
		t.Fatalf("fixed = %+v, %v", fixed, err)
	}
	if err := transform.commitHistory(); err != nil {
		t.Fatal(err)
	}
	store, err = openMekugiReplayStore(storeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	options := changeReadOptions{workspace: workspace, ids: []string{first.changeID}}
	text, err := store.readChanges(t.Context(), options)
	if err != nil || !strings.Contains(text, "attempts=3") || !strings.Contains(text, "application unconfirmed") ||
		!strings.Contains(text, "-old\n+new\n") || strings.Contains(text, "missing") {
		t.Fatalf("read = %q, %v", text, err)
	}
	// A later edit must not change captured review content.
	if err := os.WriteFile(file, []byte("someone else's edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := store.readChanges(t.Context(), options)
	if err != nil || again != text {
		t.Fatalf("unstable read: %q, %v", again, err)
	}
	proxy.replayStore = store
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{map[string]any{"type": "custom_tool_call_output", "call_id": "fixed", "output": fixed.report}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.reconcileVisibleInput(t.Context(), &request, workspace, "reader-agent"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := store.readChanges(t.Context(), options)
	if err != nil || !strings.Contains(confirmed, "attempt 3 applied") {
		t.Fatalf("confirmation: %q, %v", confirmed, err)
	}
	options.view = "history"
	full, err := store.readChanges(t.Context(), options)
	if err != nil || !strings.Contains(full, `type "missing" "new"`) ||
		!strings.Contains(full, `type "not present" "old"`) || !strings.Contains(full, "evaluated script:") ||
		strings.Count(full, "-old\n+new\n") != 1 {
		t.Fatalf("history = %q, %v", full, err)
	}
	options.workspace = t.TempDir()
	if _, err := store.readChanges(t.Context(), options); err == nil {
		t.Fatal("read another workspace's change")
	}
}

func TestTrackedChangesNoOpPendingAndMissing(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.reserveChange(t.Context(), "/w", "a", "noop")
	if err != nil {
		t.Fatal(err)
	}
	options := changeReadOptions{workspace: "/w", ids: []string{id}}
	pending, err := store.readChanges(t.Context(), options)
	if err != nil || !strings.Contains(pending, "pending") {
		t.Fatalf("pending = %q, %v", pending, err)
	}
	history := mekugiHistory{changeID: id, correlationID: "noop", alreadySatisfied: true}
	if err := store.put(t.Context(), "/w", map[string]mekugiHistory{"noop": history}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.put(t.Context(), "/w", map[string]mekugiHistory{"noop": history}); err != nil {
			t.Fatal(err)
		}
	}
	text, err := store.readChanges(t.Context(), options)
	if err != nil || text != "hp_a1 attempts=1\nattempt 1 no-op\n" {
		t.Fatalf("no-op = %q, %v", text, err)
	}
	if err := os.Remove(filepath.Join(store.directory, replayRecordName("/w", "noop", false))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readChanges(t.Context(), options); err == nil {
		t.Fatal("silently accepted missing replay record")
	}
}

func TestTrackedChangeCursorAndFilters(t *testing.T) {
	text := "hp_a1\nπ changed\n"
	digest, offset, err := changeReadOffset(text, "")
	if err != nil || offset != 0 {
		t.Fatal(err)
	}
	cursor := digest + ":6"
	if _, offset, err := changeReadOffset(text, cursor); err != nil || offset != 6 {
		t.Fatalf("cursor = %d, %v", offset, err)
	}
	for _, invalid := range []string{digest + ":7", digest + ":999", digest + ":-1", "wrong:6"} {
		if _, _, err := changeReadOffset(text, invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	if _, _, err := changeReadOffset(text+"recovered", cursor); err == nil {
		t.Fatal("accepted stale snapshot")
	}
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.reserveChange(t.Context(), "/w", "a", "one")
	if err != nil {
		t.Fatal(err)
	}
	history := mekugiHistory{
		changeID: id, correlationID: "one", applied: true,
		reviewFiles: []mekugi.ReviewFile{{BeforePath: "old", AfterPath: "new", Diff: "wanted\n"}, {AfterPath: "other", Diff: "unrelated\n"}},
	}
	if err := store.put(t.Context(), "/w", map[string]mekugiHistory{"one": history}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"old", "new"} {
		got, err := store.readChanges(t.Context(), changeReadOptions{workspace: "/w", ids: []string{id}, path: path})
		if err != nil || !strings.Contains(got, "wanted") || strings.Contains(got, "unrelated") {
			t.Fatalf("path %s: %q, %v", path, got, err)
		}
	}
}

func TestTrackedChangeReconciliationIsAtomic(t *testing.T) {
	transform, proxy, _, workspace := newMekugiTestTransform(t, newInProcessMekugiTranslator(t.TempDir()))
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy.replayStore = store
	history, err := transform.translate("one", "new f.txt\ntype \"new\\n\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transform.commitHistory(); err != nil {
		t.Fatal(err)
	}
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{
			map[string]any{"type": "custom_tool_call_output", "call_id": "one", "output": history.report},
			map[string]any{"type": "custom_tool_call", "call_id": "one", "name": "wrong-carrier", "input": "wrong"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.reconcileVisibleInput(t.Context(), &request, workspace, "reader"); err == nil {
		t.Fatal("accepted conflicting carrier")
	}
	text, err := store.readChanges(t.Context(), changeReadOptions{workspace: workspace, ids: []string{history.changeID}})
	if err != nil || !strings.Contains(text, "application unconfirmed") {
		t.Fatalf("published partial confirmation: %q, %v", text, err)
	}
}

func TestTrackedChangeQuota(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.maxBytes = 1
	if _, err := store.reserveChange(t.Context(), "/w", "a", "one"); err == nil {
		t.Fatal("accepted index quota overflow")
	}
	if _, err := os.Stat(filepath.Join(store.directory, changeIndexName("/w"))); !os.IsNotExist(err) {
		t.Fatal(fmt.Errorf("published over-quota index: %w", err))
	}
}

func TestTrackedRecoveryNeverAllocatesAChain(t *testing.T) {
	transform, proxy, _, _ := newMekugiTestTransform(t, newInProcessMekugiTranslator(t.TempDir()))
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy.replayStore = store
	orphan, err := transform.translateRecovery("orphan", `type "a" "b"`, nil)
	if err != nil || orphan.changeID != "" || !orphan.unevaluated {
		t.Fatalf("orphan = %+v, %v", orphan, err)
	}
	first, err := transform.translate("first", "new f.txt\ntype \"ok\\n\"\n", nil)
	if err != nil || first.changeID != "hp_a1" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	blocked, err := transform.translateRecovery("blocked", `type "ok" "new"`, nil)
	if err != nil || blocked.changeID != first.changeID || !blocked.unevaluated {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
	second, err := transform.translate("second", "new g.txt\ntype \"ok\\n\"\n", nil)
	if err != nil || second.changeID != "hp_a2" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestTrackedStreamsUseThreadsNotTransportSessions(t *testing.T) {
	transform, proxy, _, _ := newMekugiTestTransform(t, newInProcessMekugiTranslator(t.TempDir()))
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy.replayStore = store
	for _, test := range []struct{ thread, session, call, want string }{
		{"parent", "shared", "one", "hp_a1"},
		{"child", "shared", "two", "hp_b1"},
		{"parent", "changed", "three", "hp_a2"},
	} {
		transform.shellThreadID, transform.sessionID = test.thread, test.session
		history, err := transform.translate(test.call, "", nil)
		if err != nil || history.changeID != test.want {
			t.Fatalf("%+v: %+v, %v", test, history, err)
		}
	}
}

func TestTrackedHistoryIncludesSuccessfulDiagnostics(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.reserveChange(t.Context(), "/w", "a", "one")
	if err != nil {
		t.Fatal(err)
	}
	const warning = "mekugi: warning: outcome hook failed"
	history := mekugiHistory{changeID: id, correlationID: "one", report: changeNotice(id) + "in f.txt\n" + warning + "\n"}
	if err := store.put(t.Context(), "/w", map[string]mekugiHistory{"one": history}); err != nil {
		t.Fatal(err)
	}
	options := changeReadOptions{workspace: "/w", ids: []string{id}, view: "history"}
	text, err := store.readChanges(t.Context(), options)
	if err != nil || strings.Count(text, warning) != 1 || strings.Contains(text, changeNotice(id)) {
		t.Fatalf("history = %q, %v", text, err)
	}
	options.view = ""
	text, err = store.readChanges(t.Context(), options)
	if err != nil || strings.Contains(text, warning) {
		t.Fatalf("default = %q, %v", text, err)
	}
}

func TestTrackedChangeCorruptCounterCannotReplaceID(t *testing.T) {
	for _, corrupt := range []string{"reset", "missing", "advanced"} {
		t.Run(corrupt, func(t *testing.T) {
			store, err := openMekugiReplayStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.reserveChange(t.Context(), "/w", "a", "one"); err != nil {
				t.Fatal(err)
			}
			index, err := store.readChangeIndex("/w")
			if err != nil {
				t.Fatal(err)
			}
			switch corrupt {
			case "reset":
				index.Streams[0].Next = 0
			case "advanced":
				index.Streams[0].Next = 2
			case "missing":
				index.Streams = nil
			}
			data, err := json.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.directory, changeIndexName("/w"))
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.reserveChange(t.Context(), "/w", "a", "two"); err == nil {
				t.Fatal("accepted corrupt stream")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(data) {
				t.Fatal("corruption handling changed persisted IDs")
			}
		})
	}
}

func TestTrackedRetainedScriptScope(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.reserveChange(t.Context(), "/w", "a", "one")
	if err != nil {
		t.Fatal(err)
	}
	history := mekugiHistory{
		changeID: id, correlationID: "one", applied: true,
		script:      "in @shell/prepared\ntype \"old\" \"new\"\n",
		reviewFiles: []mekugi.ReviewFile{{BeforePath: "prepared", AfterPath: "prepared", Diff: "-old\n+new\n"}},
	}
	if err := store.put(t.Context(), "/w", map[string]mekugiHistory{"one": history}); err != nil {
		t.Fatal(err)
	}
	text, err := store.readChanges(t.Context(), changeReadOptions{workspace: "/w", ids: []string{id}})
	if err != nil || !strings.Contains(text, "scope: retained shell script, not workspace files") {
		t.Fatalf("scope = %q, %v", text, err)
	}
}

func TestTrackedNativeFailureIncludesChangeID(t *testing.T) {
	history := mekugiHistory{changeID: "hp_a1", patch: "a proposed patch\n", report: "change hp_a1\nsuccess report\n"}
	script := "apply_patch() { cat >/dev/null; printf 'executor failed\\n'; return 7; }\n" + mekugiNativeCommand(history)
	output, err := exec.CommandContext(t.Context(), "bash", "-c", script).CombinedOutput()
	if err == nil || string(output) != "change hp_a1\nexecutor failed\n" {
		t.Fatalf("failure output = %q, %v", output, err)
	}
}
