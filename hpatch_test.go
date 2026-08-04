package hpatch

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yusing/hpatch/internal/patchtest"
)

func TestHPatch2NormalMultiFileWorkflow(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "alpha old\nkeep\nend\n", 0o750)
	if err := os.Chmod(filepath.Join(root, "a.txt"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "b.txt", "x x x\n", 0o640)
	writeTestFile(t, root, "obsolete.txt", "remove me\n", 0o640)

	script := strings.Join([]string{
		"in a.txt",
		"type " + row(1, "alpha old") + ` "old" "new"`,
		"type- " + row(2, "keep") + ` "// note\n"`,
		"type " + row(3, "end") + ` ""`,
		"in b.txt",
		"type " + row(1, "x x x") + ` "x" 2 "y"`,
		"new draft.txt",
		`type "foo bar"`,
		"mv final.txt",
		"in obsolete.txt",
		"rm",
	}, "\n")

	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, "files add=1 update=2 move=0 delete=1\n") {
		t.Fatalf("report = %q", stderr)
	}
	want := map[string]string{
		"a.txt":     "alpha new\n// note\nkeep\n",
		"b.txt":     "y y x\n",
		"final.txt": "foo bar",
	}
	if got := readTree(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v", got, want)
	}
	if mode := testFileMode(t, filepath.Join(root, "a.txt")); mode != 0o750 {
		t.Fatalf("a.txt mode = %o, want 750", mode)
	}
}

func TestHPatch2TranslateMatchesNormalMode(t *testing.T) {
	initial := map[string]string{
		"code.go":     "package sample\n\nvar value = old\n",
		"obsolete.go": "package obsolete\n",
	}
	root := t.TempDir()
	for path, content := range initial {
		writeTestFile(t, root, path, content, 0o644)
	}
	script := strings.Join([]string{
		"in code.go",
		"type " + row(3, "var value = old") + ` "old" "current"`,
		"mv current.go",
		"new note.txt",
		`type "hello world\n"`,
		"in obsolete.go",
		"rm",
	}, "\n")

	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if !strings.Contains(stderr, "files add=1 update=1 move=1 delete=1\n") {
		t.Fatalf("report = %q", stderr)
	}
	if got := readTree(t, root); !reflect.DeepEqual(got, initial) {
		t.Fatalf("translate mutated tree: %#v", got)
	}
	translated, err := patchtest.Apply(initial, stdout)
	if err != nil {
		t.Fatalf("applying translated patch: %v\n%s", err, stdout)
	}
	want := map[string]string{
		"current.go": "package sample\n\nvar value = current\n",
		"note.txt":   "hello world\n",
	}
	if !reflect.DeepEqual(translated, want) {
		t.Fatalf("translated tree = %#v, want %#v\n%s", translated, want, stdout)
	}
}

