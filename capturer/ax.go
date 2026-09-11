package capturer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// AXReadOutputEnvironment opts executor-side private readers into a local journal.
const AXReadOutputEnvironment = "MEKUGI_AX_OUTPUT"

const maxAXEvidenceBytes = 64 << 20

type axReadEvent struct {
	Schema     string    `json:"schema"`
	ID         string    `json:"id"`
	ThreadID   string    `json:"thread_id"`
	Tool       string    `json:"tool"`
	Phase      string    `json:"phase"`
	At         time.Time `json:"at"`
	DurationNS *int64    `json:"duration_ns,omitempty"`
	Succeeded  *bool     `json:"succeeded,omitempty"`
}

// AXReadObservation records one actual invocation, not a parsed source command.
// It retains no command, file path, source text or output in its journal.
type AXReadObservation struct {
	file    *os.File
	event   axReadEvent
	started time.Time
}

func validAXReader(tool string) bool {
	return tool == "hcat" || tool == "hgrep" || tool == "hsymbol" || tool == "inspect_file"
}

// StartAXRead is auxiliary to execution. Callers report failures separately and
// keep the original command outcome. Each event is one O_APPEND write.
func StartAXRead(path, threadID, tool string) (*AXReadObservation, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || !validAXReader(tool) || len(threadID) > 128 ||
		strings.ContainsAny(threadID, "\r\n\x00") {
		return nil, errors.New("invalid AX journal configuration")
	}
	file, err := openAXJournal(path)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		file.Close()
		return nil, err
	}
	now := time.Now()
	observation := &AXReadObservation{
		file: file, started: now,
		event: axReadEvent{Schema: "mekugi.ax.read.v1", ID: hex.EncodeToString(id),
			ThreadID: threadID, Tool: tool, Phase: "start", At: now.UTC()},
	}
	if err := observation.write(); err != nil {
		file.Close()
		return nil, err
	}
	return observation, nil
}

