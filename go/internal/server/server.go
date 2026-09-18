package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/provider"
	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
	"github.com/yondaime-kun/zcode-proxy-go/internal/translator"
)

type Server struct {
	cfg            *config.Config
	pool           *auth.AccountPool
	proxyHandler   *proxy.ProxyHandler
	responsesStore *translator.ResponseStore
	ipFilter       *IPFilter
	httpServer     *http.Server
}

func NewServerWithPool(cfg *config.Config, pool *auth.AccountPool) *Server {
	return &Server{
		cfg:            cfg,
		pool:           pool,
		proxyHandler:   proxy.NewProxyHandlerWithPool(cfg, pool),
		responsesStore: translator.NewResponseStore(),
		ipFilter:       NewIPFilter(cfg.Security.Whitelist, cfg.Security.Blacklist),
	}
}

func NewServer(cfg *config.Config, cred *auth.Credential) *Server {
	store := &auth.MultiCredentialStore{
		Active:   "default",
		Accounts: make(map[string]*auth.Credential),
	}
	if cred != nil {
		store.Accounts["default"] = cred
	}
	return NewServerWithPool(cfg, auth.NewAccountPool(store, cfg.Routing))
}

func (s *Server) ProxyHandler() *proxy.ProxyHandler {
	return s.proxyHandler
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Endpoints
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			s.handleHealth(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/models", s.handleListModels)
	mux.HandleFunc("/v1/chat/completions", s.proxyHandler.HandleChatCompletions)
	mux.HandleFunc("/v1/messages", s.proxyHandler.HandleMessages)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/quota", s.handleQuota)

	handler := s.corsMiddleware(s.ipFilterMiddleware(s.authMiddleware(mux)))

	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	log.Printf("zcode-proxy (Go) listening on http://%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	log.Printf("  provider: %s", s.cfg.Provider)
	log.Printf("  plan: %s", s.cfg.Plan)
	log.Printf("  models: %d available", len(s.cfg.Models))
	if len(s.cfg.Security.Whitelist) > 0 {
		log.Printf("  ip whitelist: %s", strings.Join(s.cfg.Security.Whitelist, ", "))
	}
	if len(s.cfg.Security.Blacklist) > 0 {
		log.Printf("  ip blacklist: %s", strings.Join(s.cfg.Security.Blacklist, ", "))
	}
	if s.cfg.Responses.Enabled {
		log.Printf("  /v1/responses: ON")
	}

	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.proxyHandler != nil {
		s.proxyHandler.Close()
	}
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ipFilterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && r.URL.Path != "/" {
			clientIP := GetClientIP(r)
			if allowed, reason := s.ipFilter.IsAllowed(clientIP); !allowed {
				log.Printf("[security] blocked request from %s to %s (%s)", clientIP, r.URL.Path, reason)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"type":"forbidden","message":"access denied: %s"}}`, reason)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Auth.ProxyApiKey != "" && r.URL.Path != "/health" && r.URL.Path != "/" {
			clientIP := GetClientIP(r)
			clientKey := r.Header.Get("Authorization")
			if strings.HasPrefix(clientKey, "Bearer ") {
				clientKey = strings.TrimPrefix(clientKey, "Bearer ")
			}
			if clientKey == "" {
				clientKey = r.Header.Get("X-Api-Key")
			}
			if clientKey == "" || subtle.ConstantTimeCompare([]byte(clientKey), []byte(s.cfg.Auth.ProxyApiKey)) != 1 {
				log.Printf("[auth] unauthorized request from %s to %s (invalid or missing proxy API key)", clientIP, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"Invalid or missing proxy API key"}}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":   "ok",
		"provider": s.cfg.Provider,
	})
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	type ModelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}

	var data []ModelItem
	for _, m := range provider.Models {
		data = append(data, ModelItem{
			ID:      m.ID,
			Object:  "model",
			OwnedBy: "zcode-proxy",
		})
	}

	resp := map[string]interface{}{
		"object": "list",
		"data":   data,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Responses.Enabled {
		http.Error(w, `{"error":{"type":"not_found_error","message":"Responses API disabled"}}`, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"type":"invalid_request_error","message":"failed to read request body"}}`, http.StatusBadRequest)
		return
	}

	var respReq translator.ResponsesRequest
	if err := json.Unmarshal(body, &respReq); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":"%s"}}`, err.Error()), http.StatusBadRequest)
		return
	}

	chatReq := translator.ResponsesToChatCompletions(&respReq)
	anthropicReq := translator.TranslateRequestOpenAIToAnthropic(chatReq)

	modelName := anthropicReq.Model
	if modelName == "" {
		modelName = "unknown"
	}

	clientIP := GetClientIP(r)
	log.Printf("[http] %s -> POST /v1/responses (model: %s)", clientIP, modelName)

	var (
		statusCode = 500
		inTok      int64
		outTok     int64
	)
	estInTok := proxy.EstimateInputTokens(body)
	done := s.proxyHandler.Stats().RecordRequestStart(modelName, clientIP)
	defer func() {
		if inTok == 0 && estInTok > 0 {
			inTok = estInTok
		}
		done(statusCode, inTok, outTok)
		log.Printf("[http] %s <- POST /v1/responses (%d, in=%s, out=%s)", clientIP, statusCode, proxy.FormatTokens(inTok), proxy.FormatTokens(outTok))
	}()

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"internal_error","message":"%s"}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	traceHeaders := proxy.BuildTraceHeaders(s.cfg.Plan)
	sessionID := traceHeaders["x-session-id"]
	metaUserID := proxy.BuildAnthropicMetadataUserId(s.cfg.Identity.DeviceMid, sessionID)
	isStartPlan := s.cfg.Plan == "start-plan"
	transformedBody := proxy.TransformAnthropicBody(anthropicBody, metaUserID, isStartPlan, anthropicReq.Model)

	upstreamURL := s.proxyHandler.GetUpstreamURL()
	acc := s.pool.GetNextAvailable()
	if acc == nil || acc.Credential == nil {
		http.Error(w, `{"error":{"type":"auth_error","message":"no available account in pool"}}`, http.StatusUnauthorized)
		return
	}

	headers := s.proxyHandler.BuildUpstreamHeaders(sessionID, acc.Credential)
	resp, respBody, err := s.proxyHandler.ExecuteWithCaptchaRetry(r.Context(), upstreamURL, headers, transformedBody)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"upstream_unreachable","message":"%s"}}`, err.Error()), http.StatusBadGateway)
		return
	}

	if respBody != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(respBody)
			return
		}
	} else {
		// In case respBody was nil from unexpected SSE, drain it
		respBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	upBytes := respBody
	var aResp translator.AnthropicMessagesResponse
	if err := json.Unmarshal(upBytes, &aResp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"type":"translation_failed","message":"%s"}}`, err.Error()), http.StatusBadGateway)
		return
	}

	inTok = int64(aResp.Usage.InputTokens)
	outTok = int64(aResp.Usage.OutputTokens)
	if inTok == 0 && aResp.Usage.CacheReadInputTokens > 0 {
		inTok = int64(aResp.Usage.CacheReadInputTokens)
	}
	if inTok == 0 && estInTok > 0 {
		inTok = estInTok
	}
	statusCode = http.StatusOK

	chatResp := translator.TranslateResponseAnthropicToOpenAI(&aResp)
	responsesResp := translator.ChatToResponses(chatResp)
	s.responsesStore.Save(*responsesResp)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(responsesResp)
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	accName := r.URL.Query().Get("account")
	var accountsToQuery []*auth.AccountStatus

	if accName != "" {
		for _, a := range s.pool.All() {
			if a.Name == accName {
				accountsToQuery = append(accountsToQuery, a)
				break
			}
		}
		if len(accountsToQuery) == 0 {
			http.Error(w, fmt.Sprintf(`{"error":{"type":"not_found","message":"account %q not found"}}`, accName), http.StatusNotFound)
			return
		}
	} else {
		accountsToQuery = s.pool.All()
	}

	if len(accountsToQuery) == 0 {
		http.Error(w, `{"error":{"type":"not_logged_in","message":"no accounts configured in proxy"}}`, http.StatusUnauthorized)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	balanceURL := fmt.Sprintf("%s/api/v1/zcode-plan/billing/balance", s.cfg.Claim.Origin)

	// If single account requested or only 1 account exists, return raw balance JSON
	if len(accountsToQuery) == 1 {
		acc := accountsToQuery[0]
		jwt := ""
		if acc.Credential != nil {
			jwt = acc.Credential.Jwt
		}
		if jwt == "" {
			http.Error(w, `{"error":{"type":"not_logged_in","message":"account has no start-plan JWT"}}`, http.StatusUnauthorized)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), "GET", balanceURL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		headers := proxy.BuildControlIdentityHeaders(s.cfg)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"failed to fetch quota: %s"}`, err.Error()), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		return
	}

	// Multi-account summary
	type AccountQuotaSummary struct {
		Name           string      `json:"name"`
		Available      bool        `json:"available"`
		ExhaustedUntil *time.Time  `json:"exhausted_until,omitempty"`
		Balance        interface{} `json:"balance,omitempty"`
		Error          string      `json:"error,omitempty"`
	}

	results := make([]AccountQuotaSummary, 0, len(accountsToQuery))
	for _, acc := range accountsToQuery {
		item := AccountQuotaSummary{
			Name:      acc.Name,
			Available: acc.IsAvailable(),
		}
		if !acc.ExhaustedUntil.IsZero() {
			item.ExhaustedUntil = &acc.ExhaustedUntil
		}

		if acc.Credential != nil && acc.Credential.Jwt != "" {
			req, _ := http.NewRequestWithContext(r.Context(), "GET", balanceURL, nil)
			headers := proxy.BuildControlIdentityHeaders(s.cfg)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			req.Header.Set("Authorization", "Bearer "+acc.Credential.Jwt)
			req.Header.Set("Accept", "application/json")

			resp, err := client.Do(req)
			if err == nil {
				var bData interface{}
				_ = json.NewDecoder(resp.Body).Decode(&bData)
				resp.Body.Close()
				item.Balance = bData
			} else {
				item.Error = err.Error()
			}
		} else {
			item.Error = "no start-plan JWT"
		}
		results = append(results, item)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"routing":        s.pool.Mode(),
		"total_accounts": len(results),
		"accounts":       results,
	})
}
