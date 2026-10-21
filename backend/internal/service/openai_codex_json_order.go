package service

import (
	"bytes"
	"encoding/json"
	"sort"
)

// codexJSONOrder selects the object-key order used when re-encoding a native
// Codex request. encoding/json sorts map keys, while codex-rs serializes serde
// structs in declaration order. The order below mirrors the current Codex
// wire structs so a rewritten request does not advertise that it passed
// through a Go map.
type codexJSONOrder int

const (
	codexJSONOrderGeneric codexJSONOrder = iota
	codexJSONOrderRoot
	codexJSONOrderClientMetadata
	codexJSONOrderTurnMetadata
	codexJSONOrderWorkspaceSet
	codexJSONOrderWorkspace
	codexJSONOrderInputItem
	codexJSONOrderContent
	codexJSONOrderTool
	codexJSONOrderReasoning
	codexJSONOrderText
	codexJSONOrderTextFormat
	codexJSONOrderAction
)

var codexRootKeyOrder = []string{
	"model",
	"stream",
	"service_tier",
	"previous_response_id",
	"input",
	"tools",
	"tool_choice",
	"parallel_tool_calls",
	"reasoning",
	"store",
	"stream_options",
	"include",
	"prompt_cache_key",
	"text",
	"generate",
	"client_metadata",
	"access_programs",
}

// codexClientMetadataKeyOrder follows ResponsesMetadata::client_metadata's
// insertion order in codex-rs. The upstream type is a HashMap, so any order is
// valid; this one is deliberately not alphabetical.
var codexClientMetadataKeyOrder = []string{
	"x-codex-installation-id",
	"session_id",
	"thread_id",
	"x-codex-window-id",
	"turn_id",
	"x-openai-subagent",
	"x-codex-parent-thread-id",
	"parent_turn_id",
	"root_turn_id",
	"x-codex-turn-metadata",
	"mcp_attribution",
}

// codexTurnMetadataKeyOrder follows CodexTurnMetadataPayload's declaration
// order. Unknown keys are serialized after it in sorted order, matching the
// BTreeMap used by serde's flatten for app/desktop extras.
var codexTurnMetadataKeyOrder = []string{
	"installation_id",
	"session_id",
	"thread_id",
	"agent_name",
	"turn_id",
	"window_id",
	"window_number",
	"context_window_id",
	"request_kind",
	"history_initialization",
	"forked_from_thread_id",
	"forked_from_ordinal_exclusive",
	"parent_thread_id",
	"parent_turn_id",
	"root_turn_id",
	"subagent_kind",
	"thread_source",
	"turn_trigger",
	"sandbox",
	"sandbox_mode",
	"auto_review_enabled",
	"node_repl_auto_review_required",
	"node_repl_disabled",
	"workspaces",
	"tool_namespaces_info",
	"turn_started_at_unix_ms",
	"history_ingest_requested",
	"analytics_enabled",
	"compaction",
	"mcp_attribution",
}

var codexResponseItemKeyOrder = map[string][]string{
	"message": {
		"type", "encrypted_content", "status", "id", "role", "content", "phase",
		"internal_chat_message_metadata_passthrough",
	},
	"agent_message": {
		"type", "id", "author", "recipient", "content",
		"internal_chat_message_metadata_passthrough",
	},
	"reasoning": {
		"type", "id", "summary", "content", "encrypted_content",
		"internal_chat_message_metadata_passthrough",
	},
	"local_shell_call": {
		"type", "encrypted_content", "id", "call_id", "status", "action",
		"internal_chat_message_metadata_passthrough",
	},
	"function_call": {
		"type", "encrypted_content", "status", "id", "name", "namespace", "arguments",
		"encrypted_function_args", "call_id", "internal_chat_message_metadata_passthrough",
	},
	"tool_search_call": {
		"type", "encrypted_content", "id", "call_id", "status", "execution", "arguments",
		"internal_chat_message_metadata_passthrough",
	},
	"function_call_output": {
		"type", "id", "call_id", "name", "namespace", "output",
		"internal_chat_message_metadata_passthrough",
	},
	"custom_tool_call": {
		"type", "encrypted_content", "id", "status", "call_id", "name", "namespace", "input",
		"internal_chat_message_metadata_passthrough",
	},
	"custom_tool_call_output": {
		"type", "id", "call_id", "name", "output",
		"internal_chat_message_metadata_passthrough",
	},
	"tool_search_output": {
		"type", "id", "call_id", "status", "execution", "tools",
		"internal_chat_message_metadata_passthrough",
	},
	"web_search_call": {
		"type", "encrypted_content", "id", "status", "action",
		"internal_chat_message_metadata_passthrough",
	},
	"image_generation_call": {
		"type", "encrypted_content", "id", "status", "revised_prompt", "result",
		"internal_chat_message_metadata_passthrough",
	},
	"compaction": {
		"type", "id", "encrypted_content", "internal_chat_message_metadata_passthrough",
	},
	"compaction_summary": {
		"type", "id", "encrypted_content", "internal_chat_message_metadata_passthrough",
	},
	"context_compaction": {
		"type", "id", "encrypted_content", "internal_chat_message_metadata_passthrough",
	},
	"additional_tools": {"type", "id", "role", "tools"},
	"item_reference":   {"type", "id"},
}

