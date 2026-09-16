package translator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type SSETranslationState struct {
	MessageID                 string
	Model                     string
	RoleSent                  bool
	FinishReasonSent          bool
	ToolCallCount             int
	BlockIdxToToolCallIdx     map[int]int
	Usage                     AnthropicUsage
}

func NewSSETranslationState(model string) *SSETranslationState {
	return &SSETranslationState{
		Model:                 model,
		BlockIdxToToolCallIdx: make(map[int]int),
	}
}

func (s *SSETranslationState) MakeChunk(delta OpenAIStreamDelta, finishReason *string, usage *OpenAIUsage) ([]byte, error) {
	id := s.MessageID
	if id == "" {
		id = "chatcmpl-stream"
	}
	chunk := OpenAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   s.Model,
		Choices: []OpenAIStreamChoice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}

	b, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("data: %s\n\n", string(b))), nil
}

// StreamAnthropicToOpenAISSE reads an Anthropic SSE stream from upstream and writes OpenAI SSE chunks to w
func StreamAnthropicToOpenAISSE(upstream io.Reader, w http.ResponseWriter, flusher http.Flusher, model string) (AnthropicUsage, error) {
	state := NewSSETranslationState(model)
	reader := bufio.NewReader(upstream)

	var currentEvent string
	var currentData bytes.Buffer

	emitChunk := func(delta OpenAIStreamDelta, finishReason *string, usage *OpenAIUsage) error {
		chunkBytes, err := state.MakeChunk(delta, finishReason, usage)
		if err != nil {
			return err
		}
		if _, err := w.Write(chunkBytes); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			break
		}

		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// End of SSE message block
			if currentData.Len() > 0 {
				rawJSON := currentData.Bytes()
				processAnthropicEvent(state, currentEvent, rawJSON, emitChunk)
				currentData.Reset()
			}
			currentEvent = ""
			if err != nil {
				break
			}
			continue
		}

		if strings.HasPrefix(trimmed, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		} else if strings.HasPrefix(trimmed, "data:") {
			dataContent := strings.TrimPrefix(trimmed[5:], " ")
			currentData.WriteString(dataContent)
		} else if strings.HasPrefix(trimmed, "{") {
			currentData.WriteString(trimmed)
		}

		if err != nil {
			break
		}
	}

	// Flush any remaining event data
	if currentData.Len() > 0 {
		processAnthropicEvent(state, currentEvent, currentData.Bytes(), emitChunk)
	}

	// Emit final [DONE]
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
	if state.Usage.InputTokens == 0 && state.Usage.CacheReadInputTokens > 0 {
		state.Usage.InputTokens = state.Usage.CacheReadInputTokens
	}
	return state.Usage, nil
}