// PrepareAXReadJournal enables a debug bundle before the executor starts.
func PrepareAXReadJournal(path string) error {
	file, err := openAXJournal(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func openAXJournal(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("AX journal path must be absolute")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() >= maxAXEvidenceBytes {
		file.Close()
		return nil, errors.New("AX journal must be a private regular file below 64 MiB")
	}
	return file, nil
}

func (observation *AXReadObservation) write() (writeErr error) {
	data, err := json.Marshal(observation.event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// Coordinate the capacity check and append across workers. Acquisition never
	// blocks in the kernel and has a small bounded retry budget.
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		err := syscall.Flock(int(observation.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	defer func() {
		writeErr = errors.Join(writeErr, syscall.Flock(int(observation.file.Fd()), syscall.LOCK_UN))
	}()
	info, err := observation.file.Stat()
	if err != nil || info.Size()+int64(len(data)) > maxAXEvidenceBytes {
		return errors.New("AX journal exceeds 64 MiB")
	}
	n, err := observation.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

// Finish closes this observation even when recording fails.
func (observation *AXReadObservation) Finish(succeeded bool) error {
	if observation == nil {
		return nil
	}
	observation.event.Phase = "finish"
	observation.event.At = time.Now().UTC()
	observation.event.DurationNS = new(time.Since(observation.started).Nanoseconds())
	observation.event.Succeeded = new(succeeded)
	return errors.Join(observation.write(), observation.file.Close())
}

// AXReadMetrics counts only journal-observed private reader invocations. Incomplete
// observations and missing instrumentation are not zero successful reads.
type AXReadMetrics struct {
	State      string            `json:"state"`
	Started    uint64            `json:"started"`
	Completed  uint64            `json:"completed"`
	Succeeded  uint64            `json:"succeeded"`
	Failed     uint64            `json:"failed"`
	Incomplete uint64            `json:"incomplete"`
	DurationNS uint64            `json:"duration_ns"`
	ByTool     map[string]uint64 `json:"by_tool"`
	Coverage   string            `json:"coverage"`
}

// ReadAXReads computes counts from explicit runtime evidence for one thread.
// Other threads are ignored; event identity and pairing remain validated.
func ReadAXReads(ctx context.Context, path, threadID string) (AXReadMetrics, error) {
	result := AXReadMetrics{
		State: "unavailable", ByTool: map[string]uint64{},
		Coverage: "instrumented private-reader invocations only; external reads and necessity are not measured",
	}
	if path == "" {
		return result, nil
	}
	file, err := openAXEvidence(path, maxAXEvidenceBytes)
	if err != nil {
		return result, err
	}
	defer file.Close()
	reader := &io.LimitedReader{R: file, N: maxAXEvidenceBytes + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4096)
	starts := make(map[string]axReadEvent)
	finished := make(map[string]bool)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var event axReadEvent
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&event) != nil || event.Schema != "mekugi.ax.read.v1" ||
			event.ID == "" || !validAXReader(event.Tool) || event.At.IsZero() ||
			(event.Phase != "start" && event.Phase != "finish") ||
			(event.Phase == "start" && (event.DurationNS != nil || event.Succeeded != nil)) ||
			(event.Phase == "finish" && (event.DurationNS == nil || event.Succeeded == nil || *event.DurationNS < 0)) {
			return result, errors.New("invalid AX read event")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return result, errors.New("trailing AX read event data")
		}
		matched := threadID != "" && event.ThreadID == threadID
		if event.Phase == "start" && len(starts) >= 100000 {
			return result, errors.New("AX read evidence exceeds invocation limit")
		}
		if event.Phase == "start" {
			if _, exists := starts[event.ID]; exists {
				return result, errors.New("duplicate AX read start")
			}
			starts[event.ID] = event
			if matched {
				result.Started++
				result.ByTool[event.Tool]++
			}
			continue
		}
		start, exists := starts[event.ID]
		// Wall timestamps may move backward; elapsed time is recorded monotonically.
		if !exists || finished[event.ID] || start.ThreadID != event.ThreadID || start.Tool != event.Tool {
			return result, errors.New("unpaired AX read finish")
		}
		finished[event.ID] = true
		if !matched {
			continue
		}
		result.Completed++
		if uint64(*event.DurationNS) > ^uint64(0)-result.DurationNS {
			return result, errors.New("AX read durations overflow")
		}
		result.DurationNS += uint64(*event.DurationNS)
		if *event.Succeeded {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	if err := scanner.Err(); err != nil {
		return result, errors.New("AX read journal is unreadable or contains oversized events")
	}
	if reader.N == 0 {
		return result, errors.New("AX read journal exceeds 64 MiB")
	}
	result.Incomplete = result.Started - result.Completed
	if result.Started > 0 {
		result.State = "observed"
	}
	return result, nil
}

func openAXEvidence(path string, limit int64) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		file.Close()
		return nil, errors.New("evidence must be a bounded regular file")
	}
	return file, nil
}

// AXEditMetrics reports measured emissions separately from defect judgments.
type AXEditMetrics struct {
	Calls              uint64 `json:"calls"`
	RecoveryRetries    uint64 `json:"recovery_retries"`
	EmittedBytes       uint64 `json:"emitted_bytes"`
	ReEmittedLineBytes uint64 `json:"re_emitted_line_bytes"`
	Rejected           uint64 `json:"rejected"`
	Unconfirmed        uint64 `json:"unconfirmed"`
}

// AXEditAccumulator holds only the preceding original payload for exact,
// line-aligned repetition measurement, never a durable script history.
type AXEditAccumulator struct {
	Metrics  AXEditMetrics
	previous string
}

func (accumulator *AXEditAccumulator) Observe(script string, retry, rejected, unconfirmed bool) {
	accumulator.Metrics.Calls++
	accumulator.Metrics.EmittedBytes += uint64(len(script))
	if retry {
		accumulator.Metrics.RecoveryRetries++
	}
	if rejected {
		accumulator.Metrics.Rejected++
	}
	if unconfirmed {
		accumulator.Metrics.Unconfirmed++
	}
	previous := make(map[string]int)
	for line := range strings.Lines(accumulator.previous) {
		previous[line]++
	}
	for line := range strings.Lines(script) {
		if previous[line] > 0 {
			accumulator.Metrics.ReEmittedLineBytes += uint64(len(line))
			previous[line]--
		}
	}
	accumulator.previous = script
}

type AXCompletionMetrics struct {
	State          string `json:"state"`
	CompletedTurns uint64 `json:"completed_turns"`
	DurationMS     int64  `json:"duration_ms"`
	UnpairedEvents uint64 `json:"unpaired_events"`
}

// AXCompletionAccumulator pairs actual task/turn events by identity. It does not
// infer completion from silence, a tool exit, or an assistant's prose.
type AXCompletionAccumulator struct {
	Metrics   AXCompletionMetrics
	starts    map[string]axCompletionStart
	completed map[string]bool
}

type axCompletionStart struct {
	kind string
	at   time.Time
}

func (accumulator *AXCompletionAccumulator) Observe(kind, id string, at time.Time) {
	if accumulator.starts == nil {
		accumulator.completed = make(map[string]bool)
		accumulator.starts = make(map[string]axCompletionStart)
	}
	if id == "" || at.IsZero() {
		accumulator.Metrics.UnpairedEvents++
		return
	}
	if accumulator.completed[id] {
		accumulator.Metrics.UnpairedEvents++
		return
	}
	if kind == "task_started" || kind == "turn_started" {
		if _, exists := accumulator.starts[id]; exists {
			accumulator.Metrics.UnpairedEvents++
			return
		}
		accumulator.starts[id] = axCompletionStart{kind: kind, at: at}
		return
	}
	start, exists := accumulator.starts[id]
	matchingKind := start.kind == "task_started" && kind == "task_complete" ||
		start.kind == "turn_started" && kind == "turn_complete"
	if !exists || !matchingKind || at.Before(start.at) {
		accumulator.Metrics.UnpairedEvents++
		return
	}
	delete(accumulator.starts, id)
	accumulator.completed[id] = true
	accumulator.Metrics.CompletedTurns++
	durationMS := at.Sub(start.at).Milliseconds()
	if durationMS > math.MaxInt64-accumulator.Metrics.DurationMS {
		accumulator.Metrics.DurationMS = math.MaxInt64
	} else {
		accumulator.Metrics.DurationMS += durationMS
	}
}

func (accumulator *AXCompletionAccumulator) Result() AXCompletionMetrics {
	result := accumulator.Metrics
	result.UnpairedEvents += uint64(len(accumulator.starts))
	result.State = "unavailable"
	if result.CompletedTurns > 0 || result.UnpairedEvents > 0 {
		result.State = "observed"
	}
	return result
}

type AXDefectAssessment struct {
	CallID   string `json:"call_id"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
	SHA256   string `json:"sha256"`
}

type AXDefectMetrics struct {
	AssessedCalls uint64               `json:"assessed_calls"`
	Reported      uint64               `json:"reported_defects"`
	Unassessed    uint64               `json:"unassessed_calls"`
	Assessments   []AXDefectAssessment `json:"assessments"`
}

// ReadAXDefects requires a real evidence artifact for every explicit verdict.
// It records provenance, not a claim that a test failure proves edit causality.
func ReadAXDefects(path string, editCalls map[string]bool) (AXDefectMetrics, error) {
	result := AXDefectMetrics{Unassessed: uint64(len(editCalls)), Assessments: []AXDefectAssessment{}}
	if path == "" {
		return result, nil
	}
	file, err := openAXEvidence(path, 1<<20)
	if err != nil {
		return result, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var assessments []struct {
		CallID   string `json:"call_id"`
		Verdict  string `json:"verdict"`
		Evidence string `json:"evidence"`
	}
	if err := decoder.Decode(&assessments); err != nil {
		return result, errors.New("invalid defect assessment JSON")
	}
	if assessments == nil {
		return result, errors.New("defect assessments must be an array")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return result, errors.New("trailing defect assessment data")
	}
	seen := make(map[string]bool)
	for _, assessment := range assessments {
		if !editCalls[assessment.CallID] || seen[assessment.CallID] ||
			(assessment.Verdict != "defect" && assessment.Verdict != "no_defect") || assessment.Evidence == "" {
			return result, errors.New("defect assessment needs one known edit call, verdict, and evidence path")
		}
		evidencePath := assessment.Evidence
		if !filepath.IsAbs(evidencePath) {
			evidencePath = filepath.Join(filepath.Dir(path), evidencePath)
		}
		evidencePath, err = filepath.Abs(evidencePath)
		if err != nil {
			return result, err
		}
		evidence, err := openAXEvidence(evidencePath, 1<<20)
		if err != nil {
			return result, fmt.Errorf("defect evidence for call %q is unavailable", assessment.CallID)
		}
		data, readErr := io.ReadAll(io.LimitReader(evidence, (1<<20)+1))
		closeErr := evidence.Close()
		if readErr != nil || closeErr != nil || len(data) == 0 || len(data) > 1<<20 {
			return result, errors.New("defect evidence must be nonempty and at most 1 MiB")
		}
		digest := sha256.Sum256(data)
		result.Assessments = append(result.Assessments, AXDefectAssessment{
			CallID: assessment.CallID, Verdict: assessment.Verdict, Evidence: evidencePath, SHA256: hex.EncodeToString(digest[:]),
		})
		seen[assessment.CallID] = true
		result.AssessedCalls++
		result.Unassessed--
		if assessment.Verdict == "defect" {
			result.Reported++
		}
	}
	return result, nil
}
