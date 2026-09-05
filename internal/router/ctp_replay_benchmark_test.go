package router

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	ctpReplayMaximumRecordBytes = 64 << 20
	ctpReplayManifestVersion    = 1
	ctpReplaySampleLimit        = 50
	ctpReplaySelectionSeed      = "hpatch-ctp-replay-corpus-v1"
	ctpReplaySelectionAlgorithm = "sha256(seed + NUL + session_id), ascending"
	ctpReplayVariantAlgorithm   = "sha256(seed + NUL + rollout_id), ascending; sha256(content), ascending tie-break"
	ctpReplayEligibility        = "completed stock Codex apply_patch/exec_command session with a string-valued exec call"
	ctpReplayManifestFilename   = "manifest.json"
)

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
	SessionID   string
	RolloutID   string
	TotalTokens uint64
	Data        []byte
	Rank        [sha256.Size]byte
}

type ctpReplayManifest struct {
	SchemaVersion int                        `json:"schema_version"`
	Selection     ctpReplayManifestSelection `json:"selection"`
	Entries       []ctpReplayManifestEntry   `json:"entries"`
}

type ctpReplayManifestSelection struct {
	Seed                    string `json:"seed"`
	Algorithm               string `json:"algorithm"`
	Eligibility             string `json:"eligibility"`
	SampleLimit             int    `json:"sample_limit"`
	CandidateCount          int    `json:"candidate_count"`
	EligibleRolloutCount    int    `json:"eligible_rollout_count"`
	VariantSelection        string `json:"variant_selection"`
	ExcludedCurrentThreadID string `json:"excluded_current_thread_id,omitempty"`
}