func TestHPatch2LineAndRangeTerminatorSemantics(t *testing.T) {
	tests := []struct {
		name, content, script, want string
	}{
		{"line preserves CRLF", "one\r\ntwo\r\n", "in file.txt\ntype " + row(1, "one") + ` "ONE"`, "ONE\r\ntwo\r\n"},
		{"range preserves final CR", "one\rtwo\rthree", "in file.txt\ntype " + row(1, "one") + ".." + row(2, "two") + ` "both"`, "both\rthree"},
		{"unterminated final replacement", "one\ntwo", "in file.txt\ntype " + row(2, "two") + ` "TWO"`, "one\nTWO"},
		{"empty line removes LF", "one\ntwo\n", "in file.txt\ntype " + row(1, "one") + ` ""`, "two\n"},
		{"empty line removes CRLF", "one\r\ntwo\r\n", "in file.txt\ntype " + row(1, "one") + ` ""`, "two\r\n"},
		{"empty line removes standalone CR", "one\rtwo\r", "in file.txt\ntype " + row(1, "one") + ` ""`, "two\r"},
		{"empty unterminated final line", "one\ntwo", "in file.txt\ntype " + row(2, "two") + ` ""`, "one\n"},
		{"empty range removes final terminator", "one\ntwo\nthree\n", "in file.txt\ntype " + row(1, "one") + ".." + row(2, "two") + ` ""`, "three\n"},
		{"empty heredoc removes line", "one\ntwo\n", "in file.txt\ntype " + row(1, "one") + " <<PATCH\nPATCH\n", "two\n"},
		{"empty text replacement keeps terminator", "one\ntwo\n", "in file.txt\ntype " + row(1, "one") + ` "one" ""`, "\ntwo\n"},
		{"explicit terminator creates blank line", "one\ntwo\n", "in file.txt\ntype " + row(1, "one") + ` "\n"`, "\ntwo\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.content, 0o644)
			_, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 0 {
				t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
			}
			if got := readTestFile(t, root, "file.txt"); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHPatch2EmptyInitializerRemainsEmptyFile(t *testing.T) {
	root := t.TempDir()
	_, stderr, exitCode := runForTest(root, nil, "new empty.txt\ntype \"\"\n")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "empty.txt"); got != "" {
		t.Fatalf("file = %q, want empty", got)
	}
}

func TestHPatch2SameBoundaryInsertionsKeepScriptOrder(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\n", 0o644)
	target := row(1, "target")
	script := strings.Join([]string{
		"in file.txt",
		`type- ` + target + ` "first\n"`,
		`type- ` + target + ` "second\n"`,
		`type+ ` + target + ` "after-one\n"`,
		`type+ ` + target + ` "after-two\n"`,
	}, "\n")
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	want := "first\nsecond\ntarget\nafter-one\nafter-two\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestHPatch2InsertionsAtReplacementBoundariesAreAllowed(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\n", 0o644)
	target := row(1, "target")
	script := strings.Join([]string{
		"in file.txt",
		`type ` + target + ` "replacement"`,
		`type- ` + target + ` "before\n"`,
		`type+ ` + target + ` "after\n"`,
	}, "\n")
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "before\nreplacement\nafter\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestHPatch2RejectsInvalidTargetsAtomically(t *testing.T) {
	tests := []struct{ name, script, reason string }{
		{"stale", "in file.txt\ntype 2:0000 \"B\"", "row-stale"},
		{"missing", "in file.txt\ntype 9:0000 \"B\"", "row-missing"},
		{"incomplete", "in file.txt\ntype " + row(1, "alpha") + ` "alpha" 2 "A"`, "occurrence-missing"},
		{"invalid count", "in file.txt\ntype " + row(1, "alpha") + ` "alpha" 0 "A"`, "invalid-count"},
		{"reversed", "in file.txt\ntype " + row(2, "beta") + ".." + row(1, "alpha") + ` ""`, "target-order"},
		{"overlap", "in file.txt\ntype " + row(1, "alpha") + ` "A"` + "\ntype " + row(1, "alpha") + ` ""`, "edit-conflict"},
		{"insertion inside replacement", "in file.txt\ntype " + row(1, "alpha") + ` "A"` + "\ntype- " + row(1, "alpha") + ` "ph" "x"`, "edit-conflict"},
		{"introduced", "in file.txt\ntype+ " + row(1, "alpha") + ` "new\n"` + "\ntype " + row(1, "alpha") + ` "new" "NEW"`, "occurrence-missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "alpha\nbeta\n", 0o644)
			before := readTestFile(t, root, "file.txt")
			stdout, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.reason) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.reason)
			}
			if got := readTestFile(t, root, "file.txt"); got != before {
				t.Fatalf("rejection changed file to %q", got)
			}
		})
	}
}

