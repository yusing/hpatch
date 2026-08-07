package toolplugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// JSON can encode each byte as a six-byte Unicode escape. The additional
// allowance covers the execution envelope.
const maxEncodedExecutionHostOutputBytes = 6*maxHostOutputBytes + 1<<20

func Execute(
	ctx context.Context,
	node, runtimeRoot, module string,
	index int,
	arguments []string,
	stdin *os.File,
) (ExecutionOutput, error) {
	request := struct {
		Operation         string   `json:"operation"`
		SnapshotRoot      string   `json:"snapshotRoot"`
		Module            string   `json:"module"`
		Index             int      `json:"index"`
		Arguments         []string `json:"arguments"`
		InputFD           bool     `json:"inputFD"`
		OutputBudgetBytes int      `json:"outputBudgetBytes"`
	}{
		Operation:         "execute",
		SnapshotRoot:      filepath.Join(runtimeRoot, snapshotDirectory),
		Module:            module,
		Index:             index,
		Arguments:         arguments,
		InputFD:           stdin != nil,
		OutputBudgetBytes: maxHostOutputBytes,
	}
	var result ExecutionOutput
	var scriptFiles []*os.File
	if stdin != nil {
		scriptRead, scriptWrite, err := os.Pipe()
		if err != nil {
			return ExecutionOutput{}, fmt.Errorf("create plugin script pipe: %w", err)
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
		maxEncodedExecutionHostOutputBytes,
		stdin,
		scriptFiles,
		request,
		&result,
	)
	return result, err
}
