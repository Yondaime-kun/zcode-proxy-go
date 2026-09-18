package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
)

func TestIsCaptchaChallenged(t *testing.T) {
	// 1. Success 200 should never be challenged
	if IsCaptchaChallenged(200, nil, []byte(`{"code":3007}`)) {
		t.Errorf("expected 200 to not be challenged")
	}

	// 2. Header variant on 400
	h := make(http.Header)
	h.Set("x-aliyun-captcha-verify-param", "param123")
	if !IsCaptchaChallenged(400, h, nil) {
		t.Errorf("expected header variant to be detected")
	}

	// 3. Body variant with "code": 3007
	if !IsCaptchaChallenged(400, nil, []byte(`{"code":3007,"msg":"captcha verify failed"}`)) {
		t.Errorf("expected code:3007 to be detected")
	}

	if !IsCaptchaChallenged(400, nil, []byte(`{"code": 3007,"msg":"failed"}`)) {
		t.Errorf("expected code: 3007 with space to be detected")
	}

	// 4. Message variant
	if !IsCaptchaChallenged(400, nil, []byte(`{"code":1001,"msg":"captcha verification failed"}`)) {
		t.Errorf("expected captcha verify failed message to be detected")
	}

	// 5. Normal error should not be captcha challenge
	if IsCaptchaChallenged(400, nil, []byte(`{"error":{"type":"invalid_request_error"}}`)) {
		t.Errorf("expected normal error to not be challenged")
	}

	// 6. 429 quota should not be captcha challenge
	if IsCaptchaChallenged(429, nil, []byte(`{"code":1605,"msg":"quota exceeded"}`)) {
		t.Errorf("expected 429 quota to not be challenged")
	}
}

func TestExecuteWithCaptchaRetry(t *testing.T) {
	// Set mock solver command
	t.Setenv("ZCODE_CAPTCHA_SOLVER_CMD", "echo {\"verifyParam\":\"fresh_token_123\",\"region\":\"sgp\"}")

	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			// First call: return in-body 3007 challenge
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":3007,"msg":"captcha verify failed"}`))
			return
		}

		// Subsequent call: verify new captcha header was passed, then return success
		if r.Header.Get("x-aliyun-captcha-verify-param") != "fresh_token_123" {
			t.Errorf("expected fresh captcha token header on retry, got %q", r.Header.Get("x-aliyun-captcha-verify-param"))
		}
		if r.Header.Get("x-aliyun-captcha-verify-region") != "sgp" {
			t.Errorf("expected region sgp on retry, got %q", r.Header.Get("x-aliyun-captcha-verify-region"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
	}))
	defer ts.Close()

	cfg := &config.Config{
		Provider: "zai",
		Plan:     "start-plan",
		Identity: config.IdentityConfig{
			AppVersion: "4.6.5",
		},
	}

	store := &auth.MultiCredentialStore{
		Active: "test",
		Accounts: map[string]*auth.Credential{
			"test": {Jwt: "test_jwt", Provider: "zai"},
		},
	}
	pool := auth.NewAccountPool(store, auth.RoutingFailover)
	handler := NewProxyHandlerWithPool(cfg, pool)
	defer handler.Close()

	headers := map[string]string{
		"authorization": "Bearer test_jwt",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, respBody, err := handler.ExecuteWithCaptchaRetry(ctx, ts.URL, headers, []byte(`{"model":"glm-5.3"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have retried at least once
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempts)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK after retry, got %d (body: %s)", resp.StatusCode, string(respBody))
	}
}
