package router

import (
	"encoding/json"
	"errors"
)

// responsesToolCatalog owns the request-local decode of every Responses tool
// definition. Consumers keep their own validation policy while sharing this
// tree, because malformed provider-owned sections do not have one universal
// failure behavior.
type responsesToolCatalog struct {
	top             *responsesToolSection
	additional      []*responsesAdditionalTools
	inputItems      []json.RawMessage
	inputObjectsErr error
}

type responsesAdditionalTools struct {
	itemIndex int
	item      map[string]json.RawMessage
	tools     *responsesToolSection
}

type responsesToolSection struct {
	present  bool
	array    bool
	raw      json.RawMessage
	rawTools []json.RawMessage
	tools    []map[string]json.RawMessage
	nodes    []*responsesToolNode
	err      error
}

type responsesToolNode struct {
	definition map[string]json.RawMessage
	nested     *responsesToolSection
}

func decodeResponsesToolCatalog(fields map[string]json.RawMessage) *responsesToolCatalog {
	rawTop, topPresent := fields["tools"]
	catalog := &responsesToolCatalog{
		top: decodeResponsesToolSection(rawTop, topPresent),
	}

	rawInput, inputPresent := fields["input"]
	if !inputPresent {
		return catalog
	}
	if err := json.Unmarshal(rawInput, &catalog.inputItems); err != nil {
		catalog.inputObjectsErr = err
		return catalog
	}
	for itemIndex, rawItem := range catalog.inputItems {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &item); err != nil {
			catalog.inputObjectsErr = errors.Join(catalog.inputObjectsErr, err)
			continue
		}
		if jsonString(item, "type") != "additional_tools" {
			continue
		}
		rawTools, toolsPresent := item["tools"]
		catalog.additional = append(catalog.additional, &responsesAdditionalTools{
			itemIndex: itemIndex,
			item:      item,
			tools:     decodeResponsesToolSection(rawTools, toolsPresent),
		})
	}
	return catalog
}

func decodeResponsesToolSection(raw json.RawMessage, present bool) *responsesToolSection {
	section := &responsesToolSection{present: present, raw: raw}
	if !present {
		return section
	}
	var definitions []json.RawMessage
	if err := json.Unmarshal(raw, &definitions); err != nil {
		section.err = err
		return section
	}
	section.array = definitions != nil
	section.rawTools = definitions
	if definitions == nil {
		return section
	}
	section.tools = make([]map[string]json.RawMessage, len(definitions))
	section.nodes = make([]*responsesToolNode, len(definitions))
	for index, definition := range definitions {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(definition, &tool); err != nil {
			section.err = errors.Join(section.err, err)
			continue
		}
		section.tools[index] = tool
		if tool == nil {
			continue
		}
		node := &responsesToolNode{definition: tool}
		if nested, ok := tool["tools"]; ok {
			node.nested = decodeResponsesToolSection(nested, true)
		}
		section.nodes[index] = node
	}
	return section
}

func (c *responsesToolCatalog) appendTop(tools []map[string]json.RawMessage) {
	c.top.present = true
	c.top.array = true
	c.top.tools = append(c.top.tools, tools...)
	for _, tool := range tools {
		c.top.rawTools = append(c.top.rawTools, mustMarshalJSON(tool))
		node := &responsesToolNode{definition: tool}
		if nested, ok := tool["tools"]; ok {
			node.nested = decodeResponsesToolSection(nested, true)
		}
		c.top.nodes = append(c.top.nodes, node)
	}
}

func (c *responsesToolCatalog) removeTop(index int) {
	c.top.tools = append(c.top.tools[:index], c.top.tools[index+1:]...)
	c.top.rawTools = append(c.top.rawTools[:index], c.top.rawTools[index+1:]...)
	c.top.nodes = c.top.nodes[:0]
	for _, tool := range c.top.tools {
		if tool == nil {
			c.top.nodes = append(c.top.nodes, nil)
			continue
		}
		node := &responsesToolNode{definition: tool}
		if nested, ok := tool["tools"]; ok {
			node.nested = decodeResponsesToolSection(nested, true)
		}
		c.top.nodes = append(c.top.nodes, node)
	}
}

func (c *responsesToolCatalog) encodeTop(fields map[string]json.RawMessage) error {
	encoded, err := json.Marshal(c.top.tools)
	if err != nil {
		return err
	}
	fields["tools"] = encoded
	return nil
}

func (c *responsesToolCatalog) encodeAdditional(fields map[string]json.RawMessage, group *responsesAdditionalTools, section *responsesToolSection) error {
	if section != group.tools {
		for _, node := range group.tools.nodes {
			if node == nil || node.nested != section {
				continue
			}
			encoded, err := json.Marshal(section.tools)
			if err != nil {
				return err
			}
			node.definition["tools"] = encoded
			break
		}
	}
	encodedTools, err := json.Marshal(group.tools.tools)
	if err != nil {
		return err
	}
	group.item["tools"] = encodedTools
	encodedItem, err := json.Marshal(group.item)
	if err != nil {
		return err
	}
	c.inputItems[group.itemIndex] = encodedItem
	encodedInput, err := json.Marshal(c.inputItems)
	if err != nil {
		return err
	}
	fields["input"] = encodedInput
	return nil
}
