package translator

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranslateRequestOpenAIToAnthropic(t *testing.T) {
	req := &OpenAIChatRequest{
		Model: "glm-5.3",
		Messages: []OpenAIMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello world!"},
		},
		ReasoningEffort: "high",
	}

	aReq := TranslateRequestOpenAIToAnthropic(req)
	if aReq.Model != "glm-5.3" {
		t.Errorf("expected model glm-5.3, got %s", aReq.Model)
	}
	if aReq.System != "You are a helpful assistant." {
		t.Errorf("expected system message preserved, got %v", aReq.System)
	}
	if len(aReq.Messages) != 1 {
		t.Fatalf("expected 1 non-system message, got %d", len(aReq.Messages))
	}
	if aReq.Messages[0].Content != "Hello world!" {
		t.Errorf("expected message content 'Hello world!', got %v", aReq.Messages[0].Content)
	}
	if aReq.Thinking == nil || aReq.Thinking.Type != "enabled" {
		t.Errorf("expected thinking enabled for glm-5.3, got %v", aReq.Thinking)
	}
}

func TestTranslateResponseAnthropicToOpenAI(t *testing.T) {
	aResp := &AnthropicMessagesResponse{
		ID:    "msg_12345",
		Type:  "message",
		Role:  "assistant",
		Model: "glm-4.7",
		Content: []AnthropicContentBlock{
			{Type: "thinking", Thinking: "Let me think about this."},
			{Type: "text", Text: "Here is the answer."},
		},
		StopReason: "end_turn",
		Usage: AnthropicUsage{
			InputTokens:  50,
			OutputTokens: 20,
		},
	}

	oResp := TranslateResponseAnthropicToOpenAI(aResp)
	if oResp.ID != "msg_12345" {
		t.Errorf("expected id msg_12345, got %s", oResp.ID)
	}
	if len(oResp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(oResp.Choices))
	}
	choice := oResp.Choices[0]
	if choice.Message.Content != "Here is the answer." {
		t.Errorf("expected text 'Here is the answer.', got %v", choice.Message.Content)
	}
	if choice.Message.ReasoningContent != "Let me think about this." {
		t.Errorf("expected reasoning content, got %s", choice.Message.ReasoningContent)
	}
	if choice.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %s", choice.FinishReason)
	}
	if oResp.Usage.TotalTokens != 70 {
		t.Errorf("expected total tokens 70, got %d", oResp.Usage.TotalTokens)
	}
}

func TestStreamAnthropicToOpenAISSE(t *testing.T) {
	upstreamSSE := `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"glm-4.7","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world!"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

	rec := httptest.NewRecorder()
	_, err := StreamAnthropicToOpenAISSE(strings.NewReader(upstreamSSE), rec, nil, "glm-4.7")
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("expected output to contain [DONE]")
	}
	if !strings.Contains(out, "chat.completion.chunk") {
		t.Errorf("expected output to contain chat.completion.chunk")
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, " world!") {
		t.Errorf("expected output to contain stream chunks with text")
	}
}

func TestResponsesTranslator(t *testing.T) {
	req := &ResponsesRequest{
		Model: "glm-4.7",
		Input: "test question",
	}

	chatReq := ResponsesToChatCompletions(req)
	if chatReq.Model != "glm-4.7" {
		t.Errorf("model mismatch: %s", chatReq.Model)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Content != "test question" {
		t.Errorf("message conversion mismatch")
	}

	chatResp := &OpenAIChatResponse{
		Model: "glm-4.7",
		Choices: []OpenAIChoice{
			{
				Message: OpenAIMessage{
					Role:    "assistant",
					Content: "test answer",
				},
			},
		},
	}

	resp := ChatToResponses(chatResp)
	if resp.Model != "glm-4.7" {
		t.Errorf("responses model mismatch: %s", resp.Model)
	}
	if len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "test answer" {
		t.Errorf("responses output mismatch")
	}
}
