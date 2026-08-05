package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yusing/hpatch"
)

const (
	hgrepExecutableName       = "hgrep"
	maxHGrepOutputBytes       = 16 << 20
	maxHGrepStderrBytes       = 64 << 10
	maxHGrepControlEventBytes = 64 << 10
	hgrepLimitMessage         = "hgrep: output limit reached; retry with a narrower search\n"
)

var (
	hgrepSilentLongOptions = map[string]bool{
		"line-number": false, "no-column": false, "no-config": false, "no-heading": false,
		"no-json": false, "no-max-columns-preview": false, "no-stats": false, "no-trim": false,
		"with-filename": false,
	}
	hgrepWarnedLongOptions = map[string]bool{
		"block-buffered": false, "column": false, "count": false, "count-matches": false,
		"debug": false, "files": false, "files-with-matches": false, "files-without-match": false,
		"heading": false, "include-zero": false, "json": false, "line-buffered": false,
		"max-columns-preview": false, "no-filename": false, "no-ignore-messages": false,
		"no-line-number": false, "no-messages": false, "null": false, "only-matching": false,
		"passthru": false, "passthrough": false, "pretty": false, "quiet": false, "stats": false,
		"trace": false, "trim": false, "vimgrep": false,
		"color": true, "colors": true, "context-separator": true,
		"field-context-separator": true, "field-match-separator": true,
		"hyperlink-format": true, "max-columns": true, "path-separator": true,
		"replace": true,
	}
	hgrepForbiddenLongOptions = map[string]struct{}{
		"binary": {}, "encoding": {}, "generate": {}, "help": {}, "hostname-bin": {},
		"multiline": {}, "multiline-dotall": {}, "no-binary": {}, "no-text": {},
		"null-data": {}, "pcre2-version": {}, "pre": {}, "pre-glob": {},
		"search-zip": {}, "text": {}, "type-list": {}, "version": {},
	}
	hgrepLongOptionsWithValue = map[string]struct{}{
		"after-context": {}, "before-context": {}, "context": {},
		"dfa-size-limit": {}, "engine": {}, "file": {}, "glob": {},
		"iglob": {}, "ignore-file": {}, "max-count": {}, "max-depth": {},
		"max-filesize": {}, "regex-size-limit": {}, "regexp": {}, "sort": {},
		"sortr": {}, "threads": {}, "type": {}, "type-add": {}, "type-clear": {},
		"type-not": {},
	}
	hgrepSilentShortOptions    = "Hn"
	hgrepWarnedShortOptions    = map[rune]bool{'0': false, 'I': false, 'M': true, 'N': false, 'b': false, 'c': false, 'h': false, 'l': false, 'o': false, 'p': false, 'q': false, 'r': true}
	hgrepForbiddenShortOptions = "EUVaz"
	hgrepShortOptionsWithValue = "ABCTdefgjmt"
)

type hgrepJSONText struct {
	Text  *string `json:"text"`
	Bytes string  `json:"bytes"`
}

type hgrepJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       hgrepJSONText `json:"path"`
		Lines      hgrepJSONText `json:"lines"`
		LineNumber int           `json:"line_number"`
	} `json:"data"`
}

type hgrepRow struct {
	path string
	line int
}

// RunHGrepWorker handles the private child-process mode used by routed hgrep calls.
func RunHGrepWorker(ctx context.Context, argv0 string, args []string, stdout, stderr io.Writer) (bool, int) {
	if filepath.Base(argv0) != hgrepExecutableName {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		_, _ = fmt.Fprintln(stderr, "hgrep:", conciseHReadError(err))
		return true, 1
	}
	arguments, warnings, needsDefaultPath, err := normalizeHGrepArguments(args)
	if len(warnings) != 0 {
		_, _ = fmt.Fprintf(
			stderr,
			"hgrep: warning: ignoring ripgrep options %s; output remains verified rows\n",
			strings.Join(warnings, ", "),
		)
	}
	if err != nil {
		return fail(err)
	}
	if needsDefaultPath {
		arguments = append(arguments, ".")
	}
	output, err := executeHGrep(ctx, arguments)
	if err != nil {
		return fail(err)
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		return fail(fmt.Errorf("writing result: %w", err))
	}
	return true, 0
}

