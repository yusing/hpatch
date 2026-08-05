package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const (
	maxHReadOutputBytes    = 16 << 20
	maxHReadBatchItems     = 32
	hreadBufferBytes       = 32 << 10
	hreadBatchLimitMessage = "hread: batch output limit reached; retry remaining items in a narrower batch\n"
)

var (
	hreadRangePattern = regexp.MustCompile(`^ ([1-9][0-9]*):([1-9][0-9]*)$`)

	// ErrHReadResultTooLarge reports that hashline formatting would exceed the
	// bounded result size enforced by hread and its router integration.
	ErrHReadResultTooLarge = errors.New("hread result exceeds its configured bound")
)

// HashLineReadResult is the host-facing result of one hread call.
type HashLineReadResult struct {
	Output string
}

// ReadHashLines reads one grammar-constrained hread call through workspace and
// returns verified LINE:HASH logical rows for one or more file specifications.
func ReadHashLines(ctx context.Context, workspace Workspace, input string) (string, error) {
	result, err := ReadHashLinesForHost(ctx, workspace, input)
	return result.Output, err
}

func parseHReadSpec(input string) (path string, startLine, endLine int, err error) {
	path, trailing, err := hpatchsyntax.DecodeQuoted(input)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid hread path: %w", err)
	}
	if path == "" {
		return "", 0, 0, errors.New("hread path must not be empty")
	}
	if trailing == "" {
		return path, 0, 0, nil
	}
	match := hreadRangePattern.FindStringSubmatch(trailing)
	if match == nil {
		return "", 0, 0, errors.New(`hread input must be "PATH" or "PATH" START:END`)
	}
	startLine, err = strconv.Atoi(match[1])
	if err != nil {
		return "", 0, 0, errors.New("hread start line is out of range")
	}
	endLine, err = strconv.Atoi(match[2])
	if err != nil {
		return "", 0, 0, errors.New("hread end line is out of range")
	}
	if startLine > endLine {
		return "", 0, 0, errors.New("hread line range start exceeds end")
	}
	return path, startLine, endLine, nil
}

// ReadHashLinesForHost returns the formatted hread output for host integrations.
func ReadHashLinesForHost(ctx context.Context, workspace Workspace, input string) (HashLineReadResult, error) {
	return readHashLinesForHost(ctx, workspace, input, maxHReadOutputBytes)
}

func readHashLinesForHost(ctx context.Context, workspace Workspace, input string, maxOutputBytes int) (HashLineReadResult, error) {
	rawSpecs := strings.Split(input, "\n")
	if len(rawSpecs) > maxHReadBatchItems {
		return HashLineReadResult{}, fmt.Errorf("hread batch exceeds %d items", maxHReadBatchItems)
	}

	type readSpec struct {
		input              string
		path               string
		startLine, endLine int
		err                error
	}
	specs := make([]readSpec, len(rawSpecs))
	for index, raw := range rawSpecs {
		raw = strings.TrimSuffix(raw, "\r")
		if raw == "" {
			return HashLineReadResult{}, errors.New("hread batch contains an empty read specification")
		}
		path, startLine, endLine, err := parseHReadSpec(raw)
		specs[index] = readSpec{
			input:     raw,
			path:      path,
			startLine: startLine,
			endLine:   endLine,
			err:       err,
		}
	}
	if len(specs) == 1 && specs[0].err != nil {
		return HashLineReadResult{}, specs[0].err
	}

	filesystem, err := validateWorkspace(ctx, workspace)
	if err != nil {
		return HashLineReadResult{}, err
	}
	read := func(spec readSpec, limit int) (HashLineReadResult, error) {
		if spec.err != nil {
			return HashLineReadResult{}, spec.err
		}
		path, err := filesystem.resolvePath(spec.path)
		if err != nil {
			return HashLineReadResult{}, err
		}
		return filesystem.readHashLines(ctx, path, spec.startLine, spec.endLine, limit)
	}
	if len(specs) == 1 {
		return read(specs[0], maxOutputBytes)
	}

	dataLimit := max(0, maxOutputBytes-len(hreadBatchLimitMessage))
	var output strings.Builder
	appendBounded := func(text string) bool {
		if len(text) > dataLimit-output.Len() {
			return false
		}
		output.WriteString(text)
		return true
	}
	appendLimitMessage := func() {
		if len(hreadBatchLimitMessage) <= maxOutputBytes-output.Len() {
			output.WriteString(hreadBatchLimitMessage)
		}
	}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return HashLineReadResult{}, err
		}
		if !appendBounded(fmt.Sprintf("==> %s <==\n", spec.input)) {
			appendLimitMessage()
			break
		}
		result, err := read(spec, dataLimit-output.Len())
		if contextErr := ctx.Err(); contextErr != nil {
			return HashLineReadResult{}, contextErr
		}
		if errors.Is(err, ErrHReadResultTooLarge) {
			appendLimitMessage()
			break
		}
		if err != nil {
			if !appendBounded(fmt.Sprintf("hread: %s\n", err)) {
				appendLimitMessage()
				break
			}
			continue
		}
		output.WriteString(result.Output)
	}
	return HashLineReadResult{Output: output.String()}, nil
}