type ctpReplayManifestEntry struct {
	SessionID string `json:"session_id"`
	RolloutID string `json:"rollout_id"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type ctpReplayCorpusSession struct {
	Entry       ctpReplayManifestEntry
	TotalTokens uint64
	Snapshots   []ctpReplaySnapshot
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

func BenchmarkCTPSessionReplay(b *testing.B) {
	corpus, _ := loadConfiguredCTPReplayCorpus(b)
	session := corpus[0]
	codec := mustCTP2Codec(b)

	b.ReportAllocs()
	b.ResetTimer()
	var totals ctpReplayTotals
	for b.Loop() {
		var err error
		totals, err = replayCTP2Snapshots(codec, session.Snapshots)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	reportCTPReplayTotals(b, totals)
	b.ReportMetric(float64(session.TotalTokens), "session_recorded_tokens")
}

func BenchmarkCTPCorpusReplay(b *testing.B) {
	corpus, _ := loadConfiguredCTPReplayCorpus(b)
	codec := mustCTP2Codec(b)

	b.ReportAllocs()
	b.ResetTimer()
	var (
		corpusTotals ctpReplayTotals
		savings      []float64
	)
	for b.Loop() {
		corpusTotals = ctpReplayTotals{}
		savings = savings[:0]
		for _, session := range corpus {
			totals, err := replayCTP2Snapshots(codec, session.Snapshots)
			if err != nil {
				b.Fatal(err)
			}
			corpusTotals.add(totals)
			savings = append(savings, ctpReplaySavings(totals.NewNativeTokens, totals.NewCompactTokens))
		}
	}
	b.StopTimer()
	reportCTPReplayTotals(b, corpusTotals)
	b.ReportMetric(float64(len(corpus)), "sessions")
	slices.Sort(savings)
	if len(savings) != 0 {
		middle := len(savings) / 2
		median := savings[middle]
		if len(savings)%2 == 0 {
			median = (savings[middle-1] + savings[middle]) / 2
		}
		b.ReportMetric(median, "median_new_content_savings_pct")
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

func TestFreezeCTPReplayCorpus(t *testing.T) {
	destination := os.Getenv("HPATCH_CTP_REPLAY_FREEZE")
	if destination == "" {
		t.Skip("HPATCH_CTP_REPLAY_FREEZE is not set")
	}
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		root = filepath.Join(home, ".codex")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := freezeCTPReplayCorpus(destination, root, os.Getenv("CODEX_THREAD_ID"), repositoryRoot, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CTP replay freeze complete: sessions frozen, manifest sha256=%s", identity)
}

func loadConfiguredCTPReplayCorpus(tb testing.TB) ([]ctpReplayCorpusSession, string) {
	tb.Helper()
	manifestPath := os.Getenv("HPATCH_CTP_REPLAY_MANIFEST")
	if manifestPath == "" {
		tb.Skip("HPATCH_CTP_REPLAY_MANIFEST is not set")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		tb.Fatalf("resolve repository root: %v", err)
	}
	corpus, identity, err := loadCTPReplayCorpus(manifestPath, repositoryRoot, func(format string, args ...any) {
		tb.Helper()
		tb.Logf(format, args...)
	})
	if err != nil {
		tb.Fatalf("load CTP replay manifest: %v", err)
	}
	tb.Logf("CTP replay manifest sha256=%s sessions=%d", identity, len(corpus))
	return corpus, identity
}

func freezeCTPReplayCorpus(destination, sourceRoot, currentThreadID, repositoryRoot string, logf func(string, ...any)) (string, string, error) {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", "", fmt.Errorf("resolve freeze destination: %w", err)
	}
	outside, err := ctpReplayPathOutsideRepository(destination, repositoryRoot)
	if err != nil {
		return "", "", err
	}
	if !outside {
		return "", "", fmt.Errorf("freeze destination must be outside repository: %s", destination)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", "", fmt.Errorf("freeze destination already exists: %s", destination)
		}
		return "", "", fmt.Errorf("create freeze destination: %w", err)
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return "", "", fmt.Errorf("set freeze destination permissions: %w", err)
	}

	var (
		eligibleRollouts      []ctpReplayCandidate
		scanned               int
		excludedCurrentThread string
	)
	for directoryIndex, directory := range []string{filepath.Join(sourceRoot, "sessions"), filepath.Join(sourceRoot, "archived_sessions")} {
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
			scanned++
			if scanned == 1 || scanned%100 == 0 {
				logf("CTP replay freeze scan: files=%d eligible_rollouts=%d", scanned, len(eligibleRollouts))
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			candidate, eligible, err := inspectCTPReplayCandidate(data)
			if err != nil {
				return err
			}
			if !eligible {
				return nil
			}
			if candidate.SessionID == currentThreadID {
				excludedCurrentThread = currentThreadID
				return nil
			}
			eligibleRollouts = append(eligibleRollouts, candidate)
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("scan Codex transcripts: %w", err)
		}
		logf("CTP replay freeze scan: directories complete=%d/2 files=%d eligible_rollouts=%d", directoryIndex+1, scanned, len(eligibleRollouts))
	}
	if len(eligibleRollouts) == 0 {
		return "", "", errors.New("no eligible completed stock Codex sessions found; destination contains no manifest")
	}
	candidates := chooseCTPReplayVariants(eligibleRollouts)
	selected := selectCTPReplayCandidates(candidates, ctpReplaySampleLimit)
	manifest := ctpReplayManifest{
		SchemaVersion: ctpReplayManifestVersion,
		Selection: ctpReplayManifestSelection{
			Seed:                    ctpReplaySelectionSeed,
			Algorithm:               ctpReplaySelectionAlgorithm,
			Eligibility:             ctpReplayEligibility,
			SampleLimit:             ctpReplaySampleLimit,
			CandidateCount:          len(candidates),
			EligibleRolloutCount:    len(eligibleRollouts),
			VariantSelection:        ctpReplayVariantAlgorithm,
			ExcludedCurrentThreadID: excludedCurrentThread,
		},
		Entries: make([]ctpReplayManifestEntry, 0, len(selected)),
	}
	for index, candidate := range selected {
		logf("CTP replay freeze snapshot: %d/%d", index+1, len(selected))
		filename := fmt.Sprintf("session-%03d.jsonl", index+1)
		path := filepath.Join(destination, filename)
		if err := writeCTPReplayPrivateFile(path, candidate.Data); err != nil {
			return "", "", fmt.Errorf("write snapshotted session %d: %w", index+1, err)
		}
		digest := sha256.Sum256(candidate.Data)
		manifest.Entries = append(manifest.Entries, ctpReplayManifestEntry{
			SessionID: candidate.SessionID,
			RolloutID: candidate.RolloutID,
			Filename:  filename,
			Size:      int64(len(candidate.Data)),
			SHA256:    fmt.Sprintf("%x", digest),
		})
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("encode replay manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	manifestPath := filepath.Join(destination, ctpReplayManifestFilename)
	temporaryManifestPath := filepath.Join(destination, "."+ctpReplayManifestFilename+".tmp")
	if err := writeCTPReplayPrivateFile(temporaryManifestPath, manifestData); err != nil {
		return "", "", fmt.Errorf("write replay manifest: %w", err)
	}
	if err := os.Rename(temporaryManifestPath, manifestPath); err != nil {
		return "", "", fmt.Errorf("publish replay manifest: %w", err)
	}
	identity := sha256.Sum256(manifestData)
	return manifestPath, fmt.Sprintf("%x", identity), nil
}

func selectCTPReplayCandidates(candidates []ctpReplayCandidate, limit int) []ctpReplayCandidate {
	selected := slices.Clone(candidates)
	for index := range selected {
		selected[index].Rank = sha256.Sum256([]byte(ctpReplaySelectionSeed + "\x00" + selected[index].SessionID))
	}
	slices.SortFunc(selected, func(left, right ctpReplayCandidate) int {
		if order := bytes.Compare(left.Rank[:], right.Rank[:]); order != 0 {
			return order
		}
		return strings.Compare(left.SessionID, right.SessionID)
	})
	return selected[:min(limit, len(selected))]
}

func chooseCTPReplayVariants(rollouts []ctpReplayCandidate) []ctpReplayCandidate {
	bySession := make(map[string][]ctpReplayCandidate)
	for _, rollout := range rollouts {
		bySession[rollout.SessionID] = append(bySession[rollout.SessionID], rollout)
	}
	selected := make([]ctpReplayCandidate, 0, len(bySession))
	for _, variants := range bySession {
		slices.SortFunc(variants, func(left, right ctpReplayCandidate) int {
			leftRank := sha256.Sum256([]byte(ctpReplaySelectionSeed + "\x00" + left.RolloutID))
			rightRank := sha256.Sum256([]byte(ctpReplaySelectionSeed + "\x00" + right.RolloutID))
			if order := bytes.Compare(leftRank[:], rightRank[:]); order != 0 {
				return order
			}
			leftContent := sha256.Sum256(left.Data)
			rightContent := sha256.Sum256(right.Data)
			return bytes.Compare(leftContent[:], rightContent[:])
		})
		selected = append(selected, variants[0])
	}
	return selected
}

func loadCTPReplayCorpus(manifestPath, repositoryRoot string, logf func(string, ...any)) ([]ctpReplayCorpusSession, string, error) {
	manifestData, err := readCTPReplayPrivateFile(manifestPath, repositoryRoot)
	if err != nil {
		return nil, "", err
	}
	identity := sha256.Sum256(manifestData)
	var manifest ctpReplayManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode manifest: %w", err)
	}
	if err := validateCTPReplayManifest(manifest); err != nil {
		return nil, "", err
	}
	directory := filepath.Dir(manifestPath)
	corpus := make([]ctpReplayCorpusSession, 0, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		logf("CTP replay validate session: %d/%d", index+1, len(manifest.Entries))
		data, err := readCTPReplayPrivateFile(filepath.Join(directory, entry.Filename), repositoryRoot)
		if err != nil {
			return nil, "", fmt.Errorf("read replay session %d: %w", index+1, err)
		}
		if int64(len(data)) != entry.Size {
			return nil, "", fmt.Errorf("replay session %d size drift", index+1)
		}
		digest := sha256.Sum256(data)
		if fmt.Sprintf("%x", digest) != entry.SHA256 {
			return nil, "", fmt.Errorf("replay session %d sha256 drift", index+1)
		}
		candidate, eligible, err := inspectCTPReplayCandidate(data)
		if err != nil {
			return nil, "", fmt.Errorf("inspect replay session %d: %w", index+1, err)
		}
		if !eligible || candidate.SessionID != entry.SessionID || candidate.RolloutID != entry.RolloutID {
			return nil, "", fmt.Errorf("replay session %d identity mismatch", index+1)
		}
		snapshots, err := loadCTPReplaySnapshotsBytes(data)
		if err != nil {
			return nil, "", fmt.Errorf("load replay session %d: %w", index+1, err)
		}
		corpus = append(corpus, ctpReplayCorpusSession{Entry: entry, TotalTokens: candidate.TotalTokens, Snapshots: snapshots})
	}
	return corpus, fmt.Sprintf("%x", identity), nil
}

func validateCTPReplayManifest(manifest ctpReplayManifest) error {
	selection := manifest.Selection
	if manifest.SchemaVersion != ctpReplayManifestVersion || selection.Seed != ctpReplaySelectionSeed ||
		selection.Algorithm != ctpReplaySelectionAlgorithm || selection.Eligibility != ctpReplayEligibility ||
		selection.SampleLimit != ctpReplaySampleLimit || selection.VariantSelection != ctpReplayVariantAlgorithm {
		return errors.New("unsupported CTP replay manifest schema or selection policy")
	}
	wantEntries := min(selection.CandidateCount, selection.SampleLimit)
	if selection.CandidateCount <= 0 || selection.EligibleRolloutCount < selection.CandidateCount || len(manifest.Entries) != wantEntries {
		return errors.New("CTP replay manifest candidate or entry count is inconsistent")
	}
	seenIDs := make(map[string]struct{}, len(manifest.Entries))
	seenRolloutIDs := make(map[string]struct{}, len(manifest.Entries))
	var previousRank [sha256.Size]byte
	for index, entry := range manifest.Entries {
		wantFilename := fmt.Sprintf("session-%03d.jsonl", index+1)
		if entry.SessionID == "" || entry.RolloutID == "" || entry.Filename != wantFilename || entry.Size < 0 {
			return fmt.Errorf("CTP replay manifest entry %d is invalid", index+1)
		}
		if _, exists := seenIDs[entry.SessionID]; exists {
			return fmt.Errorf("CTP replay manifest has duplicate session ID at entry %d", index+1)
		}
		if _, exists := seenRolloutIDs[entry.RolloutID]; exists {
			return fmt.Errorf("CTP replay manifest has duplicate rollout ID at entry %d", index+1)
		}
		seenIDs[entry.SessionID] = struct{}{}
		seenRolloutIDs[entry.RolloutID] = struct{}{}
		rank := sha256.Sum256([]byte(selection.Seed + "\x00" + entry.SessionID))
		if index != 0 && bytes.Compare(previousRank[:], rank[:]) >= 0 {
			return fmt.Errorf("CTP replay manifest entry %d is out of selection order", index+1)
		}
		previousRank = rank
		decoded, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(decoded) != sha256.Size || len(entry.SHA256) != sha256.Size*2 || strings.ToLower(entry.SHA256) != entry.SHA256 {
			return fmt.Errorf("CTP replay manifest entry %d has invalid sha256", index+1)
		}
	}
	return nil
}

func readCTPReplayPrivateFile(path, repositoryRoot string) ([]byte, error) {
	// Replay inputs already exist, so resolve their final component as well as
	// their parent. A symlink must not hide repository-resident private data.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	outside, err := ctpReplayPathOutsideRepository(resolved, repositoryRoot)
	if err != nil {
		return nil, err
	}
	if !outside {
		return nil, errors.New("CTP replay input must be outside the repository")
	}
	return os.ReadFile(resolved)
}

func writeCTPReplayPrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func ctpReplayPathOutsideRepository(destination, repositoryRoot string) (bool, error) {
	repositoryRoot, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return false, fmt.Errorf("resolve repository root: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return false, fmt.Errorf("resolve freeze destination parent: %w", err)
	}
	resolved := filepath.Join(parent, filepath.Base(destination))
	relative, err := filepath.Rel(repositoryRoot, resolved)
	if err != nil {
		return false, fmt.Errorf("compare freeze destination with repository: %w", err)
	}
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func inspectCTPReplayCandidate(data []byte) (ctpReplayCandidate, bool, error) {
	var (
		metadata    ctpReplaySessionMetadata
		totalTokens uint64
		hasExec     bool
		terminal    string
	)
	err := scanCTPReplayRecords(bytes.NewReader(data), func(record ctpReplayRecord) error {
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
		return ctpReplayCandidate{}, false, fmt.Errorf("inspect Codex transcript: %w", err)
	}
	sessionID := metadata.SessionID
	if sessionID == "" {
		sessionID = metadata.ID
	}
	rolloutID := metadata.ID
	if rolloutID == "" {
		rolloutID = sessionID
	}
	instructions := strings.ToLower(metadata.BaseInstructions.Text)
	stockCodeMode := strings.Contains(instructions, "apply_patch") &&
		strings.Contains(instructions, "exec_command") &&
		!strings.Contains(instructions, "hpatch")
	ok := sessionID != "" && rolloutID != "" && hasExec && terminal == "task_complete" && stockCodeMode
	return ctpReplayCandidate{SessionID: sessionID, RolloutID: rolloutID, TotalTokens: totalTokens, Data: data}, ok, nil
}

func loadCTPReplaySnapshotsBytes(data []byte) ([]ctpReplaySnapshot, error) {
	var (
		metadata         ctpReplaySessionMetadata
		history          []json.RawMessage
		turn             ctpReplayTurnContext
		snapshots        []ctpReplaySnapshot
		resetInputPrefix bool
	)
	err := scanCTPReplayRecords(bytes.NewReader(data), func(record ctpReplayRecord) error {
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
		return nil, fmt.Errorf("load Codex transcript: %w", err)
	}
	if len(snapshots) == 0 {
		return nil, errors.New("load Codex transcript: no request snapshots")
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
		transform, _, err := codec.prepareRequest(&request)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		if transform == nil {
			totals.MissingCarrierRequest++
		}
		totals.Requests++
		nativeBody, err := json.Marshal(native)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		compactBody, err := json.Marshal(request.fields)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		nativeTokens, err := codec.count(nativeBody)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		compactTokens, err := codec.count(compactBody)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		totals.NativeTokens += uint64(nativeTokens)
		totals.CompactTokens += uint64(compactTokens)
		totals.NativeBytes += uint64(len(nativeBody))
		totals.CompactBytes += uint64(len(compactBody))
		representation := measureCTP2Fields(request.fields)
		totals.Definitions += representation.Definitions
		totals.DictionaryBytes += representation.DictionaryBytes
		totals.Strings += representation.Strings
		totals.VisibleReferences += representation.VisibleReferences

		nativeProjection := ctpReplayNewContent(previousNative, native, snapshot.ResetInputPrefix)
		compactProjection := ctpReplayNewContent(previousCompact, request.fields, snapshot.ResetInputPrefix)
		newNativeTokens, err := countCTPReplayFields(codec, nativeProjection)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		newCompactTokens, err := countCTPReplayFields(codec, compactProjection)
		if err != nil {
			return ctpReplayTotals{}, err
		}
		totals.NewNativeTokens += uint64(newNativeTokens)
		totals.NewCompactTokens += uint64(newCompactTokens)
		previousNative = native
		previousCompact = request.fields
	}
	return totals, nil
}

type ctp2RepresentationStats struct {
	Definitions       uint64
	DictionaryBytes   uint64
	Strings           uint64
	VisibleReferences uint64
}

func measureCTP2Fields(fields map[string]json.RawMessage) ctp2RepresentationStats {
	var total ctp2RepresentationStats
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case string:
			measured := measureCTP2String(value)
			total.Definitions += measured.Definitions
			total.DictionaryBytes += measured.DictionaryBytes
			total.Strings += measured.Strings
			total.VisibleReferences += measured.VisibleReferences
		case []any:
			for _, item := range value {
				visit(item)
			}
		case map[string]any:
			for _, item := range value {
				visit(item)
			}
		}
	}
	for _, raw := range fields {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			visit(value)
		}
	}
	return total
}

func measureCTP2String(value string) ctp2RepresentationStats {
	var measured ctp2RepresentationStats
	if strings.HasPrefix(value, ctp2DictionaryTag) {
		measured.Strings = 1
		measured.DictionaryBytes = uint64(len(ctp2DictionaryTag))
		remaining := value[len(ctp2DictionaryTag):]
		for {
			line, rest, ok := strings.Cut(remaining, "\n")
			if !ok {
				return ctp2RepresentationStats{}
			}
			measured.DictionaryBytes += uint64(len(line) + 1)
			remaining = rest
			if line == "END" {
				return measured
			}
			measured.Definitions++
		}
	}
	if strings.HasPrefix(value, ctp2VisibleLinesTag) {
		measured.Strings = 1
		for line := range strings.Lines(value[len(ctp2VisibleLinesTag):]) {
			if strings.HasPrefix(line, "=") {
				measured.VisibleReferences++
			}
		}
	} else if strings.HasPrefix(value, ctp2LiteralTag) {
		measured.Strings = 1
	}
	return measured
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

func scanCTPReplayRecords(reader io.Reader, visit func(ctpReplayRecord) error) error {
	scanner := bufio.NewScanner(reader)
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
