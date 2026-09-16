package proxy

import (
	"encoding/json"
	"math"
)

// EstimateInputTokens estimates token count from an Anthropic or OpenAI request JSON body.
// It acts as a reliable fallback when upstream does not return input_tokens.
func EstimateInputTokens(body []byte) int64 {
	if len(body) == 0 {
		return 0
	}

	var raw struct {
		System   interface{}   `json:"system"`
		Prompt   interface{}   `json:"prompt"`
		Messages []interface{} `json:"messages"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		// Fallback to byte length estimation (~4 characters/bytes per token)
		tok := int64(len(body) / 4)
		if tok < 1 {
			tok = 1
		}
		return tok
	}

	totalChars := 0

	var extractChars func(v interface{})
	extractChars = func(v interface{}) {
		if v == nil {
			return
		}
		switch val := v.(type) {
		case string:
			totalChars += len(val)
		case []interface{}:
			for _, item := range val {
				extractChars(item)
			}
		case map[string]interface{}:
			if text, ok := val["text"].(string); ok {
				totalChars += len(text)
			}
			if content, ok := val["content"]; ok {
				extractChars(content)
			}
		}
	}

	extractChars(raw.System)
	extractChars(raw.Prompt)

	for _, msg := range raw.Messages {
		if m, ok := msg.(map[string]interface{}); ok {
			extractChars(m["content"])
		}
	}

	if totalChars == 0 {
		tok := int64(len(body) / 4)
		if tok < 1 {
			tok = 1
		}
		return tok
	}

	// In natural language and code, 1 token is roughly 3.8 characters
	tok := int64(math.Ceil(float64(totalChars) / 3.8))
	if tok < 1 {
		tok = 1
	}
	return tok
}
