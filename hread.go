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
	maxHReadOutputBytes = 16 << 20
	hreadBufferBytes    = 32 << 10
)

var (
	hreadRangePattern = regexp.MustCompile(`^ ([1-9][0-9]*):([1-9][0-9]*)$`)

	// ErrHReadResultTooLarge reports that hashline formatting would exceed the
	// bounded result size enforced by hread and its router integration.
	ErrHReadResultTooLarge = errors.New("hread result exceeds its configured bound")
)

// HashLineReadResult is the host-facing result of one hread call. Output is
// returned to the model; CatOutput is the exact equivalent raw file slice used
// to measure the input overhead introduced by hashline metadata.
type HashLineReadResult struct {
	Output    string
	CatOutput string
}

// ReadHashLines reads one grammar-constrained hread input through workspace and
// returns hash-prefixed logical lines, optionally selected by an absolute numeric
// input range. Output rows never include numeric line prefixes.
func ReadHashLines(ctx context.Context, workspace Workspace, input string) (string, error) {
	result, err := ReadHashLinesForHost(ctx, workspace, input)
	return result.Output, err
}

// ReadHashLinesForHost returns both hread output and its equivalent raw file
// slice so a router can account for the added model-input cost.
func ReadHashLinesForHost(ctx context.Context, workspace Workspace, input string) (HashLineReadResult, error) {
	path, trailing, err := hpatchsyntax.DecodeQuoted(input)
	if err != nil {
		return HashLineReadResult{}, fmt.Errorf("invalid hread path: %w", err)
	}
	if path == "" {
		return HashLineReadResult{}, errors.New("hread path must not be empty")
	}

	startLine, endLine := 0, 0
	if trailing != "" {
		match := hreadRangePattern.FindStringSubmatch(trailing)
		if match == nil {
			return HashLineReadResult{}, errors.New(`hread input must be "PATH" or "PATH" START:END`)
		}
		startLine, err = strconv.Atoi(match[1])
		if err != nil {
			return HashLineReadResult{}, errors.New("hread start line is out of range")
		}
		endLine, err = strconv.Atoi(match[2])
		if err != nil {
			return HashLineReadResult{}, errors.New("hread end line is out of range")
		}
		if startLine > endLine {
			return HashLineReadResult{}, errors.New("hread line range start exceeds end")
		}
	}

	filesystem, err := validateWorkspace(ctx, workspace)
	if err != nil {
		return HashLineReadResult{}, err
	}
	path, err = filesystem.resolvePath(path)
	if err != nil {
		return HashLineReadResult{}, err
	}
	return filesystem.readHashLines(ctx, path, startLine, endLine)
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

func (w filesystemWorkspace) readHashLines(ctx context.Context, path string, startLine, endLine int) (HashLineReadResult, error) {
	file, _, err := w.openRegularFile(ctx, path)
	if err != nil {
		return HashLineReadResult{}, err
	}
	defer file.Close()

	return formatHashLineStream(ctx, file, path, startLine, endLine, maxHReadOutputBytes)
}

func formatHashLineStream(
	ctx context.Context,
	reader io.Reader,
	path string,
	startLine, endLine, maxOutputBytes int,
) (HashLineReadResult, error) {
	wholeFile := startLine == 0 && endLine == 0
	lineNumber := 1
	lineOpen := false
	pendingCR := false

	var output strings.Builder
	var catOutput strings.Builder
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
		rowBytes := output.Len() + 7 + content.Len() + 1
		if rowBytes > maxOutputBytes {
			return capacityError()
		}
		content.WriteByte(character)
		return nil
	}
	finishLine := func(terminator string) error {
		if selected() {
			rowBytes := output.Len() + 7 + content.Len()
			if rowBytes > maxOutputBytes {
				return capacityError()
			}
			lineContent := content.String()
			writeHashLine(&output, lineContent, lineContent)
			catOutput.WriteString(lineContent)
			catOutput.WriteString(terminator)
		}
		content.Reset()
		lineNumber++
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
					if err := finishLine("\r\n"); err != nil {
						return HashLineReadResult{}, err
					}
					continue
				}
				if err := finishLine("\r"); err != nil {
					return HashLineReadResult{}, err
				}
			}
			switch character {
			case '\r':
				lineOpen = true
				pendingCR = true
			case '\n':
				lineOpen = true
				if err := finishLine("\n"); err != nil {
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
		if err := finishLine("\r"); err != nil {
			return HashLineReadResult{}, err
		}
	} else if lineOpen {
		if err := finishLine(""); err != nil {
			return HashLineReadResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return HashLineReadResult{}, err
	}

	lineCount := lineNumber - 1
	if !wholeFile && (startLine < 1 || endLine < startLine || endLine > lineCount) {
		return HashLineReadResult{}, fmt.Errorf(
			"requested lines %d:%d are outside file with %d lines",
			startLine,
			endLine,
			lineCount,
		)
	}
	return HashLineReadResult{Output: output.String(), CatOutput: catOutput.String()}, nil
}
