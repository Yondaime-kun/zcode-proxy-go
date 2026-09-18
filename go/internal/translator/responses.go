package translator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type ResponsesRequest struct {
	Model              string                 `json:"model"`
	Input              interface{}            `json:"input,omitempty"` // string or []ResponsesInputItem
	Messages           []OpenAIMessage        `json:"messages,omitempty"` // fallback if client sends Chat Completions format
	Instructions       string                 `json:"instructions,omitempty"`
	Tools              []OpenAIToolDefinition `json:"tools,omitempty"`
	Stream             bool                   `json:"stream,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	MaxOutputTokens    int                    `json:"max_output_tokens,omitempty"`
}

type ResponsesResponse struct {
	ID         string                 `json:"id"`
	Object     string                 `json:"object"`
	CreatedAt  int64                  `json:"created_at"`
	Status     string                 `json:"status"`
	Model      string                 `json:"model"`
	Output     []ResponsesOutputItem  `json:"output"`
	Usage      *OpenAIUsage           `json:"usage,omitempty"`
}

type ResponsesOutputItem struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"` // "message" or "function_call"
	Role    string                 `json:"role,omitempty"`
	Content []ResponsesContentPart `json:"content,omitempty"`
	Name    string                 `json:"name,omitempty"`
	CallID  string                 `json:"call_id,omitempty"`
	Args    string                 `json:"arguments,omitempty"`
}

type ResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ResponseStore struct {
	mu      sync.RWMutex
	entries map[string]ResponsesResponse
}

func NewResponseStore() *ResponseStore {
	return &ResponseStore{
		entries: make(map[string]ResponsesResponse),
	}
}

func (s *ResponseStore) Save(resp ResponsesResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[resp.ID] = resp
}

func (s *ResponseStore) Get(id string) (ResponsesResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp, ok := s.entries[id]
	return resp, ok
}

func GenerateResponsesID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("resp_%s", hex.EncodeToString(b))
}

func ResponsesToChatCompletions(req *ResponsesRequest) *OpenAIChatRequest {
	var messages []OpenAIMessage

	if req.Instructions != "" {
		messages = append(messages, OpenAIMessage{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	if req.Input != nil {
		switch in := req.Input.(type) {
		case string:
			messages = append(messages, OpenAIMessage{
				Role:    "user",
				Content: in,
			})
		case []interface{}:
			for _, item := range in {
				if m, ok := item.(map[string]interface{}); ok {
					role, _ := m["role"].(string)
					if role == "" {
						role = "user"
					}
					content := m["content"]
					messages = append(messages, OpenAIMessage{
						Role:    role,
						Content: content,
					})
				}
			}
		}
	} else if len(req.Messages) > 0 {
		messages = append(messages, req.Messages...)
	}

	return &OpenAIChatRequest{
		Model:     req.Model,
		Messages:  messages,
		Tools:     req.Tools,
		Stream:    &req.Stream,
		MaxTokens: &req.MaxOutputTokens,
	}
}

func ChatToResponses(chatResp *OpenAIChatResponse) *ResponsesResponse {
	respID := GenerateResponsesID()
	var output []ResponsesOutputItem

	if len(chatResp.Choices) > 0 {
		c := chatResp.Choices[0]
		txt := extractMessageText(c.Message.Content)
		if txt != "" {
			output = append(output, ResponsesOutputItem{
				ID:   fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Type: "message",
				Role: "assistant",
				Content: []ResponsesContentPart{
					{Type: "text", Text: txt},
				},
			})
		}
		for _, tc := range c.Message.ToolCalls {
			output = append(output, ResponsesOutputItem{
				ID:     tc.ID,
				Type:   "function_call",
				CallID: tc.ID,
				Name:   tc.Function.Name,
				Args:   tc.Function.Arguments,
			})
		}
	}

	return &ResponsesResponse{
		ID:        respID,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Model:     chatResp.Model,
		Output:    output,
		Usage:     chatResp.Usage,
	}
}
