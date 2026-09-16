package proxy

import (
	"encoding/json"
)

func TransformAnthropicBody(body []byte, metadataUserId string, startPlan bool, model string) []byte {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}

	modified := false

	// Start-plan system prompt injection
	if startPlan {
		existingSystem := root["system"]
		root["system"] = BuildStartPlanSystem(model, existingSystem)
		if msgs, ok := root["messages"].([]interface{}); ok && len(msgs) > 0 {
			root["messages"] = append([]interface{}{BuildContextPrefixMessage()}, msgs...)
		}
		modified = true
	}

	// Inject metadata.user_id if present
	if metadataUserId != "" {
		meta, ok := root["metadata"].(map[string]interface{})
		if !ok || meta == nil {
			meta = make(map[string]interface{})
			root["metadata"] = meta
		}
		meta["user_id"] = metadataUserId
		modified = true
	}

	// Two-phase cache_control injection on messages
	if messages, ok := root["messages"].([]interface{}); ok && len(messages) > 0 {
		// Phase 1: Strip existing cache_control
		for _, m := range messages {
			if mm, ok := m.(map[string]interface{}); ok {
				if contentList, ok := mm["content"].([]interface{}); ok {
					for _, b := range contentList {
						if bm, ok := b.(map[string]interface{}); ok {
							if _, hasCache := bm["cache_control"]; hasCache {
								delete(bm, "cache_control")
								modified = true
							}
						}
					}
				}
			}
		}

		// Phase 2: Add cache_control { type: "ephemeral" } to the last non-system message's last block
		lastIdx := len(messages) - 1
		if lastMsg, ok := messages[lastIdx].(map[string]interface{}); ok {
			switch content := lastMsg["content"].(type) {
			case string:
				lastMsg["content"] = []map[string]interface{}{
					{
						"type": "text",
						"text": content,
						"cache_control": map[string]string{
							"type": "ephemeral",
						},
					},
				}
				modified = true
			case []interface{}:
				if len(content) > 0 {
					lastBlockIdx := len(content) - 1
					if lastBlock, ok := content[lastBlockIdx].(map[string]interface{}); ok {
						lastBlock["cache_control"] = map[string]string{
							"type": "ephemeral",
						}
						modified = true
					}
				}
			}
		}
	}

	if modified {
		if out, err := json.Marshal(root); err == nil {
			return out
		}
	}
	return body
}
