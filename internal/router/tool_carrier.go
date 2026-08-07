package router

import (
	"encoding/json"
	"errors"
	"fmt"
)

type codeModeCarrierKind string

const (
	codeModeCarrierCustom   codeModeCarrierKind = "custom"
	codeModeCarrierFunction codeModeCarrierKind = "function"
)

type codeModeCarrierCatalog map[string]codeModeCarrierKind

func buildCodeModeCarrierCatalog(fields map[string]json.RawMessage, registry *toolRegistry) (codeModeCarrierCatalog, error) {
	catalog := make(codeModeCarrierCatalog)
	add := func(tool map[string]json.RawMessage) error {
		name := jsonString(tool, "name")
		if name == "" {
			return nil
		}
		if _, registered := registry.contribution(name); registered {
			return fmt.Errorf("responses request already defines registered tool %s", name)
		}
		if name == applyPatchToolName {
			return nil
		}
		var kind codeModeCarrierKind
		switch jsonString(tool, "type") {
		case string(codeModeCarrierCustom):
			kind = codeModeCarrierCustom
		case string(codeModeCarrierFunction):
			kind = codeModeCarrierFunction
		default:
			return nil
		}
		if _, exists := catalog[name]; exists {
			return fmt.Errorf("code mode carrier %q is defined more than once", name)
		}
		catalog[name] = kind
		return nil
	}

	if rawTools, exists := fields["tools"]; exists {
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return nil, fmt.Errorf("decode Responses tools for carrier catalog: %w", err)
		}
		for _, tool := range tools {
			if err := add(tool); err != nil {
				return nil, err
			}
		}
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) == nil {
		for _, item := range items {
			if jsonString(item, "type") != "additional_tools" {
				continue
			}
			var additionalTools []map[string]json.RawMessage
			if err := json.Unmarshal(item["tools"], &additionalTools); err != nil {
				return nil, fmt.Errorf("decode additional tools for carrier catalog: %w", err)
			}
			for _, additionalTool := range additionalTools {
				if jsonString(additionalTool, "type") != "namespace" {
					if err := add(additionalTool); err != nil {
						return nil, err
					}
					continue
				}
				var tools []map[string]json.RawMessage
				if err := json.Unmarshal(additionalTool["tools"], &tools); err != nil {
					return nil, fmt.Errorf("decode namespaced tools for carrier catalog: %w", err)
				}
				for _, tool := range tools {
					if err := add(tool); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return catalog, nil
}

func (catalog codeModeCarrierCatalog) require(name string, kind codeModeCarrierKind) error {
	if name == "" {
		return errors.New("translator returned an empty carrier name")
	}
	available, ok := catalog[name]
	if !ok {
		return fmt.Errorf("Code Mode carrier %q is unavailable", name)
	}
	if available != kind {
		return fmt.Errorf("Code Mode carrier %q has kind %q, not %q", name, available, kind)
	}
	return nil
}

func carrierItemType(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "function_call"
	}
	return "custom_tool_call"
}

func carrierOutputItemType(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "function_call_output"
	}
	return "custom_tool_call_output"
}

func carrierPayloadField(kind codeModeCarrierKind) string {
	if kind == codeModeCarrierFunction {
		return "arguments"
	}
	return "input"
}

func renderCarrierItem(item map[string]json.RawMessage, kind codeModeCarrierKind, name, payload string) {
	item["type"] = mustMarshalJSON(carrierItemType(kind))
	item["name"] = mustMarshalJSON(name)
	delete(item, "input")
	delete(item, "arguments")
	item[carrierPayloadField(kind)] = mustMarshalJSON(payload)
}

func renderCarrierDoneEvent(payload []byte, kind codeModeCarrierKind, carrierPayload string) ([]byte, error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, errors.New("decode tool-call input completion event")
	}
	eventType := "response.custom_tool_call_input.done"
	if kind == codeModeCarrierFunction {
		eventType = "response.function_call_arguments.done"
	}
	event["type"] = mustMarshalJSON(eventType)
	delete(event, "input")
	delete(event, "arguments")
	event[carrierPayloadField(kind)] = mustMarshalJSON(carrierPayload)
	return json.Marshal(event)
}
