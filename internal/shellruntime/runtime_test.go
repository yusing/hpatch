package shellruntime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPathMapsThreadToRuntime(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "hpatch-thread-1", ".runtime")
	if got := Path(root, "thread-1"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestDirectoryRequiresAbsoluteConfiguredPath(t *testing.T) {
	t.Setenv(RuntimeDirectoryEnvironment, "relative")
	if _, err := Directory(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Directory() error = %v", err)
	}
}
