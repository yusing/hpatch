package toolplugin

import (
	"context"
	"path/filepath"
)

func Execute(ctx context.Context, node, runtimeRoot, module string, index int, arguments []string) (Execution, error) {
	request := struct {
		Operation    string   `json:"operation"`
		SnapshotRoot string   `json:"snapshotRoot"`
		Module       string   `json:"module"`
		Index        int      `json:"index"`
		Arguments    []string `json:"arguments"`
	}{
		Operation:    "execute",
		SnapshotRoot: filepath.Join(runtimeRoot, snapshotDirectory),
		Module:       module,
		Index:        index,
		Arguments:    arguments,
	}
	var result Execution
	err := invoke(ctx, node, filepath.Join(runtimeRoot, hostFilename), "", request, &result)
	return result, err
}
