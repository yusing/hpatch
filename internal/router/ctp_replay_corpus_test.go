package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectCTPReplayCandidatesIgnoresRecordedTokens(t *testing.T) {
	candidates := []ctpReplayCandidate{
		{SessionID: "session-a", TotalTokens: 1},
		{SessionID: "session-b", TotalTokens: 200},
		{SessionID: "session-c", TotalTokens: 30},
	}
	first := selectedCTPReplayIDs(selectCTPReplayCandidates(candidates, 2))
	for index := range candidates {
		candidates[index].TotalTokens = uint64(10_000 - index)
	}
	second := selectedCTPReplayIDs(selectCTPReplayCandidates(candidates, 2))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("selection changed with recorded token counts: %v then %v", first, second)
	}
}

func TestFreezeAndLoadCTPReplayCorpus(t *testing.T) {
	fixture := newCTPReplayCorpusFixture(t, []string{"session-a", "session-b", "current-session"}, "current-session")
	manifestData, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := fmt.Sprintf("%x", sha256.Sum256(manifestData))
	corpus, identity, err := loadCTPReplayCorpus(fixture.manifestPath, ctpReplayTestRepositoryRoot(t), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if identity != wantIdentity {
		t.Fatalf("manifest identity = %q, want %q", identity, wantIdentity)
	}
	if len(corpus) != 2 {
		t.Fatalf("loaded sessions = %d, want 2", len(corpus))
	}
	if fixture.manifest.Selection.CandidateCount != 2 || fixture.manifest.Selection.ExcludedCurrentThreadID != "current-session" {
		t.Fatalf("selection metadata = %#v", fixture.manifest.Selection)
	}
	assertCTPReplayMode(t, filepath.Dir(fixture.manifestPath), 0o700)
	assertCTPReplayMode(t, fixture.manifestPath, 0o600)
	for _, entry := range fixture.manifest.Entries {
		path := filepath.Join(filepath.Dir(fixture.manifestPath), entry.Filename)
		assertCTPReplayMode(t, path, 0o600)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, fixture.sources[entry.SessionID]) {
			t.Fatalf("snapshotted bytes for %s changed", entry.SessionID)
		}
	}
}