func splitHGrepArguments(input string) ([]string, error) {
	if input == "" {
		return nil, errors.New("input must not be empty")
	}
	if !utf8.ValidString(input) {
		return nil, errors.New("input must be UTF-8")
	}
	var arguments []string
	for offset := 0; offset < len(input); {
		for offset < len(input) && (input[offset] == ' ' || input[offset] == '\t') {
			offset++
		}
		if offset == len(input) {
			break
		}
		var argument strings.Builder
		started := false
		for offset < len(input) && input[offset] != ' ' && input[offset] != '\t' {
			started = true
			switch input[offset] {
			case '\r', '\n':
				return nil, errors.New("input must contain one argument line")
			case '\'', '"':
				quote := input[offset]
				offset++
				for offset < len(input) && input[offset] != quote {
					if input[offset] == '\r' || input[offset] == '\n' {
						return nil, errors.New("quoted argument must not contain a newline")
					}
					if quote == '"' && input[offset] == '\\' {
						offset++
						if offset == len(input) {
							return nil, errors.New("double-quoted argument ends with an escape")
						}
					}
					argument.WriteByte(input[offset])
					offset++
				}
				if offset == len(input) {
					return nil, errors.New("unterminated quoted argument")
				}
				offset++
			case '\\':
				offset++
				if offset == len(input) {
					return nil, errors.New("argument ends with an escape")
				}
				argument.WriteByte(input[offset])
				offset++
			default:
				argument.WriteByte(input[offset])
				offset++
			}
		}
		if started {
			arguments = append(arguments, argument.String())
		}
	}
	if len(arguments) == 0 {
		return nil, errors.New("input must contain at least one argument")
	}
	return arguments, nil
}

func normalizeHGrepArguments(arguments []string) (normalized, warnings []string, needsDefaultPath bool, err error) {
	options := true
	patternFromOption := false
	positionals := 0
	normalized = make([]string, 0, len(arguments))
	warned := make(map[string]struct{})
	addWarning := func(option string) {
		if _, exists := warned[option]; exists {
			return
		}
		warned[option] = struct{}{}
		warnings = append(warnings, option)
	}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if options && argument == "--" {
			options = false
			normalized = append(normalized, argument)
			continue
		}
		if !options || argument == "-" || !strings.HasPrefix(argument, "-") {
			positionals++
			normalized = append(normalized, argument)
			continue
		}
		if strings.HasPrefix(argument, "--") {
			name, _, attached := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
			if hasValue, silent := hgrepSilentLongOptions[name]; silent {
				if hasValue && !attached {
					if index+1 == len(arguments) {
						return normalized, warnings, false, fmt.Errorf("ripgrep option --%s requires a value", name)
					}
					index++
				}
				continue
			}
			if hasValue, ignored := hgrepWarnedLongOptions[name]; ignored {
				addWarning("--" + name)
				if hasValue && !attached {
					if index+1 == len(arguments) {
						return normalized, warnings, false, fmt.Errorf("ripgrep option --%s requires a value", name)
					}
					index++
				}
				continue
			}
			if _, forbidden := hgrepForbiddenLongOptions[name]; forbidden {
				return normalized, warnings, false, fmt.Errorf("ripgrep option --%s is incompatible with verified-row output", name)
			}
			normalized = append(normalized, argument)
			if name == "regexp" || name == "file" {
				patternFromOption = true
			}
			if _, hasValue := hgrepLongOptionsWithValue[name]; hasValue && !attached {
				if index+1 == len(arguments) {
					return normalized, warnings, false, fmt.Errorf("ripgrep option --%s requires a value", name)
				}
				index++
				normalized = append(normalized, arguments[index])
			}
			continue
		}
		short := strings.TrimPrefix(argument, "-")
		var kept strings.Builder
		var keptValue string
		var hasKeptValue bool
		for offset, option := range short {
			if strings.ContainsRune(hgrepSilentShortOptions, option) {
				continue
			}
			if hasValue, ignored := hgrepWarnedShortOptions[option]; ignored {
				addWarning(fmt.Sprintf("-%c", option))
				if hasValue {
					if offset == len(short)-1 {
						if index+1 == len(arguments) {
							return normalized, warnings, false, fmt.Errorf("ripgrep option -%c requires a value", option)
						}
						index++
					}
					break
				}
				continue
			}
			if strings.ContainsRune(hgrepForbiddenShortOptions, option) {
				return normalized, warnings, false, fmt.Errorf("ripgrep option -%c is incompatible with verified-row output", option)
			}
			kept.WriteRune(option)
			if option == 'e' || option == 'f' {
				patternFromOption = true
			}
			if strings.ContainsRune(hgrepShortOptionsWithValue, option) {
				if offset == len(short)-1 {
					if index+1 == len(arguments) {
						return normalized, warnings, false, fmt.Errorf("ripgrep option -%c requires a value", option)
					}
					index++
					keptValue = arguments[index]
					hasKeptValue = true
				} else {
					kept.WriteString(short[offset+1:])
				}
				break
			}
		}
		if kept.Len() != 0 {
			normalized = append(normalized, "-"+kept.String())
			if hasKeptValue {
				normalized = append(normalized, keptValue)
			}
		}
	}
	if patternFromOption {
		return normalized, warnings, positionals == 0, nil
	}
	if positionals == 0 {
		return normalized, warnings, false, errors.New("ripgrep search requires a pattern")
	}
	return normalized, warnings, positionals == 1, nil
}

