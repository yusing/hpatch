package hpatch

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hpatch/internal/patchtest"
)

func TestRunNormalMultiFileWorkflow(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "alpha old\nkeep\n", 0o750)
	writeTestFile(t, root, "b.txt", "one\ntwo", 0o640)
	writeTestFile(t, root, "obsolete.txt", "remove me\n", 0o640)

	script := strings.Join([]string{
		"in a.txt",
		`tsel 1 -1 "old"`,
		`type "new"`,
		"in b.txt",
		"rsel 1:2",
		"dup",
		"in a.txt",
		`tsel 2 1 "keep"`,
		"del",
		"new draft.txt",
		`type "foo"`,
		`type " "`,
		`type "bar"`,
		"mv final.txt",
		"in obsolete.txt",
		"rm",
	}, "\n")

	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := map[string]string{
		"a.txt":     "alpha new\n\n",
		"b.txt":     "one\ntwo\none\ntwo",
		"final.txt": "foo bar",
	}
	if got := readTree(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v", got, want)
	}
	if mode := testFileMode(t, filepath.Join(root, "a.txt")); mode != 0o750 {
		t.Fatalf("a.txt mode = %o, want 750", mode)
	}
}

func TestTranslateMatchesNormalMode(t *testing.T) {
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
		`tsel 3 1 "old"`,
		`type "current"`,
		"mv current.go",
		"new note.txt",
		`type "hello"`,
		`type " world\n"`,
		"in obsolete.go",
		"rm",
	}, "\n")

	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if strings.Contains(stdout, "@@ -") {
		t.Fatalf("translation used unsupported numeric hunk header:\n%s", stdout)
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

func TestMoveOnlyTranslationUsesVerificationHunk(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "nonempty", content: "first\nsecond\n"},
		{name: "empty", content: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "old.txt", test.content, 0o644)
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in old.txt\nmv new.txt\n")
			if exitCode != 0 || stderr != "" {
				t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
			}
			got, err := patchtest.Apply(map[string]string{"old.txt": test.content}, stdout)
			if err != nil {
				t.Fatalf("applying move patch: %v\n%s", err, stdout)
			}
			want := map[string]string{"new.txt": test.content}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tree = %#v, want %#v", got, want)
			}
		})
	}
}

func TestNetFileActionsCollapseMovesAndCanceledCreation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "start.txt", "content\n", 0o644)
	script := strings.Join([]string{
		"in start.txt",
		"mv intermediate.txt",
		"mv final.txt",
		"new temporary.txt",
		`type "discarded"`,
		"rm",
	}, "\n")

	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "*** Update File: start.txt\n*** Move to: final.txt\n") {
		t.Fatalf("translation did not collapse moves:\n%s", stdout)
	}
	if strings.Contains(stdout, "intermediate.txt") || strings.Contains(stdout, "temporary.txt") {
		t.Fatalf("translation retained intermediate or canceled paths:\n%s", stdout)
	}
	got, err := patchtest.Apply(map[string]string{"start.txt": "content\n"}, stdout)
	if err != nil {
		t.Fatalf("applying translation: %v", err)
	}
	want := map[string]string{"final.txt": "content\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v", got, want)
	}
}

func TestUnicodeCRLFAndSequentialEditorState(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "text.txt", "αβγ\tbar bar\r\none\r\ntwo", 0o644)
	script := strings.Join([]string{
		"in text.txt",
		"sel 1 2:3",
		`type "XY"`,
		`type "!"`,
		`tsel 1 -1 "bar"`,
		"del",
		"rsel 2:3",
		"dup",
	}, "\n")

	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	want := "αXY!\tbar \r\none\r\ntwo\r\none\r\ntwo"
	if got := readTestFile(t, root, "text.txt"); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestTranslateNormalizesLineEndingsForApplyPatchDisplay(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "text.txt", "old\r\nkeep\r\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in text.txt\ntsel 1 1 \"old\"\ntype \"new\"\n")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if strings.Contains(stdout, "\r") {
		t.Fatalf("translation contains CR bytes: %q", stdout)
	}
	if !strings.Contains(stdout, "-old\n+new\n keep\n") {
		t.Fatalf("translation does not describe logical line edit:\n%s", stdout)
	}
	if got := readTestFile(t, root, "text.txt"); got != "old\r\nkeep\r\n" {
		t.Fatalf("translate mutated CRLF input: %q", got)
	}
}

