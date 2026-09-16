package translator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/provider"
)

const (
	DefaultMaxTokens        = 4096
	Glm53MinThinkingBudget  = 1024
)

func TranslateRequestOpenAIToAnthropic(req *OpenAIChatRequest) *AnthropicMessagesRequest {
	var systemParts []string
	var nonSystem []OpenAIMessage

	for _, m := range req.Messages {
		if m.Role == "system" {
			text := extractMessageText(m.Content)
			if text != "" {
				systemParts = append(systemParts, text)
			}
		} else {
			nonSystem = append(nonSystem, m)
		}
	}

	anthropicMessages := translateMessagesWithToolCoalescing(nonSystem)

	maxTokens := DefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	} else if mDef := provider.FindModel(req.Model); mDef != nil && mDef.MaxOutputTokens > 0 {
		maxTokens = mDef.MaxOutputTokens
	}

	result := &AnthropicMessagesRequest{
		Model:     req.Model,
		Messages:  anthropicMessages,
		MaxTokens: maxTokens,
		Stream:    req.Stream,
	}

	if len(systemParts) > 0 {
		result.System = strings.Join(systemParts, "\n\n")
	}

	if req.Temperature != nil {
		result.Temperature = req.Temperature
	}
	if req.TopP != nil {
		result.TopP = req.TopP
	}

	if req.Stop != nil {
		switch s := req.Stop.(type) {
		case string:
			result.StopSequences = []string{s}
		case []interface{}:
			for _, item := range s {
				if str, ok := item.(string); ok {
					result.StopSequences = append(result.StopSequences, str)
				}
			}
		case []string:
			result.StopSequences = s
		}
	}

	// Thinking / Reasoning handling
	applyThinking(req, result)

	// Tools handling
	if len(req.Tools) > 0 {
		var aTools []AnthropicToolDefinition
		for _, t := range req.Tools {
			if t.Type == "function" {
				schema := t.Function.Parameters
				if schema == nil {
					schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
				}
				aTools = append(aTools, AnthropicToolDefinition{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					InputSchema: schema,
				})
			}
		}
		result.Tools = aTools
	}

	if req.ToolChoice != nil {
		result.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	return result
}

func normalizeGlm53Effort(effort string) string {
	switch effort {
	case "none", "minimal", "light", "low":
		return "low"
	case "medium", "high":
		return "high"
	case "xhigh", "max", "ultra":
		return "max"
	default:
		return "max"
	}
}

func glm53Budget(effort string) int {
	switch effort {
	case "low":
		return 8000
	case "high":
		return 16000
	case "max":
		return 32000
	default:
		return 32000
	}
}

func applyThinking(req *OpenAIChatRequest, result *AnthropicMessagesRequest) {
	if provider.IsGlm53Model(req.Model) {
		effort := normalizeGlm53Effort(req.ReasoningEffort)
		budget := glm53Budget(effort)
		result.OutputConfig = &struct {
			Effort string `json:"effort,omitempty"`
		}{Effort: effort}
		result.Thinking = &ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: budget,
		}
	} else if req.Thinking != nil {
		result.Thinking = req.Thinking
	} else if req.ReasoningEffort != "" && req.ReasoningEffort != "none" {
		result.Thinking = &ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: Glm53MinThinkingBudget,
		}
	} else if provider.IsReasoningModel(req.Model) {
		result.Thinking = &ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: Glm53MinThinkingBudget,
		}
	}

	// If thinking is enabled, upstream Anthropic rejects temperature and top_p
	if result.Thinking != nil && (result.Thinking.Type == "enabled" || result.Thinking.Type == "adaptive") {
		result.Temperature = nil
		result.TopP = nil
		budget := result.Thinking.BudgetTokens
		if budget <= 0 {
			budget = Glm53MinThinkingBudget
			result.Thinking.BudgetTokens = budget
		}
		result.MaxTokens += budget
		if m := provider.FindModel(result.Model); m != nil && m.MaxOutputTokens > 0 {
			if result.MaxTokens > m.MaxOutputTokens {
				result.MaxTokens = m.MaxOutputTokens
			}
		}
	}
}

func extractMessageText(content interface{}) string {
	if content == nil {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var sb strings.Builder
		for _, part := range c {
			if m, ok := part.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if txt, ok := m["text"].(string); ok {
						sb.WriteString(txt)
					}
				}
			}
		}
		return sb.String()
	}
	return fmt.Sprintf("%v", content)
}

func translateMessagesWithToolCoalescing(messages []OpenAIMessage) []AnthropicMessage {
	var result []AnthropicMessage

	for i := 0; i < len(messages); i++ {
		m := messages[i]

		if m.Role == "tool" {
			// Coalesce consecutive tool messages into single user turn with tool_result blocks
			var toolResults []AnthropicContentBlock
			for ; i < len(messages) && messages[i].Role == "tool"; i++ {
				tm := messages[i]
				toolResults = append(toolResults, AnthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: tm.ToolCallID,
					Content:   extractMessageText(tm.Content),
				})
			}
			i-- // rewind last increment
			result = append(result, AnthropicMessage{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []AnthropicContentBlock
			txt := extractMessageText(m.Content)
			if txt != "" {
				blocks = append(blocks, AnthropicContentBlock{
					Type: "text",
					Text: txt,
				})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					input = map[string]interface{}{}
				}
				blocks = append(blocks, AnthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			result = append(result, AnthropicMessage{
				Role:    "assistant",
				Content: blocks,
			})
			continue
		}

		role := m.Role
		if role != "assistant" {
			role = "user"
		}

		text := extractMessageText(m.Content)
		result = append(result, AnthropicMessage{
			Role:    role,
			Content: text,
		})
	}

	return result
}

func translateToolChoice(choice interface{}) interface{} {
	switch c := choice.(type) {
	case string:
		if c == "auto" {
			return map[string]string{"type": "auto"}
		}
		if c == "required" {
			return map[string]string{"type": "any"}
		}
	case map[string]interface{}:
		if fn, ok := c["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				return map[string]string{"type": "tool", "name": name}
			}
		}
	}
	return map[string]string{"type": "auto"}
}

func TranslateResponseAnthropicToOpenAI(resp *AnthropicMessagesResponse) *OpenAIChatResponse {
	var textParts []string
	var reasoningParts []string
	var toolCalls []OpenAIToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			reasoningParts = append(reasoningParts, block.Thinking)
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   block.ID,
				Type: "function",
				Function: OpenAIToolCallFunction{
					Name:      block.Name,
					Arguments: string(args),
				},
			})
		}
	}

	content := strings.Join(textParts, "")
	var reasoning string
	if len(reasoningParts) > 0 {
		reasoning = strings.Join(reasoningParts, "\n")
	}

	finishReason := "stop"
	switch resp.StopReason {
	case "max_tokens":
		finishReason = "length"
	case "tool_use":
		finishReason = "tool_calls"
	}

	msg := OpenAIMessage{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
	}

	cachedTokens := resp.Usage.CacheReadInputTokens
	var promptDetails *struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	}
	if cachedTokens > 0 {
		promptDetails = &struct {
			CachedTokens int `json:"cached_tokens,omitempty"`
		}{CachedTokens: cachedTokens}
	}

	inTok := resp.Usage.InputTokens
	if inTok == 0 && cachedTokens > 0 {
		inTok = cachedTokens
	}

	return &OpenAIChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: []OpenAIChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: &OpenAIUsage{
			PromptTokens:        inTok,
			CompletionTokens:    resp.Usage.OutputTokens,
			TotalTokens:         inTok + resp.Usage.OutputTokens,
			PromptTokensDetails: promptDetails,
		},
	}
}