func executeHGrep(ctx context.Context, arguments []string) (string, error) {
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandCtx, "rg", append([]string{"--json", "--no-config"}, arguments...)...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("prepare rg stdout: %w", err)
	}
	var stderr limitedBuffer
	stderr.limit = maxHGrepStderrBytes
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("execute rg: %w", err)
	}

	reader := bufio.NewReader(stdout)
	seen := make(map[hgrepRow]struct{})
	var output strings.Builder
	truncated := false
	for {
		remainingOutput := maxHGrepOutputBytes - output.Len() - len(hgrepLimitMessage)
		rawEvent, tooLarge, err := readHGrepJSONEvent(reader, max(remainingOutput, maxHGrepControlEventBytes))
		if tooLarge {
			truncated = true
			cancel()
			break
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cancel()
			_ = command.Wait()
			if contextErr := ctx.Err(); contextErr != nil {
				return "", contextErr
			}
			return "", fmt.Errorf("read rg output: %w", err)
		}
		var event hgrepJSONEvent
		if err := json.Unmarshal(rawEvent, &event); err != nil {
			cancel()
			_ = command.Wait()
			return "", fmt.Errorf("decode rg output: %w", err)
		}
		if event.Type != "match" && event.Type != "context" {
			continue
		}
		path, err := decodeHGrepJSONText(event.Data.Path)
		if err != nil {
			cancel()
			_ = command.Wait()
			return "", fmt.Errorf("decode rg path: %w", err)
		}
		line, err := decodeHGrepJSONText(event.Data.Lines)
		if err != nil {
			cancel()
			_ = command.Wait()
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
		}
		if event.Data.LineNumber < 1 || strings.ContainsRune(line, '\n') {
			cancel()
			_ = command.Wait()
			return "", errors.New("rg returned a non-logical-line result")
		}
		key := hgrepRow{path: path, line: event.Data.LineNumber}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		quotedPath, err := json.Marshal(path)
		if err != nil {
			cancel()
			_ = command.Wait()
			return "", fmt.Errorf("encode rg path: %w", err)
		}
		row := string(quotedPath) + ":" + hpatch.FormatHashLineForHost(event.Data.LineNumber, line)
		if output.Len()+len(row)+len(hgrepLimitMessage) > maxHGrepOutputBytes {
			truncated = true
			cancel()
			break
		}
		output.WriteString(row)
	}
	waitErr := command.Wait()
	if truncated {
		output.WriteString(hgrepLimitMessage)
		return output.String(), nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return output.String(), nil
		}
		diagnostic := strings.TrimSpace(stderr.String())
		if diagnostic != "" {
			return "", errors.New(conciseRGDiagnostic(diagnostic))
		}
		return "", fmt.Errorf("execute rg: %w", waitErr)
	}
	return output.String(), nil
}

func readHGrepJSONEvent(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	var event []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(event)+len(fragment) > limit {
			return nil, true, nil
		}
		event = append(event, fragment...)
		switch {
		case err == nil:
			return event, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(event) != 0:
			return event, false, nil
		default:
			return nil, false, err
		}
	}
}

func conciseRGDiagnostic(diagnostic string) string {
	lines := strings.Split(diagnostic, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return diagnostic
}

func decodeHGrepJSONText(value hgrepJSONText) (string, error) {
	if value.Text != nil {
		if !utf8.ValidString(*value.Text) {
			return "", errors.New("result is not UTF-8")
		}
		return *value.Text, nil
	}
	if value.Bytes == "" {
		return "", errors.New("rg result has no text")
	}
	decoded, err := base64.StdEncoding.DecodeString(value.Bytes)
	if err != nil {
		return "", fmt.Errorf("decode base64 result: %w", err)
	}
	if !utf8.Valid(decoded) {
		return "", errors.New("result is not UTF-8")
	}
	return string(decoded), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	if remaining := b.limit - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(data[:min(len(data), remaining)])
	}
	return written, nil
}

func ensureHGrepSymlinkForExecutable(executable string) (string, error) {
	return ensureWorkerSymlinkForExecutable(executable, hgrepExecutableName)
}