func TestLoadCTPReplayCorpusRejectsFixtureDrift(t *testing.T) {
	tests := map[string]func(*testing.T, *ctpReplayCorpusFixture){
		"missing": func(t *testing.T, fixture *ctpReplayCorpusFixture) {
			t.Helper()
			if err := os.Remove(fixture.firstSessionPath()); err != nil {
				t.Fatal(err)
			}
		},
		"drift": func(t *testing.T, fixture *ctpReplayCorpusFixture) {
			t.Helper()
			file, err := os.OpenFile(fixture.firstSessionPath(), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("\n"); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		},
		"duplicate session ID": func(t *testing.T, fixture *ctpReplayCorpusFixture) {
			t.Helper()
			fixture.manifest.Entries[1].SessionID = fixture.manifest.Entries[0].SessionID
			fixture.rewriteManifest(t)
		},
		"session identity mismatch": func(t *testing.T, fixture *ctpReplayCorpusFixture) {
			t.Helper()
			data := syntheticCTPReplaySession(t, "replacement-session", 999)
			if err := os.WriteFile(fixture.firstSessionPath(), data, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			fixture.manifest.Entries[0].Size = int64(len(data))
			fixture.manifest.Entries[0].SHA256 = fmt.Sprintf("%x", digest)
			fixture.rewriteManifest(t)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newCTPReplayCorpusFixture(t, []string{"session-a", "session-b"}, "")
			mutate(t, fixture)
			if _, _, err := loadCTPReplayCorpus(fixture.manifestPath, ctpReplayTestRepositoryRoot(t), t.Logf); err == nil {
				t.Fatal("load succeeded after fixture corruption")
			}
		})
	}
}

func TestLoadCTPReplayCorpusRejectsSameSizeDrift(t *testing.T) {
	fixture := newCTPReplayCorpusFixture(t, []string{"session-a"}, "")
	path := fixture.firstSessionPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte("echo ok"), []byte("echo no"), 1)
	if len(changed) != len(data) || bytes.Equal(changed, data) {
		t.Fatal("fixture mutation must change bytes without changing size")
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = loadCTPReplayCorpus(fixture.manifestPath, ctpReplayTestRepositoryRoot(t), t.Logf)
	if err == nil || !strings.Contains(err.Error(), "sha256 drift") {
		t.Fatalf("load error = %v, want sha256 drift", err)
	}
}

func TestLoadCTPReplayCorpusChecksSymlinkTargets(t *testing.T) {
	for _, test := range []struct {
		name       string
		manifest   bool
		insideRepo bool
	}{
		{name: "manifest into repository", manifest: true, insideRepo: true},
		{name: "session into repository", insideRepo: true},
		{name: "session outside repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCTPReplayCorpusFixture(t, []string{"session-a"}, "")
			repositoryRoot := t.TempDir()
			targetDirectory := repositoryRoot
			if !test.insideRepo {
				targetDirectory = t.TempDir()
			}
			path := fixture.firstSessionPath()
			if test.manifest {
				path = fixture.manifestPath
			}
			target := filepath.Join(targetDirectory, filepath.Base(path))
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadCTPReplayCorpus(fixture.manifestPath, repositoryRoot, t.Logf)
			if test.insideRepo {
				if err == nil || !strings.Contains(err.Error(), "outside the repository") {
					t.Fatalf("load error = %v, want repository exclusion", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFreezeCTPReplayCorpusRefusesExistingDestination(t *testing.T) {
	temporaryRoot := t.TempDir()
	destination := filepath.Join(temporaryRoot, "existing")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "keep")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := freezeCTPReplayCorpus(destination, filepath.Join(temporaryRoot, "source"), "", ctpReplayTestRepositoryRoot(t), t.Logf)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("freeze error = %v, want existing destination error", err)
	}
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(data) != "unchanged" {
		t.Fatalf("existing destination changed: data=%q err=%v", data, readErr)
	}
}

func TestChooseCTPReplayVariantIsDeterministic(t *testing.T) {
	rollouts := []ctpReplayCandidate{
		{SessionID: "logical-session", RolloutID: "rollout-a", TotalTokens: 1, Data: []byte("first")},
		{SessionID: "logical-session", RolloutID: "rollout-b", TotalTokens: 999, Data: []byte("second")},
	}
	first := chooseCTPReplayVariants(rollouts)
	rollouts[0].TotalTokens = 10_000
	rollouts[1].TotalTokens = 2
	rollouts[0], rollouts[1] = rollouts[1], rollouts[0]
	second := chooseCTPReplayVariants(rollouts)
	if len(first) != 1 || len(second) != 1 || first[0].RolloutID != second[0].RolloutID {
		t.Fatalf("variant selection changed: first=%v second=%v", selectedCTPReplayRolloutIDs(first), selectedCTPReplayRolloutIDs(second))
	}
	sameRolloutID := []ctpReplayCandidate{
		{SessionID: "logical-session", RolloutID: "same-rollout", Data: []byte("variant-z")},
		{SessionID: "logical-session", RolloutID: "same-rollout", Data: []byte("variant-a")},
	}
	first = chooseCTPReplayVariants(sameRolloutID)
	sameRolloutID[0], sameRolloutID[1] = sameRolloutID[1], sameRolloutID[0]
	second = chooseCTPReplayVariants(sameRolloutID)
	if !bytes.Equal(first[0].Data, second[0].Data) {
		t.Fatal("content-hash tie-break changed with enumeration order")
	}
}

func TestFreezeCTPReplayCorpusGroupsLogicalSessionVariants(t *testing.T) {
	temporaryRoot := t.TempDir()
	sourceRoot := filepath.Join(temporaryRoot, "source")
	writeSyntheticCTPReplayVariantSource(t, sourceRoot, "sessions", "one", "rollout-a", "logical-session", 1)
	writeSyntheticCTPReplayVariantSource(t, sourceRoot, "archived_sessions", "two", "rollout-b", "logical-session", 2)
	destination := filepath.Join(temporaryRoot, "frozen")
	manifestPath, _, err := freezeCTPReplayCorpus(destination, sourceRoot, "", ctpReplayTestRepositoryRoot(t), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest ctpReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Selection.EligibleRolloutCount != 2 || manifest.Selection.CandidateCount != 1 || len(manifest.Entries) != 1 {
		t.Fatalf("grouped manifest counts: selection=%#v entries=%d", manifest.Selection, len(manifest.Entries))
	}
	if _, _, err := loadCTPReplayCorpus(manifestPath, ctpReplayTestRepositoryRoot(t), t.Logf); err != nil {
		t.Fatal(err)
	}
}

func TestFreezeCTPReplayCorpusEmptySelectionHasNoManifest(t *testing.T) {
	temporaryRoot := t.TempDir()
	destination := filepath.Join(temporaryRoot, "frozen")
	_, _, err := freezeCTPReplayCorpus(destination, filepath.Join(temporaryRoot, "source"), "", ctpReplayTestRepositoryRoot(t), t.Logf)
	if err == nil || !strings.Contains(err.Error(), "no eligible") {
		t.Fatalf("freeze error = %v, want empty selection error", err)
	}
	if _, statErr := os.Stat(filepath.Join(destination, ctpReplayManifestFilename)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("empty freeze left a manifest: %v", statErr)
	}
}

type ctpReplayCorpusFixture struct {
	manifestPath string
	manifest     ctpReplayManifest
	sources      map[string][]byte
}

func newCTPReplayCorpusFixture(t *testing.T, sessionIDs []string, currentThreadID string) *ctpReplayCorpusFixture {
	t.Helper()
	temporaryRoot := t.TempDir()
	sourceRoot := filepath.Join(temporaryRoot, "source")
	sources := make(map[string][]byte, len(sessionIDs))
	for index, sessionID := range sessionIDs {
		directory := "sessions"
		if index%2 != 0 {
			directory = "archived_sessions"
		}
		sources[sessionID] = writeSyntheticCTPReplaySource(t, sourceRoot, directory, fmt.Sprintf("%d", index), sessionID, uint64(index+1))
	}
	destination := filepath.Join(temporaryRoot, "frozen")
	manifestPath, _, err := freezeCTPReplayCorpus(destination, sourceRoot, currentThreadID, ctpReplayTestRepositoryRoot(t), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest ctpReplayManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return &ctpReplayCorpusFixture{manifestPath: manifestPath, manifest: manifest, sources: sources}
}

func (fixture *ctpReplayCorpusFixture) firstSessionPath() string {
	return filepath.Join(filepath.Dir(fixture.manifestPath), fixture.manifest.Entries[0].Filename)
}

func (fixture *ctpReplayCorpusFixture) rewriteManifest(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(fixture.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(fixture.manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSyntheticCTPReplaySource(t *testing.T, root, directory, filename, sessionID string, totalTokens uint64) []byte {
	t.Helper()
	return writeSyntheticCTPReplayVariantSource(t, root, directory, filename, sessionID, sessionID, totalTokens)
}

func writeSyntheticCTPReplayVariantSource(t *testing.T, root, directory, filename, rolloutID, sessionID string, totalTokens uint64) []byte {
	t.Helper()
	data := syntheticCTPReplayVariant(t, rolloutID, sessionID, totalTokens)
	dir := filepath.Join(root, directory)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+filename+".jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func syntheticCTPReplaySession(t *testing.T, sessionID string, totalTokens uint64) []byte {
	return syntheticCTPReplayVariant(t, sessionID, sessionID, totalTokens)
}

func syntheticCTPReplayVariant(t *testing.T, rolloutID, sessionID string, totalTokens uint64) []byte {
	t.Helper()
	records := []map[string]any{
		{"type": "session_meta", "payload": map[string]any{
			"id":                rolloutID,
			"session_id":        sessionID,
			"base_instructions": map[string]any{"text": "Use apply_patch and exec_command."},
			"dynamic_tools":     []any{},
		}},
		{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "input": "echo ok"}},
		{"type": "event_msg", "payload": map[string]any{"type": "token_count", "info": map[string]any{
			"total_token_usage": map[string]any{"total_tokens": totalTokens},
		}}},
		{"type": "event_msg", "payload": map[string]any{"type": "task_complete"}},
		{"type": "turn_context", "payload": map[string]any{"model": "gpt-test", "effort": "medium"}},
	}
	var data []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	return data
}

func selectedCTPReplayIDs(candidates []ctpReplayCandidate) []string {
	ids := make([]string, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.SessionID
	}
	return ids
}

func selectedCTPReplayRolloutIDs(candidates []ctpReplayCandidate) []string {
	ids := make([]string, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.RolloutID
	}
	return ids
}

func assertCTPReplayMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", filepath.Base(path), got, want)
	}
}

func ctpReplayTestRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
