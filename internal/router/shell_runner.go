package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yusing/hpatch/internal/router/toolplugin"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var shellOverflowDiagnostic = fmt.Sprintf(
	"shell: interpreter output exceeds %d bytes\n",
	toolplugin.ExecutionOutputBudgetBytes,
)

const shellCommentaryTruncationDiagnostic = "shell: output truncated by commentary budget\n"

func executeShellTool(
	ctx context.Context,
	manifest toolWorkerManifest,
	runtimeRoot string,
	shellContribution *toolContribution,
	arguments []string,
	stdin *os.File,
	commentarySink shellCommentarySink,
) (toolplugin.ExecutionOutput, error) {
	if commentarySink == nil {
		commentarySink = discardShellCommentarySink{}
	}
	defer func() {
		_ = commentarySink.Complete(context.WithoutCancel(ctx))
	}()
	commentary := newShellCommentaryRuntime(commentarySink)
	if len(arguments) < 2 {
		commentary.startDefault(ctx)
		_ = commentary.terminal(ctx, "failure", "missing interpreter or script body")
		return toolplugin.ExecutionOutput{Stderr: "shell: missing interpreter or script body\n", ExitCode: 1}, nil
	}
	interpreter := shellInterpreterName(arguments[0])
	if interpreter != "bash" && interpreter != "sh" {
		commentary.startDefault(ctx)
		execution, executeErr := toolplugin.Execute(
			ctx,
			manifest.NodeExecutable,
			runtimeRoot,
			shellContribution.Module,
			shellContribution.ModuleIndex,
			arguments,
			stdin,
			"",
			nil,
		)
		if executeErr != nil {
			_ = commentary.terminal(ctx, "failure", "tool execution failed")
			return execution, executeErr
		}
		if execution.ExitCode != 0 {
			reason := fmt.Sprintf("exit status %d", execution.ExitCode)
			_ = commentary.terminal(ctx, "failure", reason)
			execution = truncateShellExecutionForCommentary(execution,
				len("Running the requested commands.")+
					len(shellCommentaryVisibleText(shellCommentaryEvent{
						Text: "Failed: Running the requested commands.", Reason: reason,
					})),
			)
		} else {
			commentary.complete()
			execution = truncateShellExecutionForCommentary(execution, len("Running the requested commands."))
		}
		return execution, nil
	}

	variant := syntax.LangBash
	if interpreter == "sh" {
		variant = syntax.LangPOSIX
	}
	program, err := syntax.NewParser(syntax.Variant(variant)).Parse(
		strings.NewReader(arguments[len(arguments)-1]),
		"",
	)
	if err != nil {
		commentary.startDefault(ctx)
		_ = commentary.terminal(ctx, "failure", "shell syntax error")
		return toolplugin.ExecutionOutput{Stderr: fmt.Sprintf("shell: %v\n", err), ExitCode: 2}, nil
	}
	commentaryPresent, err := instrumentShellCommentary(program, arguments[len(arguments)-1])
	if err != nil {
		commentary.startDefault(ctx)
		_ = commentary.terminal(ctx, "failure", "shell commentary instrumentation failed")
		return toolplugin.ExecutionOutput{Stderr: fmt.Sprintf("shell: %v\n", err), ExitCode: 2}, nil
	}
	budget := newShellOutputBudget()
	if sink, ok := commentarySink.(*httpShellCommentarySink); ok && sink.budget != nil {
		budget = sink.budget
	}
	if !commentaryPresent {
		commentary.sink = &budgetedShellCommentarySink{next: commentary.sink, budget: budget}
		commentary.startDefault(ctx)
	}

	privateTools := make(map[string]toolContribution)
	for _, contribution := range manifest.Tools {
		if contribution.PluginID == builtinToolsPluginID && !contribution.ModelVisible {
			privateTools[contribution.Name] = contribution
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := newShellOutputCapture(cancel, budget)
	middleware := func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(handlerCtx context.Context, command []string) error {
			contribution, private := privateTools[command[0]]
			if !private {
				return next(handlerCtx, command)
			}
			handler := interp.HandlerCtx(handlerCtx)
			input, _ := handler.Stdin.(*os.File)
			execution, executeErr := toolplugin.Execute(
				handlerCtx,
				manifest.NodeExecutable,
				runtimeRoot,
				contribution.Module,
				contribution.ModuleIndex,
				command[1:],
				input,
				handler.Dir,
				shellEnvironment(handler.Env),
			)
			if executeErr != nil {
				_, _ = fmt.Fprintf(handler.Stderr, "%s: %v\n", command[0], executeErr)
				return interp.ExitStatus(1)
			}
			if _, writeErr := io.WriteString(handler.Stdout, execution.Stdout); writeErr != nil {
				return fmt.Errorf("write %s stdout: %w", command[0], writeErr)
			}
			if _, writeErr := io.WriteString(handler.Stderr, execution.Stderr); writeErr != nil {
				return fmt.Errorf("write %s stderr: %w", command[0], writeErr)
			}
			recordToolExecutionMetrics(handlerCtx, manifest.MetricsDir, contribution, execution)
			if execution.ExitCode != 0 {
				return interp.ExitStatus(execution.ExitCode)
			}
			return nil
		}
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		_ = commentary.terminal(ctx, "failure", "working directory is unavailable")
		return toolplugin.ExecutionOutput{}, fmt.Errorf("resolve shell working directory: %w", err)
	}
	terminalShell := stdin != nil && term.IsTerminal(int(stdin.Fd()))
	runner, err := interp.New(
		interp.Env(expand.ListEnviron(os.Environ()...)),
		interp.Dir(workingDirectory),
		interp.Params(arguments[1:len(arguments)-1]...),
		interp.StdIO(stdin, &capture.stdout, &capture.stderr),
		interp.CallHandler(shellCommentaryCallHandler(commentary)),
		interp.ExecHandler(middleware(func(ctx context.Context, arguments []string) error {
			return executeExternalShellCommand(ctx, arguments, terminalShell)
		})),
	)
	if err != nil {
		_ = commentary.terminal(ctx, "failure", "shell interpreter setup failed")
		return toolplugin.ExecutionOutput{Stderr: fmt.Sprintf("shell: %v\n", err), ExitCode: 1}, nil
	}
	pipefail, err := syntax.NewParser(syntax.Variant(variant)).Parse(strings.NewReader("set -o pipefail"), "")
	if err != nil {
		_ = commentary.terminal(ctx, "failure", "shell pipefail setup failed")
		return toolplugin.ExecutionOutput{}, fmt.Errorf("prepare shell pipefail: %w", err)
	}
	if err := runner.Run(runCtx, pipefail); err != nil {
		_ = commentary.terminal(ctx, "failure", "shell pipefail setup failed")
		return toolplugin.ExecutionOutput{}, fmt.Errorf("enable shell pipefail: %w", err)
	}
	runErr := runner.Run(runCtx, program)
	if ctx.Err() != nil {
		outcome := "cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcome = "timeout"
		}
		_ = commentary.terminal(context.WithoutCancel(ctx), outcome, ctx.Err().Error())
		return toolplugin.ExecutionOutput{}, ctx.Err()
	}
	if runErr != nil {
		reason := "shell evaluation failed"
		if status, ok := errors.AsType[interp.ExitStatus](runErr); ok {
			reason = fmt.Sprintf("exit status %d", status)
		}
		_ = commentary.terminal(ctx, "failure", reason)
	}
	stdout, stderr, overflow, commentaryTruncated := capture.result()
	if overflow {
		if runErr == nil {
			_ = commentary.terminal(ctx, "failure", "shell output exceeds the tool output budget")
		}
		var stdoutValid, stderrValid bool
		stdout, stdoutValid = trimIncompleteUTF8Tail(stdout)
		stderr, stderrValid = trimIncompleteUTF8Tail(stderr)
		if !stdoutValid || !stderrValid {
			return toolplugin.ExecutionOutput{Stderr: "shell: interpreter output is not UTF-8\n", ExitCode: 1}, nil
		}
		return toolplugin.ExecutionOutput{Stdout: stdout, Stderr: stderr + shellOverflowDiagnostic, ExitCode: 1}, nil
	}
	if !utf8.ValidString(stdout) || !utf8.ValidString(stderr) {
		if runErr == nil {
			_ = commentary.terminal(ctx, "failure", "shell output is not UTF-8")
		}
		return toolplugin.ExecutionOutput{Stderr: "shell: interpreter output is not UTF-8\n", ExitCode: 1}, nil
	}
	if commentaryTruncated {
		stderr += shellCommentaryTruncationDiagnostic
	}
	if runErr == nil {
		commentary.complete()
		return toolplugin.ExecutionOutput{Stdout: stdout, Stderr: stderr, ExitCode: 0}, nil
	}
	if status, ok := errors.AsType[interp.ExitStatus](runErr); ok {
		return toolplugin.ExecutionOutput{Stdout: stdout, Stderr: stderr, ExitCode: int(status)}, nil
	}
	return toolplugin.ExecutionOutput{
		Stdout:   stdout,
		Stderr:   stderr + fmt.Sprintf("shell: %v\n", runErr),
		ExitCode: 1,
	}, nil
}

