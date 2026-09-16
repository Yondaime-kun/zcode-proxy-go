package proxy

import (
	"strconv"
	"testing"
)

func TestEstimateInputTokens(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		minTok   int64
		maxTok   int64
	}{
		{
			name:   "empty body",
			body:   "",
			minTok: 0,
			maxTok: 0,
		},
		{
			name:   "anthropic simple user message",
			body:   `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"Hello world"}]}`,
			minTok: 2,
			maxTok: 5,
		},
		{
			name: "anthropic system and multi-part content",
			body: `{
				"model":"claude-3-5-sonnet-20241022",
				"system":"You are an expert programmer and helpful assistant.",
				"messages":[
					{"role":"user","content":[{"type":"text","text":"Write a Golang HTTP server with graceful shutdown."}]}
				]
			}`,
			minTok: 15,
			maxTok: 40,
		},
		{
			name: "openai messages format",
			body: `{
				"model":"gpt-4o",
				"messages":[
					{"role":"system","content":"System prompt here"},
					{"role":"user","content":"User question here"}
				]
			}`,
			minTok: 5,
			maxTok: 20,
		},
		{
			name:   "non-json fallback",
			body:   "some raw body that has about 40 characters in it",
			minTok: 8,
			maxTok: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateInputTokens([]byte(tt.body))
			if got < tt.minTok || got > tt.maxTok {
				t.Errorf("EstimateInputTokens() = %d, want between %d and %d", got, tt.minTok, tt.maxTok)
			}
		})
	}
}

func TestExtractTokensFromJSON(t *testing.T) {
	t.Run("anthropic standard usage", func(t *testing.T) {
		body := []byte(`{"usage":{"input_tokens":120,"output_tokens":45}}`)
		in, out := extractTokensFromJSON(body)
		if in != 120 || out != 45 {
			t.Errorf("got (%d, %d), want (120, 45)", in, out)
		}
	})

	t.Run("openai style usage", func(t *testing.T) {
		body := []byte(`{"usage":{"prompt_tokens":350,"completion_tokens":80}}`)
		in, out := extractTokensFromJSON(body)
		if in != 350 || out != 80 {
			t.Errorf("got (%d, %d), want (350, 80)", in, out)
		}
	})

	t.Run("anthropic prompt cache read tokens", func(t *testing.T) {
		body := []byte(`{"usage":{"input_tokens":0,"cache_read_input_tokens":500,"output_tokens":15}}`)
		in, out := extractTokensFromJSON(body)
		if in != 500 || out != 15 {
			t.Errorf("got (%d, %d), want (500, 15)", in, out)
		}
	})
}

func TestSlidingChunkBoundaryRegex(t *testing.T) {
	// Chunk 1 cuts right in the middle of "input_tokens"
	chunk1 := []byte(`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_`)
	chunk2 := []byte(`tokens": 2048, "output_tokens": 99}}}` + "\n\n")

	var inTok, outTok int64
	var prevTail []byte

	chunks := [][]byte{chunk1, chunk2}
	for _, chunk := range chunks {
		var searchBuf []byte
		if len(prevTail) > 0 {
			searchBuf = append(prevTail, chunk...)
		} else {
			searchBuf = chunk
		}

		if m := reInputTokens.FindSubmatch(searchBuf); len(m) > 1 {
			if val, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && val > 0 {
				inTok = val
			}
		}
		if m := reOutputTokens.FindSubmatch(searchBuf); len(m) > 1 {
			if val, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && val > 0 {
				outTok = val
			}
		}

		if len(chunk) > 128 {
			prevTail = append([]byte(nil), chunk[len(chunk)-128:]...)
		} else {
			prevTail = append([]byte(nil), chunk...)
		}
	}

	if inTok != 2048 {
		t.Fatalf("expected inTok 2048 across boundary split, got %d", inTok)
	}
	if outTok != 99 {
		t.Fatalf("expected outTok 99, got %d", outTok)
	}
}