var codexContentKeyOrder = []string{
	"type", "text", "image_url", "file_id", "detail", "audio_url", "annotations", "logprobs",
	"encrypted_content",
}

var codexToolKeyOrder = []string{
	"type", "name", "description", "parameters", "strict", "format", "display_name",
	"defer_loading", "namespace", "function",
}

var codexReasoningKeyOrder = []string{"effort", "summary", "context"}
var codexTextKeyOrder = []string{"verbosity", "format"}
var codexTextFormatKeyOrder = []string{"type", "strict", "schema", "name"}
var codexActionKeyOrder = []string{
	"type", "command", "timeout_ms", "working_directory", "env", "user",
	"query", "queries", "url", "pattern",
}
var codexWorkspaceKeyOrder = []string{
	"associated_remote_urls", "latest_git_commit_hash", "has_changes",
}

// marshalCodexResponsesJSON serializes a native Codex Responses request using
// the current codex-rs struct key order. It intentionally only changes object
// ordering; values and unknown keys are preserved.
func marshalCodexResponsesJSON(v any) ([]byte, error) {
	return marshalCodexOrderedJSON(v, codexJSONOrderRoot)
}

func marshalCodexClientMetadataJSON(v any) ([]byte, error) {
	return marshalCodexOrderedJSON(v, codexJSONOrderClientMetadata)
}

func marshalCodexTurnMetadataJSON(v any) ([]byte, error) {
	return marshalCodexOrderedJSON(v, codexJSONOrderTurnMetadata)
}

func marshalCodexOrderedJSON(v any, order codexJSONOrder) ([]byte, error) {
	return appendCodexOrderedJSON(make([]byte, 0, 1024), v, order)
}

