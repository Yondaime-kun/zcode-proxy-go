package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/translator"
)

const StartplanAnthropicURL = "https://zcode.z.ai/api/v1/zcode-plan/anthropic/v1/messages"

var (
	reInputTokens     = regexp.MustCompile(`"(?:input_tokens|prompt_tokens)":\s*(\d+)`)
	reOutputTokens    = regexp.MustCompile(`"(?:output_tokens|completion_tokens)":\s*(\d+)`)
	reCacheReadTokens = regexp.MustCompile(`"cache_read_input_tokens":\s*(\d+)`)
)

func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		ip := strings.TrimSpace(xrip)
		if ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func extractTokensFromJSON(body []byte) (int64, int64) {
	var parsed struct {
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			PromptTokens             int64 `json:"prompt_tokens"`
			CompletionTokens         int64 `json:"completion_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &parsed)

	in := parsed.Usage.InputTokens
	if in == 0 {
		in = parsed.Usage.PromptTokens
	}
	if in == 0 && (parsed.Usage.CacheReadInputTokens > 0 || parsed.Usage.CacheCreationInputTokens > 0) {
		in = parsed.Usage.CacheReadInputTokens + parsed.Usage.CacheCreationInputTokens
	}

	out := parsed.Usage.OutputTokens
	if out == 0 {
		out = parsed.Usage.CompletionTokens
	}

	return in, out
}

type ProxyHandler struct {
	cfg         *config.Config
	pool        *auth.AccountPool
	httpClient  *http.Client
	stats       *StatsTracker
	captchaPool *CaptchaPool
}

func NewProxyHandler(cfg *config.Config, cred *auth.Credential) *ProxyHandler {
	store := &auth.MultiCredentialStore{
		Active:   "default",
		Accounts: make(map[string]*auth.Credential),
	}
	if cred != nil {
		store.Accounts["default"] = cred
	}
	return NewProxyHandlerWithPool(cfg, auth.NewAccountPool(store, cfg.Routing))
}

func NewProxyHandlerWithPool(cfg *config.Config, pool *auth.AccountPool) *ProxyHandler {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	h := &ProxyHandler{
		cfg:  cfg,
		pool: pool,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   0, // Unlimited timeout for long LLM generation
		},
		stats: NewStatsTracker(nil),
	}
	if cfg.Plan == "start-plan" {
		h.captchaPool = NewCaptchaPool(cfg.Identity.AppVersion)
		h.captchaPool.Start()
	}
	return h
}

func (p *ProxyHandler) CaptchaPool() *CaptchaPool {
	return p.captchaPool
}

func (p *ProxyHandler) Close() {
	if p.captchaPool != nil {
		p.captchaPool.Stop()
	}
}

func (p *ProxyHandler) Stats() *StatsTracker {
	return p.stats
}

func (p *ProxyHandler) SetStats(st *StatsTracker) {
	if st != nil {
		p.stats = st
	}
}

func (p *ProxyHandler) SetOnStatsUpdate(fn func()) {
	if p.stats != nil {
		p.stats.onUpdate = fn
	}
}

func (p *ProxyHandler) Pool() *auth.AccountPool {
	return p.pool
}

func (p *ProxyHandler) GetUpstreamURL() string {
	if p.cfg.Plan == "start-plan" {
		return StartplanAnthropicURL
	}
	provUrls, ok := p.cfg.Providers[p.cfg.Provider]
	if ok && provUrls.AnthropicBase != "" {
		return fmt.Sprintf("%s/v1/messages", provUrls.AnthropicBase)
	}
	return "https://api.z.ai/api/anthropic/v1/messages"
}

func (p *ProxyHandler) BuildUpstreamHeaders(sessionID string, cred *auth.Credential) map[string]string {
	headers := BuildLlmIdentityHeaders(p.cfg)
	trace := BuildTraceHeaders(p.cfg.Plan)
	for k, v := range trace {
		headers[k] = v
	}

	headers["anthropic-version"] = AnthropicVersion
	headers["content-type"] = "application/json"

	if p.cfg.Plan == "start-plan" {
		if cred != nil && cred.Jwt != "" {
			headers["authorization"] = "Bearer " + cred.Jwt
		}
		var tok *CaptchaToken
		var err error
		if p.captchaPool != nil {
			tok, err = p.captchaPool.TakeToken(context.Background())
		} else {
			tok, err = SolveCaptchaOnDemand(context.Background(), p.cfg.Identity.AppVersion)
		}
		if err == nil && tok != nil && tok.VerifyParam != "" {
			log.Printf("[captcha] adding verify param to request (len=%d, region=%s)", len(tok.VerifyParam), tok.Region)
			headers["x-aliyun-captcha-verify-param"] = tok.VerifyParam
			if tok.Region != "" {
				headers["x-aliyun-captcha-verify-region"] = tok.Region
			}
		} else {
			log.Printf("[captcha] WARNING: no verify param token generated: %v", err)
		}
	} else {
		if cred != nil {
			credStr := cred.CredentialString()
			headers["x-api-key"] = credStr
			headers["authorization"] = "Bearer " + credStr
		}
	}

	return headers
}

func (p *ProxyHandler) sendWithRetry(ctx context.Context, url string, headers map[string]string, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := p.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(500*attempt) * time.Millisecond):
		}
	}
	return nil, lastErr
}

// ExecuteWithCaptchaRetry executes the upstream request with automatic detection and retry
// when an Aliyun captcha challenge (code 3007 or verify-param header) is received on start-plan.
// For successful SSE responses (HTTP 200 with text/event-stream), returns (*http.Response, nil, nil).
// For non-SSE responses, reads and returns (*http.Response, []byte, nil).
func (p *ProxyHandler) ExecuteWithCaptchaRetry(
	ctx context.Context,
	upstreamURL string,
	headers map[string]string,
	body []byte,
) (*http.Response, []byte, error) {
	maxCaptchaRetries := p.cfg.Captcha.MaxRetries
	if maxCaptchaRetries <= 0 {
		maxCaptchaRetries = 3
	}

	for captchaAttempt := 0; captchaAttempt <= maxCaptchaRetries; captchaAttempt++ {
		resp, err := p.sendWithRetry(ctx, upstreamURL, headers, body)
		if err != nil {
			return nil, nil, err
		}

		isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
		if isSSE && resp.StatusCode == http.StatusOK {
			return resp, nil, nil
		}

		respBody, rErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rErr != nil {
			return nil, nil, fmt.Errorf("failed to read upstream response body: %w", rErr)
		}

		// Detect captcha challenge for start-plan
		if p.cfg.Plan == "start-plan" && IsCaptchaChallenged(resp.StatusCode, resp.Header, respBody) {
			if captchaAttempt < maxCaptchaRetries {
				log.Printf("[captcha] upstream captcha challenge detected (status=%d, code=3007). Re-solving fresh token and retrying (attempt %d/%d)...",
					resp.StatusCode, captchaAttempt+1, maxCaptchaRetries)

				var tok *CaptchaToken
				var solveErr error
				if p.captchaPool != nil {
					tok, solveErr = p.captchaPool.TakeToken(ctx)
				} else {
					tok, solveErr = SolveCaptchaOnDemand(ctx, p.cfg.Identity.AppVersion)
				}
				if solveErr != nil || tok == nil || tok.VerifyParam == "" {
					log.Printf("[captcha] failed to solve fresh captcha token: %v", solveErr)
					return resp, respBody, nil
				}

				log.Printf("[captcha] fresh token obtained (len=%d, region=%s). Retrying upstream request...", len(tok.VerifyParam), tok.Region)
				headers["x-aliyun-captcha-verify-param"] = tok.VerifyParam
				if tok.Region != "" {
					headers["x-aliyun-captcha-verify-region"] = tok.Region
				}
				continue
			} else {
				log.Printf("[captcha] captcha challenge retries exhausted (%d attempts)", maxCaptchaRetries)
			}
		}

		return resp, respBody, nil
	}

	return nil, nil, fmt.Errorf("unexpected end of captcha retry loop")
}

func isQuotaExceeded(statusCode int, body []byte) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if len(body) > 0 {
		var raw map[string]interface{}
		if json.Unmarshal(body, &raw) == nil {
			if c, ok := raw["code"].(float64); ok && int(c) == 1605 {
				return true
			}
			if msg, ok := raw["msg"].(string); ok && (strings.Contains(msg, "quota") || strings.Contains(msg, "配额")) {
				return true
			}
		}
	}
	return false
}

func (p *ProxyHandler) HandleMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"failed to read request body"}}`, http.StatusBadRequest)
		return
	}

	var meta struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &meta)
	modelName := meta.Model
	if modelName == "" {
		modelName = "unknown"
	}

	clientIP := GetClientIP(r)
	log.Printf("[http] %s -> POST /v1/messages (model: %s)", clientIP, modelName)

	var (
		statusCode = 500
		inTok      int64
		outTok     int64
	)
	estInTok := EstimateInputTokens(body)
	done := p.stats.RecordRequestStart(modelName, clientIP)
	defer func() {
		if inTok == 0 && estInTok > 0 {
			inTok = estInTok
		}
		done(statusCode, inTok, outTok)
		log.Printf("[http] %s <- POST /v1/messages (%d, in=%s, out=%s)", clientIP, statusCode, FormatTokens(inTok), FormatTokens(outTok))
	}()

	totalAccounts := p.pool.TotalCount()
	if totalAccounts == 0 {
		http.Error(w, `{"error":{"type":"auth_error","message":"no accounts configured in credentials store"}}`, http.StatusUnauthorized)
		return
	}

	maxAttempts := totalAccounts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc := p.pool.GetNextAvailable()
		if acc == nil {
			log.Printf("[proxy] all accounts in pool are exhausted!")
			http.Error(w, `{"error":{"type":"quota_exhausted","message":"all accounts in pool have exceeded quota limit"}}`, http.StatusTooManyRequests)
			return
		}

		sessionID := randomHex(16)
		metaUserID := BuildAnthropicMetadataUserId(p.cfg.Identity.DeviceMid, sessionID)
		isStartPlan := p.cfg.Plan == "start-plan"
		transformedBody := TransformAnthropicBody(body, metaUserID, isStartPlan, meta.Model)

		upstreamURL := p.GetUpstreamURL()
		headers := p.BuildUpstreamHeaders(sessionID, acc.Credential)

		resp, respBody, err := p.ExecuteWithCaptchaRetry(r.Context(), upstreamURL, headers, transformedBody)
		if err != nil {
			log.Printf("[proxy] upstream connection error using account %q: %v", acc.Name, err)
			http.Error(w, fmt.Sprintf(`{"error":{"type":"upstream_unreachable","message":"%s"}}`, err.Error()), http.StatusBadGateway)
			return
		}

		if respBody != nil {
			if isQuotaExceeded(resp.StatusCode, respBody) {
				log.Printf("[proxy] account %q exceeded quota limit (code 1605 or 429). Failing over to next account...", acc.Name)
				p.pool.MarkExhausted(acc.Name, auth.DefaultExhaustedCooldown)
				continue
			}

			if p.cfg.Plan == "start-plan" && IsCaptchaChallenged(resp.StatusCode, resp.Header, respBody) {
				log.Printf("[proxy] account %q failed captcha verification after retries. Failing over to next account...", acc.Name)
				continue
			}

			inTok, outTok = extractTokensFromJSON(respBody)
			if inTok == 0 && estInTok > 0 {
				inTok = estInTok
			}
			statusCode = resp.StatusCode

			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(respBody)
			return
		}

		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		flusher, isFlusher := w.(http.Flusher)
		buf := make([]byte, 4096)
		var prevTail []byte
		var cacheReadTok int64
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				chunk := buf[:n]

				// Combine with tail of previous read to avoid missing regex match on chunk boundaries
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
				if m := reCacheReadTokens.FindSubmatch(searchBuf); len(m) > 1 {
					if val, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && val > 0 {
						cacheReadTok = val
					}
				}

				if len(chunk) > 128 {
					prevTail = append([]byte(nil), chunk[len(chunk)-128:]...)
				} else {
					prevTail = append([]byte(nil), chunk...)
				}

				_, _ = w.Write(chunk)
				if isFlusher {
					flusher.Flush()
				}
			}
			if rErr != nil {
				break
			}
		}

		if inTok == 0 && cacheReadTok > 0 {
			inTok = cacheReadTok
		}
		if inTok == 0 && estInTok > 0 {
			inTok = estInTok
		}
		statusCode = resp.StatusCode
		return
	}

	http.Error(w, `{"error":{"type":"quota_exhausted","message":"all accounts in pool have exceeded quota limit"}}`, http.StatusTooManyRequests)
}

