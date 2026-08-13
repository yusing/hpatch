package hpatch

import (
	"strconv"
	"strings"
	"testing"
)

func requireTreeSitterIndentation(t *testing.T) {
	t.Helper()
	if !treeSitterIndentationAvailable {
		t.Skip("Tree-sitter indentation support requires cgo")
	}
}

func TestPythonWrapperIndentationCorrection(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "def f():\n    if ready:\n        existing()\n    return\n", 0o644)
	command := "type " + row(3, "        existing()") + " " + quoteTestValue("        if ready:\n        existing()\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "def f():\n    if ready:\n        if ready:\n            existing()\n    return\n"
	if got := readTestFile(t, root, "file.py"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestPythonWrapperRightShiftAndCorrectChild(t *testing.T) {
	requireTreeSitterIndentation(t)
	for _, proposed := range []string{"                existing()", "            existing()"} {
		t.Run(strings.TrimSpace(proposed), func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.py", "def f():\n    if ready:\n        existing()\n    return\n", 0o644)
			command := "type " + row(3, "        existing()") + " " + quoteTestValue("        if ready:\n"+proposed+"\n")
			_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
			if exitCode != 0 {
				t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
			}
			want := "def f():\n    if ready:\n        if ready:\n            existing()\n    return\n"
			if got := readTestFile(t, root, "file.py"); got != want {
				t.Fatalf("content = %q, want %q", got, want)
			}
		})
	}
}

func TestJavaScriptWrapperIndentationCorrection(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.js", "function f() {\n  if (ready) {\n    existing();\n  }\n}\n", 0o644)
	command := "type " + row(3, "    existing();") + " " + quoteTestValue("    if (ready) {\n    existing();\n    }\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.js\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "function f() {\n  if (ready) {\n    if (ready) {\n      existing();\n    }\n  }\n}\n"
	if got := readTestFile(t, root, "file.js"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestTypeScriptWrapperIndentationCorrection(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.ts", "function f(): void {\n  if (ready) {\n    existing();\n  }\n}\n", 0o644)
	command := "type " + row(3, "    existing();") + " " + quoteTestValue("    if (ready) {\n    existing();\n    }\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.ts\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "function f(): void {\n  if (ready) {\n    if (ready) {\n      existing();\n    }\n  }\n}\n"
	if got := readTestFile(t, root, "file.ts"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestJavaScriptAndTypeScriptWrapperRightShiftAndCorrectChild(t *testing.T) {
	requireTreeSitterIndentation(t)
	tests := []struct {
		name        string
		path        string
		source      string
		replacement string
	}{
		{
			name:        "js right-shifted",
			path:        "file.js",
			source:      "function f() {\n  if (ready) {\n    existing();\n  }\n}\n",
			replacement: "    if (ready) {\n        existing();\n    }\n",
		},
		{
			name:        "js already-correct",
			path:        "file.js",
			source:      "function f() {\n  if (ready) {\n    existing();\n  }\n}\n",
			replacement: "    if (ready) {\n      existing();\n    }\n",
		},
		{
			name:        "ts right-shifted",
			path:        "file.ts",
			source:      "function f(): void {\n  if (ready) {\n    existing();\n  }\n}\n",
			replacement: "    if (ready) {\n        existing();\n    }\n",
		},
		{
			name:        "ts already-correct",
			path:        "file.ts",
			source:      "function f(): void {\n  if (ready) {\n    existing();\n  }\n}\n",
			replacement: "    if (ready) {\n      existing();\n    }\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, test.path, test.source, 0o644)
			command := "type " + row(3, "    existing();") + " " + quoteTestValue(test.replacement)
			_, stderr, exitCode := runForTest(root, nil, "in "+test.path+"\n"+command)
			if exitCode != 0 {
				t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
			}
			wantReplacement := "    if (ready) {\n      existing();\n    }\n"
			want := strings.Replace(test.source, "    existing();\n", wantReplacement, 1)
			if got := readTestFile(t, root, test.path); got != want {
				t.Fatalf("content = %q, want %q", got, want)
			}
		})
	}
}

func TestSupportedExactIndentationCorrection(t *testing.T) {
	for _, extension := range []string{"py", "js", "ts"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			path := "file." + extension
			writeTestFile(t, root, path, "header\n    value\n", 0o644)
			command := "type " + row(2, "    value") + " " + quoteTestValue("value\n")
			_, stderr, exitCode := runForTest(root, nil, "in "+path+"\n"+command)
			if exitCode != 0 {
				t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
			}
			if got := readTestFile(t, root, path); got != "header\n    value\n" {
				t.Fatalf("content = %q", got)
			}
		})
	}
}

func TestUnknownWrapperDoesNotReject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "    existing()\n", 0o644)
	command := "type " + row(1, "    existing()") + " " + quoteTestValue("    if (ready) {\n    existing()\n    }\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.txt\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "file.txt"); !strings.Contains(got, "if (ready)") {
		t.Fatalf("content = %q", got)
	}
}

func TestPreservedCommentIsNotWrapperAutofixed(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "def f():\n    if ready:\n        # comment\n", 0o644)
	replacement := "        if nested:\n        # comment\n"
	command := "type " + row(3, "        # comment") + " " + quoteTestValue(replacement)
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "def f():\n    if ready:\n" + replacement
	if got := readTestFile(t, root, "file.py"); got != want {
		t.Fatalf("content = %q, want submitted comment indentation", got)
	}
}

func quoteTestValue(value string) string {
	return strconv.Quote(value)
}

func TestIndentationCandidatesApplyAfterMoveIntoSupportedExtension(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "header\n    value\n", 0o644)
	command := "type " + row(2, "    value") + " " + quoteTestValue("value\n")
	_, stderr, exitCode := runForTest(root, nil, "in source.txt\n"+command+"\nmv moved.py")
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "moved.py"); got != "header\n    value\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestIndentationCandidatesRejectAfterMoveOutOfSupportedExtension(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.py", "header\n    value\n", 0o644)
	command := "type " + row(2, "    value") + " " + quoteTestValue("value\n")
	_, stderr, exitCode := runForTest(root, nil, "in source.py\n"+command+"\nmv moved.txt")
	if exitCode == 0 || !strings.Contains(stderr, "indentation-only change to preserved text") {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "source.py"); got != "header\n    value\n" {
		t.Fatalf("source changed = %q", got)
	}
}

