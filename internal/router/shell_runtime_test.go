package router

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yusing/hpatch/internal/shellruntime"
)

func TestPreparedRequestStoresCurrentShellRuntime(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	runtimePath := shellruntime.Path(proxy.shellDirectory, "thread-1")
	target, err := os.Readlink(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if target != proxy.registry.shellRuntime {
		t.Fatalf("runtime target = %q, want %q", target, proxy.registry.shellRuntime)
	}
	if transform.shellDirectory != filepath.Dir(runtimePath) {
		t.Fatalf("shell directory = %q, want %q", transform.shellDirectory, filepath.Dir(runtimePath))
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(runtimePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("thread runtime survived proxy close: %v", err)
	}
}
