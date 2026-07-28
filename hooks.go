package hpatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const (
	settingsFilename  = "settings.json"
	maxSettingsBytes  = 1 << 20
	errorHooksTimeout = 10 * time.Second
)

type settings struct {
	Hooks hooks `json:"hooks"`
}

type hooks struct {
	Error []string `json:"error"`
}

type errorHookEvent struct {
	Body          string
	Diagnostic    string
	Repair        string
	Command       int
	SourceLine    int
	Operation     string
	Category      string
	Path          string
	FailedCommand string
	Failure       string
}

func runErrorHooks(ctx context.Context, dataDirectory string, sourceError *commandError, diagnostic string, timeout time.Duration) []error {
	if dataDirectory == "" || sourceError.Reason == reasonOther {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return []error{err}
	}

	configured, err := readSettings(dataDirectory)
	if err != nil {
		return []error{err}
	}
	if len(configured.Hooks.Error) == 0 {
		return nil
	}

	event := newErrorHookEvent(sourceError, diagnostic)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var hookErrors []error
	for index, source := range configured.Hooks.Error {
		command, err := renderErrorHook(source, event)
		if err != nil {
			hookErrors = append(hookErrors, fmt.Errorf("rendering error hook %d: %w", index+1, err))
			continue
		}
		if err := executeErrorHook(ctx, command); err != nil {
			hookErrors = append(hookErrors, fmt.Errorf("running error hook %d: %w", index+1, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return hookErrors
}

func readSettings(dataDirectory string) (settings, error) {
	file, err := os.Open(filepath.Join(dataDirectory, settingsFilename))
	if errors.Is(err, os.ErrNotExist) {
		return settings{}, nil
	}
	if err != nil {
		return settings{}, fmt.Errorf("reading settings: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return settings{}, fmt.Errorf("reading settings: %w", err)
	}
	if len(content) > maxSettingsBytes {
		return settings{}, fmt.Errorf("reading settings: file exceeds %d bytes", maxSettingsBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var configured settings
	if err := decoder.Decode(&configured); err != nil {
		return settings{}, fmt.Errorf("decoding settings: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return settings{}, errors.New("decoding settings: trailing JSON value")
		}
		return settings{}, fmt.Errorf("decoding settings: %w", err)
	}
	return configured, nil
}

func newErrorHookEvent(sourceError *commandError, diagnostic string) errorHookEvent {
	event := errorHookEvent{
		Diagnostic:    strings.TrimSuffix(diagnostic, "\n"),
		Repair:        strings.TrimSuffix(sourceError.Repair, "\n"),
		Command:       sourceError.Command,
		SourceLine:    sourceError.Line,
		Operation:     sourceError.Operation,
		Category:      sourceError.Category,
		Path:          sourceError.Path,
		FailedCommand: sourceError.Source,
		Failure:       sourceError.Message,
	}
	event.Body = formatErrorHookMarkdown(event)
	return event
}

func formatErrorHookMarkdown(event errorHookEvent) string {
	var body strings.Builder
	body.WriteString("# hpatch command failed\n\n")
	writeHookField(&body, "Command", strconv.Itoa(event.Command))
	writeHookField(&body, "Source line", strconv.Itoa(event.SourceLine))
	writeHookField(&body, "Operation", event.Operation)
	writeHookField(&body, "Category", event.Category)
	writeHookField(&body, "Path", event.Path)
	writeHookBlock(&body, "Failed command", event.FailedCommand)
	writeHookBlock(&body, "Failure", event.Failure)
	writeHookBlock(&body, "Diagnostic", event.Diagnostic)
	if event.Repair != "" {
		writeHookBlock(&body, "Repair context", event.Repair)
	}
	return strings.TrimSuffix(body.String(), "\n")
}

func writeHookField(body *strings.Builder, label, value string) {
	if value == "" || value == "0" {
		return
	}
	fmt.Fprintf(body, "- %s: %s\n", label, markdownCodeSpan(value))
}

func markdownCodeSpan(value string) string {
	longestRun := 0
	for remaining := value; ; {
		start := strings.IndexByte(remaining, '`')
		if start < 0 {
			break
		}
		remaining = remaining[start:]
		run := len(remaining) - len(strings.TrimLeft(remaining, "`"))
		longestRun = max(longestRun, run)
		remaining = remaining[run:]
	}
	delimiter := strings.Repeat("`", longestRun+1)
	padding := ""
	if strings.Contains(value, "`") || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		padding = " "
	}
	return delimiter + padding + value + padding + delimiter
}

func writeHookBlock(body *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(body, "\n## %s\n\n", label)
	for line := range strings.Lines(value) {
		body.WriteString("    ")
		body.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			body.WriteByte('\n')
		}
	}
}

func renderErrorHook(source string, event errorHookEvent) (string, error) {
	tmpl, err := template.New("error hook").Option("missingkey=error").Funcs(template.FuncMap{
		"format_markdown": func(errorHookEvent) string { return event.Body },
		"shellquote":      shellQuote,
	}).Parse(source)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, event); err != nil {
		return "", err
	}
	if strings.TrimSpace(rendered.String()) == "" {
		return "", errors.New("rendered command is empty")
	}
	return rendered.String(), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func executeErrorHook(ctx context.Context, command string) error {
	process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	err := process.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
