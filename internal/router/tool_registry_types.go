package router

import (
	"encoding/json"
	"sync"

	"github.com/gofrs/flock"
	"github.com/yusing/hpatch"
)

type (
	toolContribution struct {
		PluginID      string          `json:"plugin_id"`
		Name          string          `json:"name"`
		Specification json.RawMessage `json:"specification"`
		Module        string          `json:"module,omitempty"`
		ModuleIndex   int             `json:"module_index,omitempty"`
		Builtin       bool            `json:"builtin"`
		ModelVisible  bool            `json:"model_visible"`
	}

	toolRegistry struct {
		SnapshotDir    string
		RuntimeRoot    string
		NodeExecutable string
		DiagnoseHooks  hpatch.DiagnoseHooks

		executable        string
		frontendDirectory string
		frontendLock      *flock.Flock
		runtimeDirectory  string
		shellRuntime      string
		ordered           []toolContribution
		byName            map[string]toolContribution
		wrappers          map[string]string
		frontends         map[string]string

		closeOnce sync.Once
		closeErr  error
	}

	toolWorkerManifest struct {
		Version        int                `json:"version"`
		RegistryID     string             `json:"registry_id"`
		NodeExecutable string             `json:"node_executable,omitempty"`
		RuntimeRoot    string             `json:"runtime_root"`
		Tools          []toolContribution `json:"tools"`
	}
)
