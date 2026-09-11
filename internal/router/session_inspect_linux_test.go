package router

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestSessionInspectionRejectsFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSessionInspection(t.Context(), path, nil); err == nil {
		t.Fatal("FIFO accepted")
	}
}
