package router

// Source: routing_context.go:18:219 Codex metadata and usable workspace handling.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	codexTurnMetadataHeader   = "x-codex-turn-metadata"
	maxCodexTurnMetadataBytes = 32 << 10
)

type codexTurnMetadata struct {
	RequestKind string                     `json:"request_kind"`
	TurnID      string                     `json:"turn_id"`
	Workspaces  map[string]json.RawMessage `json:"workspaces"`
	Compaction  json.RawMessage            `json:"compaction"`
}

type routingWorkspace struct {
	canonical string
	root      *os.Root
}

func decodeCodexTurnMetadata(headers http.Header) (codexTurnMetadata, bool) {
	values := []string{}
	for name, headerValues := range headers {
		if !strings.EqualFold(name, codexTurnMetadataHeader) {
			continue
		}
		for _, value := range headerValues {
			if strings.TrimSpace(value) == "" {
				continue
			}
			if !slices.Contains(values, value) {
				values = append(values, value)
			}
		}
	}
	if len(values) != 1 || len(values[0]) > maxCodexTurnMetadataBytes || !isASCII(values[0]) {
		return codexTurnMetadata{}, false
	}

	trimmed := strings.TrimLeft(values[0], " \t\r\n")
	if !strings.HasPrefix(trimmed, "{") {
		return codexTurnMetadata{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(values[0]))
	var metadata codexTurnMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return codexTurnMetadata{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return codexTurnMetadata{}, false
	}
	return metadata, true
}

func isASCII(value string) bool {
	for index := range len(value) {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

func usableRoutingWorkspace(declared map[string]json.RawMessage) (routingWorkspace, bool) {
	var result routingWorkspace
	for path := range declared {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(canonical) {
			continue
		}
		root, err := os.OpenRoot(canonical)
		if err != nil {
			continue
		}
		rootInfo, err := root.Stat(".")
		if err != nil || !os.SameFile(info, rootInfo) {
			root.Close()
			continue
		}
		if result.root != nil {
			root.Close()
			if result.canonical == canonical {
				continue
			}
			result.close()
			return routingWorkspace{}, false
		}
		result = routingWorkspace{canonical: canonical, root: root}
	}
	return result, result.root != nil
}

func (workspace *routingWorkspace) unchanged() bool {
	pathInfo, err := os.Stat(workspace.canonical)
	if err != nil {
		return false
	}
	rootInfo, err := workspace.root.Stat(".")
	return err == nil && os.SameFile(pathInfo, rootInfo)
}

func (workspace *routingWorkspace) close() {
	if workspace.root == nil {
		return
	}
	workspace.root.Close()
	workspace.root = nil
}
