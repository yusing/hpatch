package hpatch

import (
	"bytes"
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
	if want := "8ed3: alpha\nf44e: beta\nbe9d: gamma\n"; whole != want {
		t.Fatalf("whole read = %q, want %q", whole, want)
	}

	detailed, err := ReadHashLinesForHost(t.Context(), workspace, `"path with spaces.txt" 2:3`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "f44e: beta\nbe9d: gamma\n"; detailed.Output != want {
		t.Fatalf("bounded read = %q, want %q", detailed.Output, want)
	}
	if want := "beta\r\ngamma"; detailed.CatOutput != want {
		t.Fatalf("cat baseline = %q, want %q", detailed.CatOutput, want)
	}

	clamped, err := ReadHashLinesForHost(t.Context(), workspace, `"path with spaces.txt" 2:999`)
	if err != nil {
		t.Fatal(err)
	}
	if clamped != detailed {
		t.Fatalf("end-past-EOF read = %#v, want %#v", clamped, detailed)
	}
}

func TestHashLinesIgnoreLeadingIndentation(t *testing.T) {
	wantHash := hashLine("foo")
	for _, content := range []string{"  foo", "\t\tfoo", " \t foo"} {
		if got := hashLine(content); got != wantHash {
			t.Errorf("hashLine(%q) = %q, want %q", content, got, wantHash)
		}
	}

	result, err := formatHashLineStream(t.Context(), strings.NewReader("\t\tfoo\n"), "fixture.txt", 0, 0, maxHReadOutputBytes)
	if err != nil {
		t.Fatal(err)
	}
	if want := wantHash + ": \t\tfoo\n"; result.Output != want {
		t.Fatalf("indented output = %q, want %q", result.Output, want)
	}

	line, lineNumber, err := resolveLineHash("\t\tfoo\n", wantHash)
	if err != nil {
		t.Fatal(err)
	}
	if content := lineContent("\t\tfoo\n", line); content != "\t\tfoo" || lineNumber != 1 {
		t.Fatalf("resolved line = %q at %d, want %q at 1", content, lineNumber, "\t\tfoo")
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
	if want := hashLine("target") + ": target\n"; result.Output != want {
		t.Fatalf("bounded range = %q, want %q", result.Output, want)
	}
	if result.CatOutput != "target\r\n" {
		t.Fatalf("cat baseline = %q, want %q", result.CatOutput, "target\r\n")
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

func TestHashlineSelectorsRequireUniqueBaselineIdentity(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		script      string
		want        string
		wantFailure string
	}{
		{
			name:    "unique hash selects its line",
			current: "inserted\ntop\ntarget\nbottom\n",
			script:  "in file.txt\nrsel 34a0 34a0\ntype \"TARGET\"\n",
			want:    "inserted\ntop\nTARGET\nbottom\n",
		},
		{
			name:        "missing hash rejects",
			current:     "top\nother\nbottom\n",
			script:      "in file.txt\nrsel 34a0 34a0\ntype \"WRONG\"\n",
			wantFailure: "does not identify any line",
		},
		{
			name:        "duplicate content hash rejects",
			current:     "target\ntop\ntarget\nbottom\n",
			script:      "in file.txt\nrsel 34a0 34a0\ntype \"WRONG\"\n",
			wantFailure: "ambiguous",
		},
		{
			name:        "indentation-only hash collision rejects",
			current:     "target\ntop\n\ttarget\nbottom\n",
			script:      "in file.txt\nrsel 34a0 34a0\ntype \"WRONG\"\n",
			wantFailure: "ambiguous",
		},
		{
			name:        "text selection stays at or after its anchor",
			current:     "match\nanchor\nlater\n",
			script:      "in file.txt\ntsel " + hashLine("anchor") + " \"match\"\ntype \"WRONG\"\n",
			wantFailure: "found 0 of 1 requested matches",
		},
		{
			name:        "truncated hash collision rejects",
			current:     "line-503\nother\nline-526\n",
			script:      "in file.txt\nrsel 51d2 51d2\ndel\n",
			wantFailure: "ambiguous",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			writeTestFile(t, rootPath, "file.txt", test.current, 0o644)
			before := readTestFile(t, rootPath, "file.txt")
			stdout, stderr, exitCode := runWithStateReport(rootPath, test.script)
			if test.wantFailure != "" {
				if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.wantFailure) {
					t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
				}
				if strings.Contains(test.wantFailure, "identif") || strings.Contains(test.wantFailure, "ambiguous") {
					if strings.Count(stderr, "\n") != 1 {
						t.Fatalf("missing or ambiguous hash chose repair context: %q", stderr)
					}
				}
				if got := readTestFile(t, rootPath, "file.txt"); got != before {
					t.Fatalf("rejected selector mutated file to %q", got)
				}
				return
			}
			if exitCode != 0 || stdout != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, rootPath, "file.txt"); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func runWithStateReport(root, script string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, "")
	return stdout.String(), stderr.String(), exitCode
}

func TestHashlineSelectorsKeepSameGenerationIdentity(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
	script := "in file.txt\n" +
		"tsel 8ed3 \"alpha\"\n" +
		"type \"alpha\\ninserted\"\n" +
		"tsel be9d \"gamma\"\n" +
		"type \"G\"\n"
	stdout, _, exitCode := runForTest(rootPath, nil, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q", exitCode, stdout)
	}
	if got, want := readTestFile(t, rootPath, "file.txt"), "alpha\ninserted\nbeta\nG\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}