func TestHPatch2NewFileInitializerIsImmediate(t *testing.T) {
	tests := []struct{ name, script string }{
		{"existing", "in existing.txt\ntype \"new\""},
		{"intervening", "new new.txt\nin existing.txt\nin new.txt\ntype \"new\""},
		{"second", "new new.txt\ntype \"one\"\ntype \"two\""},
		{"target new", "new new.txt\ntype \"one\\n\"\ntype " + row(1, "one") + ` "ONE"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "existing.txt", "old\n", 0o644)
			_, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 1 || !strings.Contains(stderr, "initialization") {
				t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
			}
			if got := readTree(t, root); !reflect.DeepEqual(got, map[string]string{"existing.txt": "old\n"}) {
				t.Fatalf("rejection changed tree: %#v", got)
			}
		})
	}
}

func TestHPatch2RejectsRemovedGrammar(t *testing.T) {
	for _, command := range []string{`tsel 0000 "x"`, `rsel 0000 0000`, "copy", "cut", "paste", "commit", "del " + row(1, "x"), "type <<BODY\nx\nBODY"} {
		t.Run(strings.Fields(command)[0], func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "x\n", 0o644)
			_, stderr, exitCode := runForTest(root, nil, "in file.txt\n"+command)
			if exitCode != 1 || !strings.Contains(stderr, "category syntax") {
				t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
			}
		})
	}
}

func TestHPatch2FixedHeredocAndInlineInsertion(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\n", 0o644)
	script := "in file.txt\n" +
		"type- " + row(1, "target") + ` "// comment\n"` + "\n" +
		"type+ " + row(1, "target") + " <<PATCH\nmultiline\nvalue\nPATCH\n"
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	want := "// comment\ntarget\nmultiline\nvalue\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestHPatch2HeredocValueSupportsTextTarget(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "prefix needle suffix\n", 0o644)
	script := "in file.txt\ntype " + row(1, "prefix needle suffix") + " \"needle\" <<PATCH\n" +
		"multiline\nvalue\nPATCH\n"
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	want := "prefix multiline\nvalue\n suffix\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestHPatch2QuotedDoubleLessRemainsInlineText(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		initial string
		script  func(string) string
		want    string
	}{
		{
			name: "initializer",
			path: "file.txt",
			script: func(string) string {
				return `new file.txt` + "\n" + `type "a << b"`
			},
			want: "a << b",
		},
		{
			name: "initializer after escaped quote",
			path: "file.txt",
			script: func(string) string {
				return `new file.txt` + "\n" + `type "a \"<< b"`
			},
			want: `a "<< b`,
		},
		{
			name:    "replacement value",
			path:    "file.txt",
			initial: "old\n",
			script: func(string) string {
				return "in file.txt\ntype " + row(1, "old") + ` "a << b"`
			},
			want: "a << b\n",
		},
		{
			name:    "target literal",
			path:    "file.txt",
			initial: "a << b\n",
			script: func(string) string {
				return "in file.txt\ntype " + row(1, "a << b") + ` "a << b" "changed"`
			},
			want: "changed\n",
		},
		{
			name:    "path",
			path:    "a<<b.txt",
			initial: "old\n",
			script: func(string) string {
				return "in a<<b.txt\ntype " + row(1, "old") + ` "changed"`
			},
			want: "changed\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.initial != "" {
				writeTestFile(t, root, test.path, test.initial, 0o644)
			}
			_, stderr, exitCode := runForTest(root, nil, test.script(test.path))
			if exitCode != 0 {
				t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
			}
			if got := readTestFile(t, root, test.path); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHPatch2InvalidHeredocIsOneHeaderOwnedFailure(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "unchanged\n", 0o644)
	script := "in file.txt\ntype " + row(1, "unchanged") + " <<BODY\n" +
		"rm\nBODY\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 1 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if strings.Count(stderr, "hpatch: command") != 1 ||
		!strings.Contains(stderr, "command 2") ||
		!strings.Contains(stderr, "requires an unquoted <<PATCH") {
		t.Fatalf("diagnostic = %q", stderr)
	}
	if got := readTestFile(t, root, "file.txt"); got != "unchanged\n" {
		t.Fatalf("rejection changed file to %q", got)
	}
}

func TestHPatch2TargetLiteralRejectsC0ControlsExceptTab(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "a\tb\n", 0o644)
	valid := "in file.txt\ntype " + row(1, "a\tb") + ` "a\tb" "ok"`
	_, stderr, exitCode := runForTest(root, nil, valid)
	if exitCode != 0 {
		t.Fatalf("tab target = exit %d, stderr %q", exitCode, stderr)
	}

	writeTestFile(t, root, "file.txt", "a\x01b\n", 0o644)
	invalid := "in file.txt\ntype " + row(1, "a\x01b") + ` "a\u0001b" "bad"`
	_, stderr, exitCode = runForTest(root, nil, invalid)
	if exitCode != 1 || !strings.Contains(stderr, "forbidden control character") {
		t.Fatalf("control target = exit %d, stderr %q", exitCode, stderr)
	}
}

func row(line int, content string) string {
	return fmt.Sprintf("%d:%s", line, hashLine(content))
}

func runForTest(root string, args []string, script string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(args, strings.NewReader(script), &stdout, &stderr, root, "")
	return stdout.String(), stderr.String(), exitCode
}

func writeTestFile(t *testing.T, root, path, content string, mode fs.FileMode) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func testFileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