func TestMultipleSupportedExactCandidatesApply(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "header\n    first\nmiddle\n    second\n", 0o644)
	first := "type " + row(2, "    first") + " " + quoteTestValue("first\n")
	second := "type " + row(4, "    second") + " " + quoteTestValue("second\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+first+"\n"+second)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "file.py"); got != "header\n    first\nmiddle\n    second\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestWrapperWithoutUnambiguousUnitRemainsByteExact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "value()\n", 0o644)
	command := "type " + row(1, "value()") + " " + quoteTestValue("if ready:\nvalue()\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "if ready:\nvalue()\n"
	if got := readTestFile(t, root, "file.py"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestPythonWrapperShapeRejectionsRemainSubmitted(t *testing.T) {
	tests := []string{
		"        if ready: pass\n            value()\n",
		"        if ready: # comment\n            value()\n",
		"        if ready:\n            other()\n",
	}
	for _, replacement := range tests {
		t.Run(strings.TrimSpace(replacement), func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.py", "def f():\n    if ready:\n        value()\n", 0o644)
			command := "type " + row(3, "        value()") + " " + quoteTestValue(replacement)
			_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
			if exitCode != 0 {
				t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
			}
			if got := readTestFile(t, root, "file.py"); got != "def f():\n    if ready:\n"+replacement {
				t.Fatalf("content = %q, want %q", got, replacement)
			}
		})
	}
}

func TestWrapperUnrelatedParseErrorIsRejectedAtomically(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	before := "def f():\n    if ready:\n        value()\n    broken = (\n"
	writeTestFile(t, root, "file.py", before, 0o644)
	replacement := "        if ready:\n        value()\n"
	command := "type " + row(3, "        value()") + " " + quoteTestValue(replacement)
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 1 || !strings.Contains(stderr, "language-syntax") ||
		!strings.Contains(stderr, "parse Python source") {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "file.py"); got != before {
		t.Fatalf("content = %q, want unchanged", got)
	}
}

func TestMixedStructuralUnitsRemainByteExact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.js", "function f() {\n  if (a) {\n    first();\n  }\n    if (b) {\n       second();\n    }\n}\n", 0o644)
	replacement := "    if (ready) {\n    first();\n    }\n"
	command := "type " + row(3, "    first();") + " " + quoteTestValue(replacement)
	_, stderr, exitCode := runForTest(root, nil, "in file.js\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "function f() {\n  if (a) {\n" + replacement + "  }\n    if (b) {\n       second();\n    }\n}\n"
	if got := readTestFile(t, root, "file.js"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestPythonWrapperTabIndentationUnit(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "def f():\n\tif ready:\n\t\texisting()\n", 0o644)
	replacement := "\t\tif ready:\n\texisting()\n"
	command := "type " + row(3, "\t\texisting()") + " " + quoteTestValue(replacement)
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "def f():\n\tif ready:\n\t\tif ready:\n\t\t\texisting()\n"
	if got := readTestFile(t, root, "file.py"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWrapperCorrectionFinalReferencesUseCorrectedContent(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "def f():\n    if ready:\n        existing()\n    return\n", 0o644)
	replacement := "        if ready:\n        existing()\n"
	command := "type " + row(3, "        existing()") + " " + quoteTestValue(replacement)
	_, report, exitCode := runForTest(root, nil, "in file.py\n"+command)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, report %q", exitCode, report)
	}
	if !strings.Contains(report, "refs 2 type file.py\n") ||
		!strings.Contains(report, "3:"+hashLine("        if ready:")+` \x20\x20\x20\x20\x20\x20\x20\x20if ready:`+"\n") {
		t.Fatalf("report = %q", report)
	}
}

func TestMultipleWrapperCandidatesUseOneFinalProbe(t *testing.T) {
	requireTreeSitterIndentation(t)
	root := t.TempDir()
	writeTestFile(t, root, "file.py", "def f():\n    first()\n    second()\n", 0o644)
	first := "type " + row(2, "    first()") + " " +
		quoteTestValue("    if first_ready:\n    first()\n")
	second := "type " + row(3, "    second()") + " " +
		quoteTestValue("    if second_ready:\n        second()\n")
	_, stderr, exitCode := runForTest(root, nil, "in file.py\n"+first+"\n"+second)
	if exitCode != 0 {
		t.Fatalf("Run() = %d, stderr %q", exitCode, stderr)
	}
	want := "def f():\n" +
		"    if first_ready:\n" +
		"        first()\n" +
		"    if second_ready:\n" +
		"        second()\n"
	if got := readTestFile(t, root, "file.py"); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}
