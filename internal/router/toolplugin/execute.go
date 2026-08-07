package toolplugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// One execution output can expand to six times its UTF-8 size during JSON encoding.
// The compared response carries two independently bounded outputs.
const maxComparedExecutionHostOutputBytes = 2 * maxExecutionHostOutputBytes

func Execute(
	ctx context.Context,
	node, runtimeRoot, module string,
	index int,
	arguments []string,
	stdin *os.File,
) (Execution, error) {
	request := struct {
		Operation    string   `json:"operation"`
		SnapshotRoot string   `json:"snapshotRoot"`
		Module       string   `json:"module"`
		Index        int      `json:"index"`
		Arguments    []string `json:"arguments"`
		InputFD      bool     `json:"inputFD"`
	}{
		Operation:    "execute",
		SnapshotRoot: filepath.Join(runtimeRoot, snapshotDirectory),
		Module:       module,
		Index:        index,
		Arguments:    arguments,
		InputFD:      stdin != nil,
	}
	var result Execution
	var scriptFiles []*os.File
	if stdin != nil {
		scriptRead, scriptWrite, err := os.Pipe()
		if err != nil {
			return Execution{}, fmt.Errorf("create plugin script pipe: %w", err)
		}
		scriptFiles = []*os.File{scriptRead, scriptWrite}
		defer func() {
			_ = scriptRead.Close()
			_ = scriptWrite.Close()
		}()
	}
	err := invoke(
		ctx,
		node,
		filepath.Join(runtimeRoot, hostFilename),
		"",
		maxComparedExecutionHostOutputBytes,
		stdin,
		scriptFiles,
		request,
		&result,
	)
	return result, err
}