func processAnthropicEvent(
	state *SSETranslationState,
	eventType string,
	data []byte,
	emit func(OpenAIStreamDelta, *string, *OpenAIUsage) error,
) {
	var root struct {
		Type    string `json:"type"`
		Message *struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				PromptTokens             int `json:"prompt_tokens"`
				CompletionTokens         int `json:"completion_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message,omitempty"`
		Index        int                    `json:"index"`
		ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"`
		Delta        *struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			Thinking    string `json:"thinking,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
			StopReason  string `json:"stop_reason,omitempty"`
		} `json:"delta,omitempty"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			PromptTokens             int `json:"prompt_tokens"`
			CompletionTokens         int `json:"completion_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage,omitempty"`
	}

	if err := json.Unmarshal(data, &root); err != nil {
		return
	}

	typ := root.Type
	if typ == "" {
		typ = eventType
	}

	switch typ {
	case "message_start":
		if root.Message != nil {
			state.MessageID = root.Message.ID
			if root.Message.Model != "" {
				state.Model = root.Message.Model
			}
			inTok := root.Message.Usage.InputTokens
			if inTok == 0 {
				inTok = root.Message.Usage.PromptTokens
			}
			if inTok > 0 {
				state.Usage.InputTokens = inTok
			}
			state.Usage.CacheReadInputTokens = root.Message.Usage.CacheReadInputTokens
			state.Usage.CacheCreationInputTokens = root.Message.Usage.CacheCreationInputTokens
		}
		if root.Usage != nil {
			inTok := root.Usage.InputTokens
			if inTok == 0 {
				inTok = root.Usage.PromptTokens
			}
			if inTok > 0 {
				state.Usage.InputTokens = inTok
			}
			if root.Usage.CacheReadInputTokens > 0 {
				state.Usage.CacheReadInputTokens = root.Usage.CacheReadInputTokens
			}
		}
		if !state.RoleSent {
			state.RoleSent = true
			_ = emit(OpenAIStreamDelta{Role: "assistant"}, nil, nil)
		}

	case "content_block_start":
		if root.ContentBlock != nil && root.ContentBlock.Type == "tool_use" {
			toolIdx := state.ToolCallCount
			state.ToolCallCount++
			state.BlockIdxToToolCallIdx[root.Index] = toolIdx

			_ = emit(OpenAIStreamDelta{
				ToolCalls: []OpenAIStreamToolCall{
					{
						Index: toolIdx,
						ID:    root.ContentBlock.ID,
						Type:  "function",
						Function: &OpenAIToolCallFunction{
							Name:      root.ContentBlock.Name,
							Arguments: "",
						},
					},
				},
			}, nil, nil)
		}

	case "content_block_delta":
		if root.Delta != nil {
			switch root.Delta.Type {
			case "text_delta":
				if root.Delta.Text != "" {
					_ = emit(OpenAIStreamDelta{Content: root.Delta.Text}, nil, nil)
				}
			case "thinking_delta":
				if root.Delta.Thinking != "" {
					_ = emit(OpenAIStreamDelta{ReasoningContent: root.Delta.Thinking}, nil, nil)
				}
			case "input_json_delta":
				if toolIdx, ok := state.BlockIdxToToolCallIdx[root.Index]; ok {
					_ = emit(OpenAIStreamDelta{
						ToolCalls: []OpenAIStreamToolCall{
							{
								Index: toolIdx,
								Function: &OpenAIToolCallFunction{
									Arguments: root.Delta.PartialJSON,
								},
							},
						},
					}, nil, nil)
				}
			}
		}

	case "message_delta":
		if root.Usage != nil {
			inTok := root.Usage.InputTokens
			if inTok == 0 {
				inTok = root.Usage.PromptTokens
			}
			if inTok > 0 && state.Usage.InputTokens == 0 {
				state.Usage.InputTokens = inTok
			}
			outTok := root.Usage.OutputTokens
			if outTok == 0 {
				outTok = root.Usage.CompletionTokens
			}
			if outTok > 0 {
				state.Usage.OutputTokens = outTok
			}
			if root.Usage.CacheReadInputTokens > 0 {
				state.Usage.CacheReadInputTokens = root.Usage.CacheReadInputTokens
			}
		}
		if root.Delta != nil && root.Delta.StopReason != "" && !state.FinishReasonSent {
			state.FinishReasonSent = true
			reason := mapStopReason(root.Delta.StopReason)
			usage := mapUsage(state.Usage)
			_ = emit(OpenAIStreamDelta{}, &reason, usage)
		}

	case "message_stop":
		if !state.FinishReasonSent {
			state.FinishReasonSent = true
			reason := "stop"
			usage := mapUsage(state.Usage)
			_ = emit(OpenAIStreamDelta{}, &reason, usage)
		}

	case "error":
		var errObj struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
			Msg string `json:"msg"`
		}
		_ = json.Unmarshal(data, &errObj)
		errMsg := errObj.Error.Message
		if errMsg == "" {
			errMsg = errObj.Msg
		}
		if errMsg == "" {
			errMsg = string(data)
		}
		_ = emit(OpenAIStreamDelta{Content: fmt.Sprintf("\n[Error: %s]", errMsg)}, nil, nil)
	}
}

func mapStopReason(r string) string {
	switch r {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func mapUsage(u AnthropicUsage) *OpenAIUsage {
	inTok := u.InputTokens
	if inTok == 0 && u.CacheReadInputTokens > 0 {
		inTok = u.CacheReadInputTokens
	}
	usage := &OpenAIUsage{
		PromptTokens:     inTok,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      inTok + u.OutputTokens,
	}
	if u.CacheReadInputTokens > 0 {
		usage.PromptTokensDetails = &struct {
			CachedTokens int `json:"cached_tokens,omitempty"`
		}{CachedTokens: u.CacheReadInputTokens}
	}
	return usage
}
