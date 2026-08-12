package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type result struct {
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: configvalue CODEX_CONFIG")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read Codex config: %v\n", err)
		os.Exit(1)
	}

	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		fmt.Fprintf(os.Stderr, "parse Codex config: %v\n", err)
		os.Exit(1)
	}

	value, found := config["model_instructions_file"]
	if !found {
		writeResult(result{})
		return
	}
	path, ok := value.(string)
	if !ok {
		fmt.Fprintln(os.Stderr, "model_instructions_file must be a string")
		os.Exit(1)
	}
	writeResult(result{Found: true, Path: path})
}

func writeResult(value result) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "encode config result: %v\n", err)
		os.Exit(1)
	}
}