func (p *ProxyHandler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"failed to read request body"}}`, http.StatusBadRequest)
		return
	}

	var openAIReq translator.OpenAIChatRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":"invalid JSON: %s"}}`, err.Error()), http.StatusBadRequest)
		return
	}

	isStream := openAIReq.Stream != nil && *openAIReq.Stream
	anthropicReq := translator.TranslateRequestOpenAIToAnthropic(&openAIReq)

	modelName := openAIReq.Model
	if modelName == "" {
		modelName = "unknown"
	}

	clientIP := GetClientIP(r)
	log.Printf("[http] %s -> POST /v1/chat/completions (model: %s)", clientIP, modelName)

	var (
		statusCode = 500
		inTok      int64
		outTok     int64
	)
	estInTok := EstimateInputTokens(body)
	done := p.stats.RecordRequestStart(modelName, clientIP)
	defer func() {
		if inTok == 0 && estInTok > 0 {
			inTok = estInTok
		}
		done(statusCode, inTok, outTok)
		log.Printf("[http] %s <- POST /v1/chat/completions (%d, in=%s, out=%s)", clientIP, statusCode, FormatTokens(inTok), FormatTokens(outTok))
	}()

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"internal_error","message":"failed to serialize translated request: %s"}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	totalAccounts := p.pool.TotalCount()
	if totalAccounts == 0 {
		http.Error(w, `{"error":{"type":"auth_error","message":"no accounts configured in credentials store"}}`, http.StatusUnauthorized)
		return
	}

	maxAttempts := totalAccounts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc := p.pool.GetNextAvailable()
		if acc == nil {
			log.Printf("[proxy] all accounts in pool are exhausted!")
			http.Error(w, `{"error":{"type":"quota_exhausted","message":"all accounts in pool have exceeded quota limit"}}`, http.StatusTooManyRequests)
			return
		}

		if p.cfg.Plan == "start-plan" && (acc.Credential == nil || acc.Credential.Jwt == "") {
			log.Printf("[proxy] account %q has no valid start-plan JWT, trying next...", acc.Name)
			p.pool.MarkExhausted(acc.Name, 10*time.Minute)
			continue
		}

		sessionID := randomHex(16)
		metaUserID := BuildAnthropicMetadataUserId(p.cfg.Identity.DeviceMid, sessionID)
		isStartPlan := p.cfg.Plan == "start-plan"
		transformedBody := TransformAnthropicBody(anthropicBody, metaUserID, isStartPlan, anthropicReq.Model)

		upstreamURL := p.GetUpstreamURL()
		headers := p.BuildUpstreamHeaders(sessionID, acc.Credential)

		resp, respBody, err := p.ExecuteWithCaptchaRetry(r.Context(), upstreamURL, headers, transformedBody)
		if err != nil {
			log.Printf("[proxy] upstream connection error using account %q: %v", acc.Name, err)
			http.Error(w, fmt.Sprintf(`{"error":{"type":"upstream_unreachable","message":"%s"}}`, err.Error()), http.StatusBadGateway)
			return
		}

		if respBody != nil {
			if isQuotaExceeded(resp.StatusCode, respBody) {
				log.Printf("[proxy] account %q exceeded quota limit (status=%d, body=%s). Marking exhausted and failing over...", acc.Name, resp.StatusCode, string(respBody))
				p.pool.MarkExhausted(acc.Name, auth.DefaultExhaustedCooldown)
				continue // retry with next account!
			}

			if p.cfg.Plan == "start-plan" && IsCaptchaChallenged(resp.StatusCode, resp.Header, respBody) {
				log.Printf("[proxy] account %q failed captcha verification after retries. Failing over to next account...", acc.Name)
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				log.Printf("[proxy] upstream returned error %d: %s", resp.StatusCode, string(respBody))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(respBody)
				return
			}

			// Check if upstream returned a business error envelope like {"code": ..., "msg": ...}
			var rawEnvelope map[string]interface{}
			if err := json.Unmarshal(respBody, &rawEnvelope); err == nil {
				if code, ok := rawEnvelope["code"]; ok {
					if c, ok := code.(float64); ok && c != 0 && c != 200 {
						log.Printf("[proxy] upstream returned business error code %v: %s", c, string(respBody))
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write(respBody)
						return
					}
				}
				if _, hasErr := rawEnvelope["error"]; hasErr {
					log.Printf("[proxy] upstream returned error object: %s", string(respBody))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write(respBody)
					return
				}
			}

			var anthropicResp translator.AnthropicMessagesResponse
			if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
				log.Printf("[proxy] invalid upstream response: %s", string(respBody))
				http.Error(w, fmt.Sprintf(`{"error":{"type":"translation_failed","message":"invalid upstream JSON: %s"}}`, string(respBody)), http.StatusBadGateway)
				return
			}

			inTok = int64(anthropicResp.Usage.InputTokens)
			outTok = int64(anthropicResp.Usage.OutputTokens)
			if inTok == 0 && anthropicResp.Usage.CacheReadInputTokens > 0 {
				inTok = int64(anthropicResp.Usage.CacheReadInputTokens)
			}
			if inTok == 0 && estInTok > 0 {
				inTok = estInTok
			}
			statusCode = http.StatusOK

			openAIResp := translator.TranslateResponseAnthropicToOpenAI(&anthropicResp)

			if isStream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)

				var content string
				if len(openAIResp.Choices) > 0 {
					if s, ok := openAIResp.Choices[0].Message.Content.(string); ok {
						content = s
					}
				}
				chunk := translator.OpenAIStreamChunk{
					ID:      openAIResp.ID,
					Object:  "chat.completion.chunk",
					Created: openAIResp.Created,
					Model:   openAIResp.Model,
					Choices: []translator.OpenAIStreamChoice{
						{
							Index: 0,
							Delta: translator.OpenAIStreamDelta{
								Role:    "assistant",
								Content: content,
							},
						},
					},
					Usage: openAIResp.Usage,
				}
				b, _ := json.Marshal(chunk)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(openAIResp)
			return
		}

		// SSE Stream
		defer resp.Body.Close()
		log.Printf("[proxy] upstream responded with stream using account %q: status=%d", acc.Name, resp.StatusCode)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		usage, streamErr := translator.StreamAnthropicToOpenAISSE(resp.Body, w, flusher, openAIReq.Model)
		if streamErr == nil {
			statusCode = http.StatusOK
		} else {
			statusCode = http.StatusInternalServerError
		}
		inTok = int64(usage.InputTokens)
		outTok = int64(usage.OutputTokens)
		if inTok == 0 && estInTok > 0 {
			inTok = estInTok
		}
		return
	}

	http.Error(w, `{"error":{"type":"quota_exhausted","message":"all accounts in pool have exceeded quota limit"}}`, http.StatusTooManyRequests)
}
