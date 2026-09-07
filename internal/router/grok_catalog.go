package router

import (
	"encoding/json"
	"errors"
)

// appendGrokModel preserves Codex's evolving model metadata schema by extending
// a native v2 entry instead of inventing an incomplete catalog representation.
func appendGrokModel(body []byte) ([]byte, error) {
	var catalog map[string]json.RawMessage
	if json.Unmarshal(body, &catalog) != nil || catalog == nil {
		return nil, errors.New("invalid Codex model catalog")
	}
	var models []map[string]json.RawMessage
	if json.Unmarshal(catalog["models"], &models) != nil {
		return nil, errors.New("Codex model catalog is missing models")
	}
	var template map[string]json.RawMessage
	for _, model := range models {
		if jsonString(model, "slug") == grokModel {
			return nil, errors.New("upstream model catalog already owns grok:grok-4.6")
		}
		if jsonString(model, "slug") == "gpt-5.6-sol" && jsonString(model, "multi_agent_version") == "v2" {
			template = model
		}
	}
	if template == nil {
		for _, model := range models {
			if jsonString(model, "multi_agent_version") == "v2" {
				template = model
				break
			}
		}
	}
	if template == nil {
		return nil, errors.New("Grok requires a Codex catalog with native v2 subagent support")
	}
	model := make(map[string]json.RawMessage, len(template))
	for key, value := range template {
		model[key] = value
	}
	for key, value := range map[string]any{
		"slug": grokModel, "display_name": grokModel, "description": "Grok 4.6 native subagent through Hpatch. Start with fork_turns=none.",
		"context_window": 500000, "max_context_window": 500000,
		"visibility": "list", "supported_in_api": true, "priority": 100, "default_reasoning_level": "high",
		"supported_reasoning_levels": []map[string]string{{"effort": "low", "description": "Low reasoning"}, {"effort": "medium", "description": "Medium reasoning"}, {"effort": "high", "description": "High reasoning"}, {"effort": "xhigh", "description": "Extra-high reasoning"}},
		"multi_agent_version":        "v2", "use_responses_lite": false, "supports_search_tool": false,
		"input_modalities": []string{"text", "image"}, "supports_image_detail_original": false,
		"additional_speed_tiers": []any{}, "service_tiers": []any{}, "upgrade": nil, "availability_nux": nil,
	} {
		model[key] = mustMarshalJSON(value)
	}
	// Do not inherit account-gated OpenAI scheduling or model-upgrade defaults.
	delete(model, "multi_agent_reasoning_effort")
	models = append(models, model)
	catalog["models"] = mustMarshalJSON(models)
	return json.Marshal(catalog)
}