func appendCodexOrderedJSON(dst []byte, v any, order codexJSONOrder) ([]byte, error) {
	switch typed := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case map[string]any:
		return appendCodexOrderedObject(dst, typed, order)
	case map[string]string:
		return appendCodexOrderedStringObject(dst, typed, order)
	case []any:
		dst = append(dst, '[')
		for index, item := range typed {
			if index > 0 {
				dst = append(dst, ',')
			}
			var err error
			dst, err = appendCodexOrderedJSON(dst, item, order)
			if err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case []map[string]any:
		dst = append(dst, '[')
		for index, item := range typed {
			if index > 0 {
				dst = append(dst, ',')
			}
			var err error
			dst, err = appendCodexOrderedObject(dst, item, order)
			if err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case []string:
		dst = append(dst, '[')
		for index, item := range typed {
			if index > 0 {
				dst = append(dst, ',')
			}
			dst = appendCodexJSONString(dst, item)
		}
		return append(dst, ']'), nil
	case string:
		return appendCodexJSONString(dst, typed), nil
	case json.RawMessage:
		compacted, err := compactCodexRawJSON(typed)
		if err != nil {
			return nil, err
		}
		return append(dst, compacted...), nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return append(dst, raw...), nil
	}
}

func appendCodexOrderedObject(dst []byte, object map[string]any, order codexJSONOrder) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	itemType, _ := object["type"].(string)
	keys = orderCodexJSONKeys(order, itemType, keys)

	dst = append(dst, '{')
	for index, key := range keys {
		if index > 0 {
			dst = append(dst, ',')
		}
		dst = appendCodexJSONString(dst, key)
		dst = append(dst, ':')
		var err error
		dst, err = appendCodexOrderedJSON(dst, object[key], codexChildJSONOrder(order, key))
		if err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

func appendCodexOrderedStringObject(dst []byte, object map[string]string, order codexJSONOrder) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	keys = orderCodexJSONKeys(order, "", keys)

	dst = append(dst, '{')
	for index, key := range keys {
		if index > 0 {
			dst = append(dst, ',')
		}
		dst = appendCodexJSONString(dst, key)
		dst = append(dst, ':')
		dst = appendCodexJSONString(dst, object[key])
	}
	return append(dst, '}'), nil
}

func orderCodexJSONKeys(order codexJSONOrder, itemType string, keys []string) []string {
	preferred := codexJSONPreferredKeys(order, itemType)
	if len(preferred) == 0 {
		sort.Strings(keys)
		return keys
	}
	present := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		present[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(preferred))
	for _, key := range preferred {
		if _, ok := present[key]; !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}
	rest := make([]string, 0, len(keys)-len(ordered))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

func codexJSONPreferredKeys(order codexJSONOrder, itemType string) []string {
	switch order {
	case codexJSONOrderRoot:
		// ResponsesWsRequest is an internally tagged enum: serde emits "type"
		// before the response.create struct fields. HTTP /responses has no
		// top-level type and the missing key is skipped by the caller.
		return append([]string{"type"}, codexRootKeyOrder...)
	case codexJSONOrderClientMetadata:
		return codexClientMetadataKeyOrder
	case codexJSONOrderTurnMetadata:
		return codexTurnMetadataKeyOrder
	case codexJSONOrderWorkspace:
		return codexWorkspaceKeyOrder
	case codexJSONOrderInputItem:
		if keys, ok := codexResponseItemKeyOrder[itemType]; ok {
			return keys
		}
		return []string{"type", "id"}
	case codexJSONOrderContent:
		return codexContentKeyOrder
	case codexJSONOrderTool:
		return codexToolKeyOrder
	case codexJSONOrderReasoning:
		return codexReasoningKeyOrder
	case codexJSONOrderText:
		return codexTextKeyOrder
	case codexJSONOrderTextFormat:
		return codexTextFormatKeyOrder
	case codexJSONOrderAction:
		return codexActionKeyOrder
	default:
		return nil
	}
}

func codexChildJSONOrder(parent codexJSONOrder, key string) codexJSONOrder {
	switch parent {
	case codexJSONOrderRoot:
		switch key {
		case "input":
			return codexJSONOrderInputItem
		case "tools":
			return codexJSONOrderTool
		case "client_metadata":
			return codexJSONOrderClientMetadata
		case "reasoning":
			return codexJSONOrderReasoning
		case "text":
			return codexJSONOrderText
		}
	case codexJSONOrderTurnMetadata:
		switch key {
		case "workspaces":
			return codexJSONOrderWorkspaceSet
		}
	case codexJSONOrderWorkspaceSet:
		return codexJSONOrderWorkspace
	case codexJSONOrderInputItem:
		switch key {
		case "content", "summary":
			return codexJSONOrderContent
		case "tools":
			return codexJSONOrderTool
		case "action":
			return codexJSONOrderAction
		}
	case codexJSONOrderTool:
		if key == "tools" {
			return codexJSONOrderTool
		}
	case codexJSONOrderText:
		if key == "format" {
			return codexJSONOrderTextFormat
		}
	}
	return codexJSONOrderGeneric
}

func appendCodexJSONString(dst []byte, value string) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return append(dst, `""`...)
	}
	encoded := buf.Bytes()
	if len(encoded) > 0 && encoded[len(encoded)-1] == '\n' {
		encoded = encoded[:len(encoded)-1]
	}
	return append(dst, encoded...)
}

func compactCodexRawJSON(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
