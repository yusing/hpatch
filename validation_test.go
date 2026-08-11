package hpatch

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHPatch2IndentationOnlyReplacementRejectsWithoutSuggestion(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "script.sh", "header\n\texit \"$status\"\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	command := "type " + row(2, "\texit \"$status\"") + ` "exit \"$status\"\n"`
	script := "in script.sh\n" + command
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err == nil {
		t.Fatal("indentation-only replacement unexpectedly succeeded")
	}
	wantRejections := []HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", Target: "line",
		Reason: "edit-conflict", Path: "script.sh",
	}}
	if !reflect.DeepEqual(result.Rejections, wantRejections) {
		t.Fatalf("rejections = %#v, want %#v", result.Rejections, wantRejections)
	}
	if !strings.Contains(result.Diagnostic, "indentation-only change") {
		t.Fatalf("diagnostic = %q", result.Diagnostic)
	}
	if strings.Contains(result.Diagnostic, "1:"+hashLine(`exit "$status"`)) {
		t.Fatalf("diagnostic contains fabricated target row: %q", result.Diagnostic)
	}
	metrics := result.Invocation.value
	if metrics.Reasons[reasonEditConflict] != 1 ||
		metrics.CommandReasons[commandOperationIndex("type")][reasonEditConflict] != 1 {
		t.Fatalf("indentation correction metrics = %+v", metrics)
	}
}

func TestHostRejectionsPreserveGroupedCommandIdentityWithoutSourceText(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, "bad\nworse\n", t.TempDir())
	if err == nil {
		t.Fatal("invalid commands unexpectedly succeeded")
	}
	want := []HostRejection{
		{Command: 1, SourceLine: 1, Operation: "bad", Reason: "script-syntax"},
		{Command: 2, SourceLine: 2, Operation: "worse", Reason: "script-syntax"},
	}
	if !reflect.DeepEqual(result.Rejections, want) {
		t.Fatalf("rejections = %#v, want %#v", result.Rejections, want)
	}
}

func TestHPatch2InvalidGoAfterMoveUsesContentOrigin(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T, string)
		script string
	}{
		{
			name: "existing file",
			setup: func(t *testing.T, root string) {
				writeTestFile(t, root, "old.go", "package p\n\nvar value = 1\n", 0o644)
			},
			script: "in old.go\ntype " + row(3, "var value = 1") + ` "var ="` + "\nmv moved.go",
		},
		{
			name:  "new file initializer",
			setup: func(*testing.T, string) {},
			script: `new old.go
type "package p\n\nvar =\n"
mv moved.go`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.setup(t, root)
			stdout, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 1 || stdout != "" ||
				!strings.HasPrefix(stderr, "type: command") ||
				!strings.Contains(stderr, `path "moved.go"`) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
		})
	}
}

func TestHPatch2MoveOnlyGoValidationUsesMoveOrigin(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "not Go\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, "in source.txt\nmv moved.go")
	if exitCode != 1 || stdout != "" ||
		!strings.HasPrefix(stderr, "mv: command") ||
		!strings.Contains(stderr, `path "moved.go"`) {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestHPatch2ChangedGoFilesAreFormatted(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.go", "package p\n\nvar value=1\n", 0o644)
	script := "in file.go\ntype " + row(3, "var value=1") + ` "var value=2"`
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	if got, want := readTestFile(t, root, "file.go"), "package p\n\nvar value = 2\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestHPatch2InvalidGoRejectsAtomically(t *testing.T) {
	rootPath := t.TempDir()
	before := "package p\n\nvar value = 1\n"
	writeTestFile(t, rootPath, "file.go", before, 0o644)
	script := "in file.go\ntype " + row(3, "var value = 1") + ` "var ="`
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	root.Close()
	if err == nil {
		t.Fatal("invalid Go unexpectedly translated")
	}
	want := []HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", Target: "line",
		Reason: "language-syntax", Path: "file.go", GeneratedLine: 3, GeneratedColumn: 5,
	}}
	if !reflect.DeepEqual(result.Rejections, want) {
		t.Fatalf("rejections = %#v, want %#v", result.Rejections, want)
	}

	stdout, stderr, exitCode := runForTest(rootPath, nil, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "language-syntax") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, "generated Go near 3:5\n") || !strings.Contains(stderr, "> 3 | var =\n") {
		t.Fatalf("diagnostic lacks generated-source context: %q", stderr)
	}
	if got := readTestFile(t, rootPath, "file.go"); got != before {
		t.Fatalf("file = %q, want unchanged", got)
	}
}