func trimIncompleteUTF8Tail(value string) (string, bool) {
	if utf8.ValidString(value) {
		return value, true
	}
	encoded := []byte(value)
	start := len(encoded) - 1
	for start > 0 && !utf8.RuneStart(encoded[start]) {
		start--
	}
	if utf8.FullRune(encoded[start:]) || !utf8.Valid(encoded[:start]) {
		return "", false
	}
	return string(encoded[:start]), true
}

func executeExternalShellCommand(ctx context.Context, arguments []string, terminalShell bool) error {
	handler := interp.HandlerCtx(ctx)
	path, err := interp.LookPathDir(handler.Dir, handler.Env, arguments[0])
	if err != nil {
		_, _ = fmt.Fprintln(handler.Stderr, err)
		return interp.ExitStatus(127)
	}
	command := exec.CommandContext(ctx, path, arguments[1:]...)
	command.Args = arguments
	command.Dir = handler.Dir
	command.Env = shellEnvironment(handler.Env)
	command.Stdin = handler.Stdin
	command.Stdout = handler.Stdout
	command.Stderr = handler.Stderr
	if terminalShell {
		// mvdan does not coordinate terminal foreground-group handoff across a
		// pipeline, so every command in a PTY-backed shell must remain in the
		// worker's foreground group. WaitDelay still bounds inherited pipes.
		command.WaitDelay = 2 * time.Second
	} else {
		toolplugin.ConfigureProcessGroup(command)
	}
	err = command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return interp.ExitStatus(shellProcessExitCode(exitErr))
	}
	if execErr, ok := errors.AsType[*exec.Error](err); ok {
		_, _ = fmt.Fprintln(handler.Stderr, execErr)
		return interp.ExitStatus(127)
	}
	return err
}

