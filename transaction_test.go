package hpatch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommitFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "a\n", 0o644)
	writeTestFile(t, root, "b.txt", "b\n", 0o644)
	operations := &failingFileOperations{
		fileOperations: testRootOperations(t, root),
		failRename:     map[int]error{4: errors.New("injected install failure")},
	}
	err := commitChanges(updateChanges(), operations)
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
	operations := &failingFileOperations{
		fileOperations: testRootOperations(t, root),
		failRename: map[int]error{
			4: errors.New("injected install failure"),
			5: errors.New("injected restore failure"),
		},
	}
	err := commitChanges(updateChanges(), operations)
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
	operations := &failingFileOperations{
		fileOperations: testRootOperations(t, root),
		failRemove: map[int]error{
			1: errors.New("injected reservation cleanup failure"),
			2: errors.New("injected staging cleanup failure"),
		},
	}
	err := commitChanges(updateChanges()[:1], operations)
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

func testRootOperations(t *testing.T, path string) rootFileOperations {
	t.Helper()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("closing root: %v", err)
		}
	})
	return rootFileOperations{root: root}
}
