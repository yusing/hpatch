package toolplugin

import "encoding/json"

type (
	Tool struct {
		Specification json.RawMessage `json:"specification"`
	}

	Plugin struct {
		ID     string `json:"id"`
		Module string `json:"module"`
		Tools  []Tool `json:"tools"`
	}

	Carrier struct {
		Kind         string                     `json:"kind"`
		Name         string                     `json:"name"`
		Payload      string                     `json:"payload"`
		Template     string                     `json:"template"`
		Params       map[string]json.RawMessage `json:"params"`
		StockCommand string                     `json:"stockCommand"`
	}

	Translation struct {
		Rejected   bool     `json:"rejected"`
		Diagnostic string   `json:"diagnostic"`
		Arguments  []string `json:"arguments"`
		Carrier    Carrier  `json:"carrier"`
	}

	ExecutionOutput struct {
		Stdout   string           `json:"stdout"`
		Stderr   string           `json:"stderr"`
		ExitCode int              `json:"exitCode"`
		Stock    *ExecutionOutput `json:"stock,omitempty"`
	}

	Snapshot struct {
		Root           string
		NodeExecutable string
		Plugins        []Plugin
		Diagnostics    []string
	}
)