// Source: plugins/shell.mjs:158:160 interpreterBasename.
func shellInterpreterName(interpreter string) string {
	base := filepath.Base(strings.ReplaceAll(interpreter, "\\", "/"))
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base
}

func shellEnvironment(environment expand.Environ) []string {
	values := make(map[string]string)
	environment.Each(func(name string, variable expand.Variable) bool {
		if variable.IsSet() && variable.Exported {
			values[name] = variable.String()
		}
		return true
	})
	names := slices.Sorted(maps.Keys(values))
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

type shellOutputCapture struct {
	mu                  sync.Mutex
	stdout              shellOutputWriter
	stderr              shellOutputWriter
	budget              *shellOutputBudget
	nativeRemaining     int
	overflow            bool
	commentaryTruncated bool
	cancel              context.CancelFunc
}

type shellOutputBudget struct {
	mu        sync.Mutex
	remaining int
	overdraw  int
}

type budgetedShellCommentarySink struct {
	next   shellCommentarySink
	budget *shellOutputBudget
}

type shellOutputWriter struct {
	capture *shellOutputCapture
	buffer  bytes.Buffer
}

func newShellOutputBudget() *shellOutputBudget {
	return &shellOutputBudget{remaining: toolplugin.ExecutionOutputBudgetBytes}
}

func (b *shellOutputBudget) charge(publish func(int) (int, error)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	charged, err := publish(b.remaining)
	if err != nil {
		return err
	}
	if charged < 0 || charged > b.remaining {
		return errors.New("commentary publisher exceeded the shared output budget")
	}
	b.remaining -= charged
	return nil
}

func (b *shellOutputBudget) reserve(size int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	accepted := min(size, b.remaining)
	b.remaining -= accepted
	b.overdraw += size - accepted
}

func (b *shellOutputBudget) take(size int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	accepted := min(size, b.remaining)
	b.remaining -= accepted
	return accepted
}

func (b *shellOutputBudget) overdrawn() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overdraw
}

