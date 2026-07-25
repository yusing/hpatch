package hpatch

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Run executes the hpatch command-line contract using explicit process boundaries.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, workingDirectory, dataDirectory string) int {
	translateMode := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "translate":
		translateMode = true
	case len(args) == 1 && args[0] == "gain":
		metrics, err := readMetrics(dataDirectory)
		if err != nil {
			return fail(stderr, err.Error())
		}
		if _, err := fmt.Fprintf(stdout, "estimated hpatch output tokens: %d\nestimated apply_patch output tokens: %d\nestimated reduction: %.1f%%\n", metrics.HPatchTokens, metrics.ApplyPatchTokens, metrics.reduction()); err != nil {
			return fail(stderr, fmt.Sprintf("writing gain report: %v", err))
		}
		return 0
	default:
		return fail(stderr, "expected no arguments or exactly: translate or gain")
	}

	script, err := io.ReadAll(stdin)
	if err != nil {
		return fail(stderr, fmt.Sprintf("reading script: %v", err))
	}
	program, err := parse(string(script))
	if err != nil {
		return fail(stderr, err.Error())
	}

	load := func(path string) (loadedFile, error) {
		fullPath := resolveFilesystemPath(workingDirectory, path)
		info, err := os.Stat(fullPath)
		if err != nil {
			return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return loadedFile{}, fmt.Errorf("%s is not a regular file", path)
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
		}
		if !utf8.Valid(content) {
			return loadedFile{}, fmt.Errorf("%s is not UTF-8", path)
		}
		return loadedFile{content: string(content), mode: info.Mode()}, nil
	}
	exists := func(path string) (fs.FileMode, bool, error) {
		info, err := os.Stat(resolveFilesystemPath(workingDirectory, path))
		if err == nil {
			return info.Mode(), true, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}

	changes, err := program.evaluate(load, exists)
	if err != nil {
		return fail(stderr, err.Error())
	}
	if !translateMode && len(changes) == 0 {
		return 0
	}
	if translateMode {
		patch, err := translate(changes)
		if err != nil {
			return fail(stderr, err.Error())
		}
		if dataDirectory != "" {
			if err := recordMetrics(dataDirectory, workingDirectory, string(script), patch); err != nil {
				warn(stderr, err.Error())
			}
		}
		if _, err := io.WriteString(stdout, patch); err != nil {
			return fail(stderr, fmt.Sprintf("writing patch: %v", err))
		}
		return 0
	}
	if dataDirectory != "" {
		patch, err := translate(changes)
		if err != nil {
			warn(stderr, "collecting metrics: "+err.Error())
		} else if err := recordMetrics(dataDirectory, workingDirectory, string(script), patch); err != nil {
			warn(stderr, err.Error())
		}
	}
	if err := commitChanges(workingDirectory, changes, osFileOperations{}); err != nil {
		return fail(stderr, fmt.Sprintf("changing %s: %v", describePaths(changes), err))
	}
	return 0
}

func resolveFilesystemPath(workingDirectory, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workingDirectory, path)
}

func sanitizeDiagnostic(message string) string {
	var sanitized strings.Builder
	for _, character := range message {
		switch {
		case character == '\n':
			sanitized.WriteString("; ")
		case unicode.IsControl(character):
			escaped := strconv.QuoteRune(character)
			sanitized.WriteString(escaped[1 : len(escaped)-1])
		default:
			sanitized.WriteRune(character)
		}
	}
	return sanitized.String()
}

func fail(stderr io.Writer, message string) int {
	message = sanitizeDiagnostic(message)
	_, _ = fmt.Fprintf(stderr, "hpatch: %s\n", message)
	return 1
}

func warn(stderr io.Writer, message string) {
	message = sanitizeDiagnostic(message)
	_, _ = fmt.Fprintf(stderr, "hpatch: warning: %s\n", message)
}
