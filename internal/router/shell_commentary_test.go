package router

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type recordingShellCommentarySink struct {
	mu        sync.Mutex
	events    []shellCommentaryEvent
	completed bool
}

func (s *recordingShellCommentarySink) Publish(_ context.Context, event shellCommentaryEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingShellCommentarySink) Complete(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = true
	return nil
}

func runShellCommentaryTest(t *testing.T, source string) ([]shellCommentaryEvent, string, error) {
	t.Helper()
	program, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(source), "")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := instrumentShellCommentary(program, source)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("commentary program was not instrumented")
	}
	sink := new(recordingShellCommentarySink)
	runtime := newShellCommentaryRuntime(sink)
	var stdout bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stdout),
		interp.CallHandler(shellCommentaryCallHandler(runtime)),
	)
	if err != nil {
		t.Fatal(err)
	}
	pipefail, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader("set -o pipefail"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), pipefail); err != nil {
		t.Fatal(err)
	}
	runErr := runner.Run(t.Context(), program)
	return sink.events, stdout.String(), runErr
}

func TestShellCommentaryStandaloneFailureStopsEvaluation(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "commentary Running the check\nfalse\necho continued\n")
	if err == nil {
		t.Fatal("labelled standalone failure did not stop evaluation")
	}
	if output != "" {
		t.Fatalf("output = %q", output)
	}
	if len(events) != 2 || events[0].Text != "Running the check" || events[0].Form != "commentary Running the check" ||
		events[1].Text != "Failed: Running the check" || events[1].Outcome != "failure" ||
		events[1].Reason != "exit status 1" {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellCommentaryUsesFunctionFinalStatus(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "check() { false; echo continued; }\ncommentary Running function\ncheck\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "continued\n" || len(events) != 1 || events[0].Text != "Running function" {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryTrailingPipelineCommandIsUnbound(t *testing.T) {
	events, _, err := runShellCommentaryTest(t, "false | commentary Done\n")
	if err == nil {
		t.Fatal("pipeline failure was lost")
	}
	if len(events) != 1 || events[0].Text != "Done" || events[0].Outcome != "" {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellCommentarySkippedShortCircuitBranchPreservesSuccess(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "false && commentary skipped || echo recovered\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "recovered\n" || len(events) != 0 {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentarySkippedShortCircuitBranchPreservesFailureStatus(t *testing.T) {
	t.Setenv("PATH", "")
	events, output, err := runShellCommentaryTest(t, "( exit 7 ) && commentary skipped\necho status=$?\necho continued\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "status=7\ncontinued\n" || len(events) != 0 {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryConsecutiveLabelsReplacePendingFailure(t *testing.T) {
	events, _, err := runShellCommentaryTest(t, "commentary first\ncommentary second\nfalse\n")
	if err == nil {
		t.Fatal("final false status was lost")
	}
	if len(events) != 3 || events[0].Text != "first" || events[1].Text != "second" ||
		events[2].Text != "Failed: second" || events[2].Form != "" {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellCommentaryCannotBeOverriddenByFunction(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "commentary() { echo hijacked; }\ncommentary safe\ntrue\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "" || len(events) != 1 || events[0].Text != "safe" {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryFinishBranchCannotBeOverriddenByFunction(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "test() { return 1; }\ncommentary Running\nfalse\necho continued\n")
	if err == nil {
		t.Fatal("labelled failure did not stop evaluation")
	}
	if output != "" || len(events) != 2 || events[0].Text != "Running" ||
		events[1].Text != "Failed: Running" || events[1].Outcome != "failure" {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentarySkippedStatusCannotBeOverriddenByExitFunction(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "fail() { return 7; }\nexit() { return 0; }\nfail && commentary skipped\necho status=$?\necho continued\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "status=7\ncontinued\n" || len(events) != 0 {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryZeroStatusCannotBeOverriddenByTrueFunction(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		events, output, err := runShellCommentaryTest(t, "true() { return 9; }\nok() { return 0; }\nok || commentary skipped\necho status=$?\n")
		if err != nil {
			t.Fatal(err)
		}
		if output != "status=0\n" || len(events) != 0 {
			t.Fatalf("output = %q, events = %+v", output, events)
		}
	})

	t.Run("active", func(t *testing.T) {
		events, output, err := runShellCommentaryTest(t, "true() { return 9; }\nprintf() { return 8; }\ncommentary Running\n:\necho continued\n")
		if err != nil {
			t.Fatal(err)
		}
		if output != "continued\n" || len(events) != 1 || events[0].Text != "Running" {
			t.Fatalf("output = %q, events = %+v", output, events)
		}
	})
}

func TestShellCommentaryNoopIsReinstalledAfterEval(t *testing.T) {
	events, output, err := runShellCommentaryTest(t, "commentary Running\neval '__hpatch_commentary_noop() { echo hijacked; return 9; }'\necho continued\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "continued\n" || len(events) != 1 || events[0].Text != "Running" {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryNoopIsReinstalledAfterSource(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("override.sh", []byte("__hpatch_commentary_noop() { echo hijacked; return 9; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	events, output, err := runShellCommentaryTest(t, "commentary Running\n. ./override.sh\necho continued\n")
	if err != nil {
		t.Fatal(err)
	}
	if output != "continued\n" || len(events) != 1 || events[0].Text != "Running" {
		t.Fatalf("output = %q, events = %+v", output, events)
	}
}

func TestShellCommentaryRunsInsideLoop(t *testing.T) {
	events, _, err := runShellCommentaryTest(t, "for i in 1 2; do\n  commentary Running $i/2\n  true\ndone\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Text != "Running 1/2" || events[1].Text != "Running 2/2" {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellCommentaryConcurrentFunctionInvocationsUseDistinctTokens(t *testing.T) {
	events, _, err := runShellCommentaryTest(t, "check() { commentary Running $1; true; }\ncheck left | check right\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	texts := []string{events[0].Text, events[1].Text}
	slices.Sort(texts)
	if !slices.Equal(texts, []string{"Running left", "Running right"}) {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellCommentaryIgnoresRedirection(t *testing.T) {
	t.Chdir(t.TempDir())
	events, _, err := runShellCommentaryTest(t, "commentary status > marker\ntrue\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Text != "status" || events[0].Form != "commentary status > marker" {
		t.Fatalf("events = %+v", events)
	}
	if _, err := os.Stat("marker"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("commentary redirection created a file: %v", err)
	}
}

func TestShellCommentaryBlankExpansionIsMeteredAndGetsFailureFallback(t *testing.T) {
	sink := new(recordingShellCommentarySink)
	runtime := newShellCommentaryRuntime(sink)
	if _, err := runtime.call(t.Context(), io.Discard, []string{shellCommentaryStartCommand, "1", `commentary "$EMPTY"`, "", ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.call(t.Context(), io.Discard, []string{shellCommentaryFinishCommand, "1", "1"}); err == nil {
		t.Fatal("blank labelled failure did not preserve its status")
	}
	if err := runtime.terminal(t.Context(), "failure", "exit status 1"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[0].Text != "" || sink.events[0].Form != `commentary "$EMPTY"` ||
		sink.events[1].Text != "Failed: Running the requested commands." || sink.events[1].Outcome != "failure" {
		t.Fatalf("events = %+v", sink.events)
	}
}
