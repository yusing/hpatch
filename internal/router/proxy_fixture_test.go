package router

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yusing/hpatch/internal/shellruntime"
)

var proxyTestFixture struct {
	once      sync.Once
	registry  *toolRegistry
	directory string
	err       error
}

// Ordinary proxy tests borrow the real, immutable built-in catalog and its
// stateless shell translator. Each proxy still owns its session state and shell
// storage. Tests of configured plugins, registry mutation, startup, or shutdown
// build and close their own registries instead.
func sharedProxyTestRegistry(t *testing.T) *toolRegistry {
	t.Helper()
	proxyTestFixture.once.Do(func() {
		proxyTestFixture.directory, proxyTestFixture.err = os.MkdirTemp("", "hpatch-proxy-tests-")
		if proxyTestFixture.err != nil {
			return
		}
		t.Setenv(shellruntime.RuntimeDirectoryEnvironment, proxyTestFixture.directory)
		proxyTestFixture.registry, proxyTestFixture.err = buildToolRegistry(
			t.Context(), filepath.Join(proxyTestFixture.directory, "data"), testHPatchToolDescription, false,
		)
	})
	if proxyTestFixture.err != nil {
		t.Fatal(proxyTestFixture.err)
	}
	return proxyTestFixture.registry
}

func TestMain(m *testing.M) {
	code := m.Run()
	err := proxyTestFixture.registry.Close()
	if proxyTestFixture.directory != "" {
		err = errors.Join(err, os.RemoveAll(proxyTestFixture.directory))
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up proxy test fixture: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
