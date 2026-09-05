package shellruntime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPathMapsThreadToRuntime(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "hpatch-thread-1", ".runtime")
	got, err := Path(root, "thread-1")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPathRejectsInvalidThreadIDs(t *testing.T) {
	for _, threadID := range []string{"", ".", "..", "nested/thread", `nested\\thread`, "thread\x00suffix"} {
		t.Run(threadID, func(t *testing.T) {
			if got, err := Path(t.TempDir(), threadID); err == nil || got != "" {
				t.Fatalf("Path() = (%q, %v), want empty path and an error", got, err)
			}
		})
	}
}

func TestDirectoryRequiresAbsoluteConfiguredPath(t *testing.T) {
	t.Setenv(RuntimeDirectoryEnvironment, "relative")
	if _, err := Directory(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Directory() error = %v", err)
	}
}