func TestStandaloneCRLogicalLines(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "text.txt", "first\rsecond\rthird", 0o644)
	script := "in text.txt\ntsel 2 1 \"second\"\ntype \"middle\"\nrsel 2:3\ndup\n"
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	if got, want := readTestFile(t, root, "text.txt"), "first\rmiddle\rthird\rmiddle\rthird"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestTranslateDisambiguatesRepeatedBlocks(t *testing.T) {
	root := t.TempDir()
	content := "first\nrepeat\nvalue=old\nend\nmiddle\nrepeat\nvalue=old\nend\nlast\n"
	writeTestFile(t, root, "text.txt", content, 0o644)
	script := "in text.txt\ntsel 7 1 \"old\"\ntype \"new\"\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	got, err := patchtest.Apply(map[string]string{"text.txt": content}, stdout)
	if err != nil {
		t.Fatalf("applying translation: %v\n%s", err, stdout)
	}
	want := map[string]string{"text.txt": "first\nrepeat\nvalue=old\nend\nmiddle\nrepeat\nvalue=new\nend\nlast\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v\n%s", got, want, stdout)
	}
}

func TestEvaluationFailuresDoNotMutateOrEmitPatch(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "no active file", script: `type "x"`},
		{name: "future command", script: "in file.txt\nsplice 1:2"},
		{name: "malformed selection", script: "in file.txt\nsel 1 2"},
		{name: "number overflow", script: "in file.txt\nsel 999999999999999999999999999 1:1"},
		{name: "out of bounds", script: "in file.txt\nsel 1 20:21"},
		{name: "overlapping occurrence is not counted", script: "in file.txt\ntsel 1 2 \"aa\""},
		{name: "new collision", script: "new file.txt"},
		{name: "move collision", script: "in file.txt\nmv occupied.txt"},
		{name: "old path after move", script: "in file.txt\nmv moved.txt\nin file.txt"},
		{name: "use after remove", script: "in file.txt\nrm\ntype \"x\""},
		{name: "missing destination parent", script: "new missing/new.txt\ntype \"x\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "aaa\n", 0o644)
			writeTestFile(t, root, "occupied.txt", "occupied\n", 0o644)
			before := readTree(t, root)
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, test.script)
			if exitCode == 0 || stdout != "" || !strings.HasPrefix(stderr, "hpatch:") {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestInvalidUTF8IsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.txt"), []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in binary.txt\ntype \"x\"\n")
	if exitCode == 0 || stdout != "" || stderr == "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestSymlinkPathResolvesNormally(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "target.txt", "target\n", 0o644)
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in link.txt\ntsel 1 1 \"target\"\ntype \"updated\"\n")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "+updated") {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestAbsoluteAndNormalizedPaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "relative.txt", "old\n", 0o644)
	absoluteRoot := t.TempDir()
	writeTestFile(t, absoluteRoot, "absolute.txt", "old\n", 0o644)

	script := "in nested/../relative.txt\ntsel 1 1 \"old\"\ntype \"relative\"\nin " + filepath.Join(absoluteRoot, "absolute.txt") + "\ntsel 1 1 \"old\"\ntype \"absolute\"\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := readTestFile(t, root, "relative.txt"); got != "relative\n" {
		t.Fatalf("relative file = %q", got)
	}
	if got := readTestFile(t, absoluteRoot, "absolute.txt"); got != "absolute\n" {
		t.Fatalf("absolute file = %q", got)
	}
}

