package router

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// Exchange a real entry and a replacement while callers cross the Lstat/open
// boundary. Only the original real directory may ever be returned successfully.
func raceShellEntry(t *testing.T, path, replacement string) func() {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			for _, move := range [][2]string{
				{path, path + "-parked"}, {replacement, path},
				{path, replacement}, {path + "-parked", path},
			} {
				if err := os.Rename(move[0], move[1]); err != nil {
					done <- err
					return
				}
				runtime.Gosched()
			}
		}
	}()
	return func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestShellStorageDirectorySwapCannotSelectSibling(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"thread", "sibling"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := os.Stat(filepath.Join(directory, "thread"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sibling", filepath.Join(directory, "replacement")); err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	stop := raceShellEntry(t, filepath.Join(directory, "thread"), filepath.Join(directory, "replacement"))
	defer stop()
	for range 2000 {
		root, err := openExistingShellDirectory(parent, "thread")
		if err != nil {
			continue
		}
		opened, err := root.Stat(".")
		_ = root.Close()
		if err != nil || !os.SameFile(identity, opened) {
			t.Fatalf("opened sibling through replaced directory: %v", err)
		}
	}
}

func TestRetainedShellOpenDoesNotBlockOnFIFOSwap(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) { testShellOpenFIFOSwap(t, kind) })
	}
}

func testShellOpenFIFOSwap(t *testing.T, kind string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "script")
	fifoPath := filepath.Join(directory, "fifo")
	var createErr error
	if kind == "directory" {
		createErr = os.Mkdir(path, 0o700)
	} else {
		createErr = os.WriteFile(path, []byte("script"), 0o600)
	}
	if createErr != nil {
		t.Fatal(createErr)
	}
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	fifo, err := os.OpenFile(fifoPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stopSwapping := raceShellEntry(t, path, fifoPath)
	defer stopSwapping()
	cancel := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2000 {
			select {
			case <-cancel:
				return
			default:
			}
			if kind == "directory" {
				directory, err := openExistingShellDirectory(root, "script")
				if err == nil {
					_ = directory.Close()
				}
			} else {
				file, err := openRegularShellFile(root, "script")
				if err == nil {
					_ = file.Close()
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(cancel)
		// Release a regressed blocking open through the pinned FIFO descriptor,
		// so a failure does not leave a test goroutine behind.
		writer, err := os.OpenFile(fmt.Sprintf("/dev/fd/%d", fifo.Fd()), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer writer.Close()
		}
		<-done
		t.Fatal("retained file open blocked after replacement by a FIFO")
	}
}
