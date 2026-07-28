package router

// Source: routing_context.go:18:219 Codex metadata and usable workspace handling.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
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
	id        string
	declared  string
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

func usableRoutingWorkspaces(declared map[string]json.RawMessage) []routingWorkspace {
	keys := slices.Sorted(maps.Keys(declared))
	result := make([]routingWorkspace, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, path := range keys {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(canonical) || seen[canonical] {
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
		seen[canonical] = true
		result = append(result, routingWorkspace{
			id:        routingWorkspaceID(canonical),
			declared:  path,
			canonical: canonical,
			root:      root,
		})
	}
	return result
}

func routingWorkspaceID(canonical string) string {
	digest := sha256.Sum256([]byte(canonical))
	return "workspace-" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func (workspace routingWorkspace) unchanged() bool {
	pathInfo, err := os.Stat(workspace.canonical)
	if err != nil {
		return false
	}
	rootInfo, err := workspace.root.Stat(".")
	return err == nil && os.SameFile(pathInfo, rootInfo)
}

func closeRoutingWorkspaces(workspaces []routingWorkspace) {
	for _, workspace := range workspaces {
		workspace.root.Close()
	}
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