func TestUnchangedModes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "same\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, "in file.txt\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("normal no-op = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	stdout, stderr, exitCode = runForTest(root, []string{"translate"}, "in file.txt\n")
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "does not change") {
		t.Fatalf("translate no-op = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestCommitFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "a\n", 0o644)
	writeTestFile(t, root, "b.txt", "b\n", 0o644)
	changes := updateChanges()
	operations := &failingFileOperations{fileOperations: osFileOperations{}, failRename: map[int]error{4: errors.New("injected install failure")}}
	err := commitChanges(root, changes, operations)
	if err == nil || !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("commitChanges() error = %v", err)
	}
	want := map[string]string{"a.txt": "a\n", "b.txt": "b\n"}
	if got := readTree(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree after rollback = %#v, want %#v", got, want)
	}
}

func TestRollbackFailureIsReportedAndBackupPreserved(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "a\n", 0o644)
	writeTestFile(t, root, "b.txt", "b\n", 0o644)
	changes := updateChanges()
	operations := &failingFileOperations{
		fileOperations: osFileOperations{},
		failRename: map[int]error{
			4: errors.New("injected install failure"),
			5: errors.New("injected restore failure"),
		},
	}
	err := commitChanges(root, changes, operations)
	if err == nil || !strings.Contains(err.Error(), "rollback also failed") || !strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("commitChanges() error = %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(root, ".b.txt.hpatch-backup-*"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("preserved backups = %v, error %v", backups, globErr)
	}
	content, readErr := os.ReadFile(backups[0])
	if readErr != nil || string(content) != "b\n" {
		t.Fatalf("backup content = %q, error %v", content, readErr)
	}
}

func TestStagingCleanupFailureReportsRetainedArtifact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "a\n", 0o644)
	changes := updateChanges()[:1]
	operations := &failingFileOperations{
		fileOperations: osFileOperations{},
		failRemove: map[int]error{
			1: errors.New("injected reservation cleanup failure"),
			2: errors.New("injected staging cleanup failure"),
		},
	}
	err := commitChanges(root, changes, operations)
	if err == nil || !strings.Contains(err.Error(), "cleanup also failed") || !strings.Contains(err.Error(), ".a.txt.hpatch-backup-") {
		t.Fatalf("commitChanges() error = %v", err)
	}
	artifacts, globErr := filepath.Glob(filepath.Join(root, ".a.txt.hpatch-backup-*"))
	if globErr != nil || len(artifacts) != 1 {
		t.Fatalf("retained artifacts = %v, error %v", artifacts, globErr)
	}
}

type failingFileOperations struct {
	fileOperations
	renameCalls int
	failRename  map[int]error
	removeCalls int
	failRemove  map[int]error
}

func (f *failingFileOperations) Rename(oldPath, newPath string) error {
	f.renameCalls++
	if err := f.failRename[f.renameCalls]; err != nil {
		return err
	}
	return f.fileOperations.Rename(oldPath, newPath)
}

func (f *failingFileOperations) Remove(path string) error {
	f.removeCalls++
	if err := f.failRemove[f.removeCalls]; err != nil {
		return err
	}
	return f.fileOperations.Remove(path)
}

func updateChanges() []change {
	changes := make([]change, 0, 2)
	for _, entry := range []struct {
		path   string
		before string
		after  string
	}{
		{path: "a.txt", before: "a\n", after: "A\n"},
		{path: "b.txt", before: "b\n", after: "B\n"},
	} {
		changes = append(changes, change{
			kind:         changeUpdate,
			originalPath: entry.path,
			path:         entry.path,
			original:     entry.before,
			content:      entry.after,
			mode:         0o644,
		})
	}
	return changes
}

func runForTest(root string, args []string, script string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(args, strings.NewReader(script), &stdout, &stderr, root)
	return stdout.String(), stderr.String(), exitCode
}

func writeTestFile(t *testing.T, root, path, content string, mode fs.FileMode) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.WriteFile(fullPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fullPath, mode); err != nil {
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
	tree := make(map[string]string)
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
		tree[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func testFileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