func (w filesystemWorkspace) openRegularFile(ctx context.Context, path string) (*os.File, fs.FileMode, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	file, err := w.root.Open(path)
	if err != nil {
		reason := reasonOther
		if errors.Is(err, fs.ErrNotExist) {
			reason = reasonFileMissing
		}
		return nil, 0, withReason(reason, fmt.Errorf("reading %s: %w", path, err))
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	return file, info.Mode(), nil
}

func (w filesystemWorkspace) readFile(ctx context.Context, path string) (loadedFile, error) {
	file, mode, err := w.openRegularFile(ctx, path)
	if err != nil {
		return loadedFile{}, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if !utf8.Valid(content) {
		return loadedFile{}, fmt.Errorf("%s is not UTF-8", path)
	}
	return loadedFile{content: string(content), mode: mode}, nil
}

func (w filesystemWorkspace) readHashLines(ctx context.Context, path string, startLine, endLine, maxOutputBytes int) (HashLineReadResult, error) {
	file, _, err := w.openRegularFile(ctx, path)
	if err != nil {
		return HashLineReadResult{}, err
	}
	defer file.Close()

	return formatHashLineStream(ctx, file, path, startLine, endLine, maxOutputBytes)
}

func formatHashLineStream(
	ctx context.Context,
	reader io.Reader,
	path string,
	startLine, endLine, maxOutputBytes int,
) (HashLineReadResult, error) {
	wholeFile := startLine == 0 && endLine == 0
	lineNumber := 1
	lineNumberWidth := 1
	lineOpen := false
	pendingCR := false

	var output strings.Builder
	var content strings.Builder
	selected := func() bool {
		return wholeFile || lineNumber >= startLine && lineNumber <= endLine
	}
	capacityError := func() error {
		return fmt.Errorf("%w of %d bytes", ErrHReadResultTooLarge, maxOutputBytes)
	}
	appendContent := func(character byte) error {
		lineOpen = true
		if !selected() {
			return nil
		}
		rowBytes := output.Len() + lineNumberWidth + 7 + content.Len() + 1
		if rowBytes > maxOutputBytes {
			return capacityError()
		}
		content.WriteByte(character)
		return nil
	}
	finishLine := func() error {
		if selected() {
			rowBytes := output.Len() + lineNumberWidth + 7 + content.Len()
			if rowBytes > maxOutputBytes {
				return capacityError()
			}
			lineContent := content.String()
			writeHashLine(&output, lineNumber, lineContent, lineContent)
		}
		content.Reset()
		lineNumber++
		lineNumberWidth = len(strconv.Itoa(lineNumber))
		lineOpen = false
		return nil
	}

	var encodedRune [utf8.UTFMax]byte
	encodedRuneBytes := 0
	validateByte := func(character byte) bool {
		encodedRune[encodedRuneBytes] = character
		encodedRuneBytes++
		encoded := encodedRune[:encodedRuneBytes]
		if !utf8.FullRune(encoded) {
			return true
		}
		decoded, size := utf8.DecodeRune(encoded)
		if decoded == utf8.RuneError && size == 1 {
			return false
		}
		encodedRuneBytes = 0
		return true
	}

	buffer := make([]byte, hreadBufferBytes)
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return HashLineReadResult{}, err
		}
		readBytes, readErr := reader.Read(buffer)
		if err := ctx.Err(); err != nil {
			return HashLineReadResult{}, err
		}
		if readBytes == 0 && readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return HashLineReadResult{}, fmt.Errorf("reading %s: %w", path, io.ErrNoProgress)
			}
			continue
		}
		emptyReads = 0

		for _, character := range buffer[:readBytes] {
			if !validateByte(character) {
				return HashLineReadResult{}, fmt.Errorf("%s is not UTF-8", path)
			}
			if pendingCR {
				pendingCR = false
				if character == '\n' {
					if err := finishLine(); err != nil {
						return HashLineReadResult{}, err
					}
					continue
				}
				if err := finishLine(); err != nil {
					return HashLineReadResult{}, err
				}
			}
			switch character {
			case '\r':
				lineOpen = true
				pendingCR = true
			case '\n':
				lineOpen = true
				if err := finishLine(); err != nil {
					return HashLineReadResult{}, err
				}
			default:
				if err := appendContent(character); err != nil {
					return HashLineReadResult{}, err
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return HashLineReadResult{}, fmt.Errorf("reading %s: %w", path, readErr)
			}
			break
		}
	}
	if encodedRuneBytes != 0 {
		return HashLineReadResult{}, fmt.Errorf("%s is not UTF-8", path)
	}
	if pendingCR {
		if err := finishLine(); err != nil {
			return HashLineReadResult{}, err
		}
	} else if lineOpen {
		if err := finishLine(); err != nil {
			return HashLineReadResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return HashLineReadResult{}, err
	}

	lineCount := lineNumber - 1
	if !wholeFile && (startLine < 1 || endLine < startLine || startLine > lineCount) {
		return HashLineReadResult{}, fmt.Errorf(
			"requested lines %d:%d are outside file with %d lines",
			startLine,
			endLine,
			lineCount,
		)
	}
	return HashLineReadResult{Output: output.String()}, nil
}
