package hpatch

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestIndentationOnlyLineReplacementOffersExactCorrection(t *testing.T) {
	for _, test := range []struct {
		name            string
		typeCommand     string
		wantReplacement string
	}{
		{
			name:            "inline",
			typeCommand:     "type \"exit \\\"$status\\\"\\n\"",
			wantReplacement: "type \"\\texit \\\"$status\\\"\\n\"",
		},
		{
			name:            "heredoc",
			typeCommand:     "type <<BODY\nexit \"$status\"\nBODY",
			wantReplacement: "type <<BODY\n\texit \"$status\"\nBODY",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			content := strings.Repeat("echo ok\n", 264) + "\texit \"$status\"\n"
			writeTestFile(t, rootPath, "script.sh", content, 0o644)
			before := readTree(t, rootPath)

			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			script := "in script.sh\nrsel 265:265\n" + test.typeCommand + "\n"
			result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
			if err == nil {
				t.Fatal("indentation-only replacement unexpectedly succeeded")
			}
			if after := readTree(t, rootPath); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejection mutated workspace: before %#v, after %#v", before, after)
			}
			const proposed = "265|exit \"$status\"\n"
			if strings.Count(result.Diagnostic, proposed) != 1 {
				t.Fatalf("diagnostic contains proposed line %d times, want once:\n%s", strings.Count(result.Diagnostic, proposed), result.Diagnostic)
			}
			if !strings.Contains(result.Diagnostic, "indentation: proposed=\"\" correction=\"\\t\"\n") {
				t.Fatalf("diagnostic lacks concise indentation correction:\n%s", result.Diagnostic)
			}
			if strings.Contains(result.Diagnostic, "265|\texit \"$status\"") {
				t.Fatalf("diagnostic repeated corrected source content:\n%s", result.Diagnostic)
			}
			want := []CommandCorrection{{Command: 3, Replacement: test.wantReplacement}}
			if !reflect.DeepEqual(result.Corrections, want) {
				t.Fatalf("corrections = %#v, want %#v", result.Corrections, want)
			}
		})
	}
}

func TestAmbiguousIndentationReplacementOffersNoCorrection(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "script.sh", "\texit \"$status\"\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	script := "in script.sh\nrsel 1:1\ntype <<BODY\nexit \"$status\"\n  exit \"$status\"\nBODY\n"
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err != nil {
		t.Fatalf("ambiguous replacement should remain an ordinary edit: %v\n%s", err, result.Diagnostic)
	}
	if len(result.Corrections) != 0 {
		t.Fatalf("ambiguous replacement offered corrections: %#v", result.Corrections)
	}
}

func TestStructuralMultilineReplacementIsNotAnIndentationCorrection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "script.sh", "\twork()\n", 0o644)
	script := "in script.sh\nrsel 1:1\ntype <<BODY\nif ready {\nwork()\n}\nBODY\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "script.sh"), "if ready {\nwork()\n}\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestChangedGoFilesAreFormattedWithStandardLibrary(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.go", "package sample\nfunc main(){println(\"old\")}\n", 0o644)
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in main.go\ntsel 2 \"old\"\ntype \"new\"\n"), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	want := "package sample\n\nfunc main() { println(\"new\") }\n"
	if got := readTestFile(t, root, "main.go"); got != want {
		t.Fatalf("formatted Go = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "in main.go 3:27\n") ||
		!strings.Contains(stderr.String(), "last edit in main.go: type 1 tsel match: 3:24-3:27\n") ||
		!strings.Contains(stderr.String(), "3|func main() { println(\"new\") }\n") {
		t.Fatalf("report does not map formatted source coordinates:\n%s", stderr.String())
	}
}

func TestGoFormattingAllowsImportSorting(t *testing.T) {
	root := t.TempDir()
	content := "package sample\n\nimport (\n\t\"strings\"\n\t\"fmt\"\n)\n\nfunc value() string { return fmt.Sprint(strings.TrimSpace(\"old\")) }\n"
	writeTestFile(t, root, "main.go", content, 0o644)

	stdout, stderr, exitCode := runForTest(root, nil, "in main.go\ntsel 8 \"old\"\ntype \"new\"\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "package sample\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc value() string { return fmt.Sprint(strings.TrimSpace(\"new\")) }\n"
	if got := readTestFile(t, root, "main.go"); got != want {
		t.Fatalf("formatted Go = %q, want %q", got, want)
	}
}

func TestInvalidChangedGoRejectsAtomically(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.go", "package sample\n\nfunc main() { println(\"old\") }\n", 0o644)
	before := readTree(t, root)

	stdout, stderr, exitCode := runForTest(root, nil, "in main.go\ntsel 3 \"println(\\\"old\\\")\"\ntype \"if\"\n")
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "format Go source") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid Go mutation was not atomic: before %#v, after %#v", before, after)
	}
}

func TestInvalidGoMakesMultiFileTransactionAtomic(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "note.txt", "old\n", 0o644)
	writeTestFile(t, root, "main.go", "package sample\n\nfunc main() { println(\"old\") }\n", 0o644)
	before := readTree(t, root)

	script := "in note.txt\ntsel 1 \"old\"\ntype \"new\"\nin main.go\ntsel 3 \"println(\\\"old\\\")\"\ntype \"if\"\n"
	_, _, exitCode := runForTest(root, nil, script)
	if exitCode != 1 {
		t.Fatalf("Run() exit = %d, want rejection", exitCode)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("multi-file rejection mutated workspace: before %#v, after %#v", before, after)
	}
}

func TestGoValidationAttributesMoveCommand(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "not Go\n", 0o644)
	before := readTree(t, root)
	_, stderr, exitCode := runForTest(root, nil, "in source.txt\nmv main.go\n")
	for _, want := range []string{"command 2", "source line 2", "operation \"mv\"", "format Go source"} {
		if exitCode != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("Run() = exit %d, stderr %q; want %q", exitCode, stderr, want)
		}
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected move mutated workspace: before %#v, after %#v", before, after)
	}
}

func TestNonGoFilesReceiveNoLanguageValidation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.txt", "not valid in any language\n", 0o644)

	stdout, _, exitCode := runForTest(root, nil, "in main.txt\ntsel 1 \"valid\"\ntype \"parseable\"\n")
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q", exitCode, stdout)
	}
	if got, want := readTestFile(t, root, "main.txt"), "not parseable in any language\n"; got != want {
		t.Fatalf("non-Go content = %q, want %q", got, want)
	}
}
