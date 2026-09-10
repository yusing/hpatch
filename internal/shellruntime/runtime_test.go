package shellruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathMapsThreadToRuntime(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "mekugi-runtime-thread-1")
	got, err := Path(root, "thread-1")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestCurrentPathPrefersCurrentLocatorThenLegacy(t *testing.T) {
	root := t.TempDir()
	current, err := Path(root, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, "hpatch-runtime-thread-1")
	got, err := CurrentPath(root, "thread-1")
	if err != nil || got != current {
		t.Fatalf("missing locators = %q, %v, want canonical %q", got, err, current)
	}
	if err := os.WriteFile(legacy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = CurrentPath(root, "thread-1")
	if err != nil || got != legacy {
		t.Fatalf("legacy locator = %q, %v, want %q", got, err, legacy)
	}
	if err := os.WriteFile(current, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = CurrentPath(root, "thread-1")
	if err != nil || got != current {
		t.Fatalf("current locator = %q, %v, want %q", got, err, current)
	}
}

func TestPathRejectsInvalidThreadIDs(t *testing.T) {
	for _, threadID := range []string{"", ".", "..", "nested/thread", `nested\\thread`, "thread\x00suffix"} {
		t.Run(threadID, func(t *testing.T) {
			for _, path := range []func(string, string) (string, error){Path, ScriptsPath} {
				if got, err := path(t.TempDir(), threadID); err == nil || got != "" {
					t.Fatalf("path = (%q, %v), want empty path and an error", got, err)
				}
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

func TestScriptsPathIsSeparateFromRuntimeLocator(t *testing.T) {
	root := t.TempDir()
	got, err := ScriptsPath(root, "thread-1")
	if err != nil || got != filepath.Join(root, "mekugi-scripts-thread-1") {
		t.Fatalf("ScriptsPath = %q, %v", got, err)
	}
}
