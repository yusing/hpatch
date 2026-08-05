package hpatch

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadHashLinesWholeFileAndRange(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "path with spaces.txt", "alpha\nbeta\r\ngamma", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	workspace := Workspace{Root: root, CWD: "."}

	whole, err := ReadHashLines(t.Context(), workspace, `"path with spaces.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1:8ed3 alpha\n2:f44e beta\n3:be9d gamma\n"; whole != want {
		t.Fatalf("whole read = %q, want %q", whole, want)
	}

	detailed, err := ReadHashLinesForHost(t.Context(), workspace, `"path with spaces.txt" 2:3`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2:f44e beta\n3:be9d gamma\n"; detailed.Output != want {
		t.Fatalf("bounded read = %q, want %q", detailed.Output, want)
	}

	clamped, err := ReadHashLinesForHost(t.Context(), workspace, `"path with spaces.txt" 2:999`)
	if err != nil {
		t.Fatal(err)
	}
	if clamped != detailed {
		t.Fatalf("end-past-EOF read = %#v, want %#v", clamped, detailed)
	}
}

func TestReadHashLinesBatchPreservesOrderAndItemErrors(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "alpha.txt", "first\nsecond\n", 0o644)
	writeTestFile(t, rootPath, "beta.txt", "third\nfourth\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	input := "\"alpha.txt\"\n\"missing.txt\"\n\"beta.txt\" 2:99\n\"beta.txt\" 2:1"
	result, err := ReadHashLinesForHost(t.Context(), Workspace{Root: root}, input)
	if err != nil {
		t.Fatal(err)
	}
	fragments := []string{
		"==> \"alpha.txt\" <==\n1:" + hashLine("first") + " first\n2:" + hashLine("second") + " second\n",
		"==> \"missing.txt\" <==\nhread: reading missing.txt:",
		"==> \"beta.txt\" 2:99 <==\n2:" + hashLine("fourth") + " fourth\n",
		"==> \"beta.txt\" 2:1 <==\nhread: hread line range start exceeds end\n",
	}
	offset := 0
	for _, fragment := range fragments {
		index := strings.Index(result.Output[offset:], fragment)
		if index < 0 {
			t.Fatalf("batch output lacks ordered fragment %q:\n%s", fragment, result.Output)
		}
		offset += index + len(fragment)
	}

	const batchLimit = 128
	limited, err := readHashLinesForHost(t.Context(), Workspace{Root: root}, input, batchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Output) > batchLimit ||
		!strings.Contains(limited.Output, `==> "alpha.txt" <==`) ||
		!strings.HasSuffix(limited.Output, hreadBatchLimitMessage) {
		t.Fatalf("bounded batch = %d bytes, %q", len(limited.Output), limited.Output)
	}

	tooMany := strings.TrimSuffix(strings.Repeat("\"alpha.txt\"\n", maxHReadBatchItems+1), "\n")
	if output, err := ReadHashLines(t.Context(), Workspace{Root: root}, tooMany); err == nil || output != "" {
		t.Fatalf("oversized batch = %q, %v", output, err)
	}
}

func TestHashLinesIncludeLeadingIndentation(t *testing.T) {
	plainHash := hashLine("foo")
	indentedHash := hashLine("\t\tfoo")
	if indentedHash == plainHash {
		t.Fatalf("indented hash %q equals plain hash", indentedHash)
	}

	result, err := formatHashLineStream(t.Context(), strings.NewReader("\t\tfoo\n"), "fixture.txt", 0, 0, maxHReadOutputBytes)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1:" + indentedHash + " \t\tfoo\n"; result.Output != want {
		t.Fatalf("indented output = %q, want %q", result.Output, want)
	}

	line, err := resolveRow("\t\tfoo\n", rowReference{line: 1, hash: indentedHash})
	if err != nil {
		t.Fatal(err)
	}
	if content := lineContent("\t\tfoo\n", line); content != "\t\tfoo" {
		t.Fatalf("resolved line = %q, want %q", content, "\t\tfoo")
	}
}

func TestReadHashLinesRejectsInvalidInputAndBounds(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "file.txt", "one\ntwo\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	workspace := Workspace{Root: root}

	for _, input := range []string{
		`file.txt`,
		`""`,
		`"file.txt" 0:1`,
		`"file.txt" 2:1`,
		`"file.txt" 3:3`,
		`"file.txt" 1:1 trailing`,
	} {
		t.Run(input, func(t *testing.T) {
			if output, err := ReadHashLines(t.Context(), workspace, input); err == nil || output != "" {
				t.Fatalf("ReadHashLines(%q) = %q, %v", input, output, err)
			}
		})
	}
}

func TestReadHashLinesHonorsWorkspaceBoundaryAndCancellation(t *testing.T) {
	rootPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	workspace := Workspace{Root: root}

	if output, err := ReadHashLines(t.Context(), workspace, `"`+outside+`"`); err == nil || output != "" {
		t.Fatalf("outside read = %q, %v", output, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if output, err := ReadHashLines(ctx, workspace, `"missing.txt"`); err == nil || output != "" {
		t.Fatalf("canceled read = %q, %v", output, err)
	}
}

func TestFormatHashLineStreamBoundsRangesAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "one long line", content: strings.Repeat("x", 64)},
		{name: "many short lines", content: strings.Repeat("x\n", 16)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := formatHashLineStream(t.Context(), strings.NewReader(test.content), "fixture.txt", 0, 0, 32)
			if !errors.Is(err, ErrHReadResultTooLarge) || result != (HashLineReadResult{}) {
				t.Fatalf("formatHashLineStream() = %#v, %v", result, err)
			}
		})
	}

	large := strings.Repeat("skip\n", 1000) + "target\r\n" + strings.Repeat("tail\n", 1000)
	result, err := formatHashLineStream(t.Context(), strings.NewReader(large), "fixture.txt", 1001, 1001, 64)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1001:" + hashLine("target") + " target\n"; result.Output != want {
		t.Fatalf("bounded range = %q, want %q", result.Output, want)
	}
	if _, err := formatHashLineStream(t.Context(), strings.NewReader("ok\n\xff"), "fixture.txt", 1, 1, 64); err == nil {
		t.Fatal("invalid UTF-8 after the selected range was accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancelingReader{content: strings.Repeat("content\n", 10_000), cancel: cancel}
	if result, err := formatHashLineStream(ctx, reader, "fixture.txt", 0, 0, maxHReadOutputBytes); !errors.Is(err, context.Canceled) || result != (HashLineReadResult{}) {
		t.Fatalf("canceled stream = %#v, %v", result, err)
	}
}

type cancelingReader struct {
	content string
	cancel  context.CancelFunc
	read    bool
}

func (r *cancelingReader) Read(output []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	readBytes := copy(output, r.content)
	r.cancel()
	return readBytes, nil
}

func TestVerifiedRowsDisambiguateDuplicateContent(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "file.txt", "same\nmiddle\nsame\n", 0o644)
	script := "in file.txt\ntype " + row(3, "same") + " \"changed\""
	stdout, _, exitCode := runForTest(rootPath, nil, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q", exitCode, stdout)
	}
	if got, want := readTestFile(t, rootPath, "file.txt"), "same\nmiddle\nchanged\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}
