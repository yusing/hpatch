package hpatch

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHPatch2IndentationOnlyReplacementOffersExactCorrection(t *testing.T) {
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
	wantCommand := "type " + row(2, "\texit \"$status\"") + ` "\texit \"$status\"\n"`
	want := []CommandCorrection{{Command: 2, Replacement: wantCommand}}
	if !reflect.DeepEqual(result.Corrections, want) {
		t.Fatalf("corrections = %#v, want %#v", result.Corrections, want)
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
				!strings.Contains(stderr, `operation "type"`) ||
				!strings.Contains(stderr, `path "moved.go"`) ||
				strings.Contains(stderr, `operation "mv"`) {
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
		!strings.Contains(stderr, `operation "mv"`) ||
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
	root := t.TempDir()
	before := "package p\n\nvar value = 1\n"
	writeTestFile(t, root, "file.go", before, 0o644)
	script := "in file.go\ntype " + row(3, "var value = 1") + ` "var ="`
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "language-syntax") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := readTestFile(t, root, "file.go"); got != before {
		t.Fatalf("file = %q, want unchanged", got)
	}
}
