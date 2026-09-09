package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestHPatchReplayStoreRestartAndConflict(t *testing.T) {
	dir := t.TempDir()
	s, err := openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := hpatchHistory{script: "original", carrierPayload: "delivered", carrierName: "exec", replayCarrier: true, confirmed: true, sequence: 9}
	if err := s.put(t.Context(), "/workspace", map[string]hpatchHistory{"call": h}); err != nil {
		t.Fatal(err)
	}
	s, err = openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.lookup(t.Context(), "/workspace", "call")
	if err != nil || !ok || got.script != h.script || got.confirmed || got.sequence != 0 {
		t.Fatalf("lookup: %#v %v %v", got, ok, err)
	}
	if _, ok, err := s.lookup(t.Context(), "/other", "call"); err != nil || ok {
		t.Fatalf("workspace leak: %v %v", ok, err)
	}
	h.carrierPayload = "changed"
	if err := s.put(t.Context(), "/workspace", map[string]hpatchHistory{"call": h}); err == nil {
		t.Fatal("accepted conflicting carrier")
	}
}

func TestHPatchReplayStoreConcurrencyAndCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			other, err := openHPatchReplayStore(dir)
			if err == nil {
				err = other.put(t.Context(), "/w", map[string]hpatchHistory{"c": {script: "x"}})
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := os.WriteFile(dir+"/"+replayRecordName("/w", "c", false), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lookup(t.Context(), "/w", "c"); err == nil {
		t.Fatal("accepted corrupt record")
	}
}

func TestHPatchReplayStoreQuotaAndCommentary(t *testing.T) {
	s, err := openHPatchReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.putCommentary(t.Context(), "/w", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.hasCommentary(t.Context(), "/w", "id"); err != nil || !ok {
		t.Fatalf("membership %v %v", ok, err)
	}
	s.maxBytes = 1
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": {script: "x"}}); err == nil {
		t.Fatal("accepted quota overflow")
	}
	if _, ok, err := s.lookup(t.Context(), "/w", "c"); err != nil || ok {
		t.Fatalf("partial record %v %v", ok, err)
	}
}

func TestHPatchReplayStoreProgressiveCompletion(t *testing.T) {
	s, err := openHPatchReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hpatchHistory{script: "x", upstreamItem: map[string]json.RawMessage{"status": json.RawMessage(`"in_progress"`), "input": json.RawMessage(`"x"`)}, commentaryMessageIDs: []string{"first"}}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatal(err)
	}
	h.upstreamItem["status"] = json.RawMessage(`"completed"`)
	h.upstreamItem["id"] = json.RawMessage(`"item"`)
	h.commentaryMessageIDs = []string{"second"}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.lookup(t.Context(), "/w", "c")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.upstreamItem["status"]) != `"completed"` || len(got.commentaryMessageIDs) != 2 {
		t.Fatalf("incomplete merge: %#v", got)
	}
	h.upstreamItem["input"] = json.RawMessage(`"other"`)
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err == nil {
		t.Fatal("accepted changed model input")
	}
}

func TestHPatchReplayStoreRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, dir+"/linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := openHPatchReplayStore(dir + "/linked"); err == nil {
		t.Fatal("accepted symlink directory")
	}
	s, err := openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target+"/outside", dir+"/"+replayRecordName("/w", "c", false)); err != nil {
		t.Fatal(err)
	}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": {script: "x"}}); err == nil {
		t.Fatal("accepted symlink record")
	}
}

func TestHPatchReplayStorePreservesProtocolEncoding(t *testing.T) {
	dir := t.TempDir()
	s, err := openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := hpatchHistory{upstreamItem: map[string]json.RawMessage{
		"input": json.RawMessage(`"<>& \u003c \\n \\u003e"`),
	}}
	before, err := marshalProtocolJSON(h.upstreamItem)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatal(err)
	}
	s, err = openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.lookup(t.Context(), "/w", "c")
	if err != nil || !ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	after, err := marshalProtocolJSON(got.upstreamItem)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("protocol spelling changed: %s -> %s", before, after)
	}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatalf("identical retry conflicts: %v", err)
	}
}

func TestHPatchReplayStoreCancelledWrite(t *testing.T) {
	s, err := openHPatchReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.put(ctx, "/w", map[string]hpatchHistory{"c": {script: "x"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v", err)
	}
	if _, ok, err := s.lookup(t.Context(), "/w", "c"); err != nil || ok {
		t.Fatalf("cancelled write retained record: %v %v", ok, err)
	}
}

func TestHPatchReplayStoreSymlinkAncestorCreatesNothing(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, dir+"/linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := openHPatchReplayStore(dir + "/linked/new/replay"); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("created directories through symlink before rejecting it")
	}
}

func TestHPatchReplayStoreCommentaryCannotConsumeCallQuota(t *testing.T) {
	s, err := openHPatchReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := replayRecord{Version: 1, Workspace: "/w", CallID: "c", History: durableHistory(hpatchHistory{script: "x"})}
	encoded, err := marshalProtocolJSON(call)
	if err != nil {
		t.Fatal(err)
	}
	s.maxBytes = int64(len(encoded))
	if err := s.putCommentary(t.Context(), "/w", []string{"commentary"}); err != nil {
		t.Fatal(err)
	}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": {script: "x"}}); err != nil {
		t.Fatalf("commentary displaced replay: %v", err)
	}
	s.maxCommentaryBytes = 1
	if err := s.putCommentary(t.Context(), "/w", []string{"another"}); err == nil {
		t.Fatal("accepted commentary capacity overflow")
	}
	if _, ok, err := s.lookup(t.Context(), "/w", "c"); err != nil || !ok {
		t.Fatalf("commentary overflow lost replay: %v %v", ok, err)
	}
}

func TestDefaultHPatchReplayDirectory(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	got, err := defaultHPatchReplayDirectory()
	if err != nil || got != filepath.Join(state, "hpatch", "replay") {
		t.Fatalf("explicit state path = %q, %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative-state")
	if _, err := defaultHPatchReplayDirectory(); err == nil {
		t.Fatal("accepted relative state home")
	}
	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err = defaultHPatchReplayDirectory()
	if err != nil || got != filepath.Join(home, ".local", "state", "hpatch", "replay") {
		t.Fatalf("fallback state path = %q, %v", got, err)
	}
}

func TestHPatchReplayStoreStructuredFieldWhitespaceRetry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "state", "replay")
	s, err := openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := hpatchHistory{upstreamItem: map[string]json.RawMessage{
		"extension": json.RawMessage(`{ "x": 1, "values": [ "<>&", "\u003c", 2 ] }`),
	}}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatal(err)
	}
	s, err = openHPatchReplayStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err != nil {
		t.Fatalf("unchanged structured field conflicts on retry: %v", err)
	}
	got, ok, err := s.lookup(t.Context(), "/w", "c")
	if err != nil || !ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	before, err := marshalProtocolJSON(h.upstreamItem)
	if err != nil {
		t.Fatal(err)
	}
	after, err := marshalProtocolJSON(got.upstreamItem)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("provider encoding changed: %s -> %s", before, after)
	}
	h.upstreamItem["extension"] = json.RawMessage(`{"x":1,"values":["<>&","<",2]}`)
	if err := s.put(t.Context(), "/w", map[string]hpatchHistory{"c": h}); err == nil {
		t.Fatal("accepted changed escape spelling")
	}
}
