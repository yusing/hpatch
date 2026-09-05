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
	runtimePath, err := shellruntime.Path(proxy.shellDirectory, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if target != proxy.registry.shellRuntime {
		t.Fatalf("runtime target = %q, want %q", target, proxy.registry.shellRuntime)
	}
	if transform.shellDirectory != filepath.Join(filepath.Dir(runtimePath), "scripts") {
		t.Fatalf("shell directory = %q, want scripts below %q", transform.shellDirectory, filepath.Dir(runtimePath))
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(runtimePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("thread runtime survived proxy close: %v", err)
	}
}

func TestShellRuntimeRejectsTraversalAndSymlinkDirectories(t *testing.T) {
	proxy, _ := newShellStorageTestProxy(t)
	for _, id := range []string{"", "../outside", "../../../outside", `/absolute`, `a\b`, "bad\x00id"} {
		if _, err := proxy.storeShellRuntime(id); err == nil {
			t.Fatalf("accepted thread ID %q", id)
		}
	}
	for _, component := range []string{"thread", "scripts"} {
		t.Run(component, func(t *testing.T) {
			outside := t.TempDir()
			sentinel := filepath.Join(outside, ".runtime")
			if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
				t.Fatal(err)
			}
			threadID := "symlink-" + component
			thread := filepath.Join(proxy.shellDirectory, "hpatch-"+threadID)
			link := thread
			if component == "scripts" {
				if err := os.Mkdir(thread, 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(thread, "scripts")
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if _, err := proxy.storeShellRuntime(threadID); err == nil {
				t.Fatal("accepted symlink storage directory")
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched" {
				t.Fatalf("outside launcher changed: %q, %v", got, err)
			}
		})
	}
}
