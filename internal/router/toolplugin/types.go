package toolplugin

import "encoding/json"

type (
	Tool struct {
		PluginID      string          `json:"pluginId"`
		Module        string          `json:"module"`
		Index         int             `json:"index"`
		Specification json.RawMessage `json:"specification"`
		MaxInputBytes int             `json:"maxInputBytes"`
	}

	Plugin struct {
		ID     string `json:"id"`
		Module string `json:"module"`
		Tools  []Tool `json:"tools"`
	}

	Carrier struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Payload string `json:"payload"`
	}

	Translation struct {
		Rejected   bool     `json:"rejected"`
		Diagnostic string   `json:"diagnostic"`
		Arguments  []string `json:"arguments"`
		Carrier    Carrier  `json:"carrier"`
	}

	Execution struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	}

	Snapshot struct {
		Root           string
		NodeExecutable string
		Plugins        []Plugin
		Diagnostics    []string
	}
)