func TestHPatch2InvalidGoAttributesCausativeMutation(t *testing.T) {
	rootPath := t.TempDir()
	before := "package p\n\nvar first = 1\nvar second = 2\n"
	writeTestFile(t, rootPath, "file.go", before, 0o644)
	script := strings.Join([]string{
		"in file.go",
		"type " + row(3, "var first = 1") + ` "var ="`,
		"type " + row(4, "var second = 2") + ` "var second = 3"`,
	}, "\n")

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	root.Close()
	if err == nil {
		t.Fatal("invalid Go unexpectedly translated")
	}
	want := []HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", Target: "line",
		Reason: "language-syntax", Path: "file.go", GeneratedLine: 3, GeneratedColumn: 5,
	}}
	if !reflect.DeepEqual(result.Rejections, want) {
		t.Fatalf("rejections = %#v, want %#v", result.Rejections, want)
	}
	if got := readTestFile(t, rootPath, "file.go"); got != before {
		t.Fatalf("file = %q, want unchanged", got)
	}
}

func TestHPatch2InvalidGoReportsMultilineValueRow(t *testing.T) {
	rootPath := t.TempDir()
	before := "package p\n\nvar value = 1\n"
	writeTestFile(t, rootPath, "file.go", before, 0o644)
	script := "in file.go\ntype " + row(3, "var value = 1") + " <<PATCH\n" +
		"var first = 1\n" +
		"var =\n" +
		"var third = 3\n" +
		"PATCH\n"

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	root.Close()
	if err == nil {
		t.Fatal("invalid Go unexpectedly translated")
	}
	want := []HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", Target: "line",
		Reason: "language-syntax", Path: "file.go", GeneratedLine: 4, GeneratedColumn: 5, ValueLine: 2,
	}}
	if !reflect.DeepEqual(result.Rejections, want) {
		t.Fatalf("rejections = %#v, want %#v", result.Rejections, want)
	}

	_, stderr, exitCode := runForTest(rootPath, nil, script)
	if exitCode != 1 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"command 2 multiline value near row 2\n",
		"  value row 1 | var first = 1\n",
		"> value row 2 | var =\n",
		"  value row 3 | var third = 3\n",
	} {
		if !strings.Contains(stderr, fragment) {
			t.Fatalf("diagnostic lacks %q:\n%s", fragment, stderr)
		}
	}
	if got := readTestFile(t, rootPath, "file.go"); got != before {
		t.Fatalf("file = %q, want unchanged", got)
	}
}

func TestHPatch2MultilineValueRowsUsePhysicalFraming(t *testing.T) {
	rootPath := t.TempDir()
	script := "new file.go\ntype <<PATCH\npackage p\rvar =\nPATCH\n"

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	root.Close()
	if err == nil {
		t.Fatal("invalid Go unexpectedly translated")
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Command != 2 || result.Rejections[0].ValueLine != 1 {
		t.Fatalf("rejections = %#v, want command 2 physical value row 1", result.Rejections)
	}

	_, stderr, exitCode := runForTest(rootPath, nil, script)
	if exitCode != 1 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"command 2 multiline value near row 1\n",
		"> value row 1 | package p\\rvar =\n",
	} {
		if !strings.Contains(stderr, fragment) {
			t.Fatalf("diagnostic lacks %q:\n%s", fragment, stderr)
		}
	}
	if strings.Contains(stderr, "value row 2 |") {
		t.Fatalf("diagnostic split an embedded carriage return into another value row:\n%s", stderr)
	}
	if _, err := os.Stat(rootPath + "/file.go"); !os.IsNotExist(err) {
		t.Fatalf("rejected translation created file.go: %v", err)
	}
}