func (s *budgetedShellCommentarySink) Publish(ctx context.Context, event shellCommentaryEvent) error {
	if err := s.next.Publish(ctx, event); err != nil {
		return err
	}
	s.budget.reserve(len(shellCommentaryVisibleText(event)))
	return nil
}

func (s *budgetedShellCommentarySink) Complete(ctx context.Context) error {
	return s.next.Complete(ctx)
}

func newShellOutputCapture(cancel context.CancelFunc, budget *shellOutputBudget) *shellOutputCapture {
	// Match the JavaScript executor's three-byte UTF-8 boundary reserve so both
	// interpreter paths keep the overflow diagnostic inside the shared budget.
	diagnosticReserve := max(len(shellOverflowDiagnostic), len(shellCommentaryTruncationDiagnostic)) + 3
	budget.reserve(diagnosticReserve)
	capture := &shellOutputCapture{
		budget: budget, nativeRemaining: max(0, toolplugin.ExecutionOutputBudgetBytes-diagnosticReserve), cancel: cancel,
	}
	capture.stdout.capture = capture
	capture.stderr.capture = capture
	return capture
}

func (writer *shellOutputWriter) Write(value []byte) (int, error) {
	written := len(value)
	capture := writer.capture
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.overflow {
		return written, nil
	}
	nativeAccepted := min(len(value), capture.nativeRemaining)
	capture.nativeRemaining -= nativeAccepted
	accepted := capture.budget.take(nativeAccepted)
	_, _ = writer.buffer.Write(value[:accepted])
	if accepted != nativeAccepted {
		capture.commentaryTruncated = true
	}
	if nativeAccepted != len(value) {
		capture.overflow = true
		capture.cancel()
	}
	return written, nil
}

func (capture *shellOutputCapture) result() (stdout, stderr string, overflow, commentaryTruncated bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if trim := capture.budget.overdrawn(); trim != 0 {
		trim = trimShellOutputBuffer(&capture.stderr.buffer, trim)
		_ = trimShellOutputBuffer(&capture.stdout.buffer, trim)
		capture.commentaryTruncated = true
	}
	return capture.stdout.buffer.String(), capture.stderr.buffer.String(), capture.overflow, capture.commentaryTruncated
}

func trimShellOutputBuffer(buffer *bytes.Buffer, size int) int {
	removed := min(buffer.Len(), size)
	buffer.Truncate(buffer.Len() - removed)
	return size - removed
}

func truncateShellExecutionForCommentary(execution toolplugin.ExecutionOutput, commentaryBytes int) toolplugin.ExecutionOutput {
	limit := max(0, toolplugin.ExecutionOutputBudgetBytes-commentaryBytes-len(shellCommentaryTruncationDiagnostic))
	if len(execution.Stdout)+len(execution.Stderr) <= limit {
		return execution
	}
	stdoutBytes := min(len(execution.Stdout), limit)
	execution.Stdout = execution.Stdout[:stdoutBytes]
	remaining := limit - stdoutBytes
	execution.Stderr = execution.Stderr[:min(len(execution.Stderr), remaining)]
	execution.Stdout, _ = trimIncompleteUTF8Tail(execution.Stdout)
	execution.Stderr, _ = trimIncompleteUTF8Tail(execution.Stderr)
	execution.Stderr += shellCommentaryTruncationDiagnostic
	return execution
}
