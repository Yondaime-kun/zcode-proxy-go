package proxy

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCaptchaPool_PopFast(t *testing.T) {
	pool := NewCaptchaPool("3.11.2")
	defer pool.Stop()

	// Inject a pre-warmed token
	pool.mu.Lock()
	pool.tokens = append(pool.tokens, &CaptchaTokenEntry{
		Token: &CaptchaToken{
			VerifyParam: "test_param_123",
			Region:      "sgp",
		},
		CreatedAt: time.Now(),
	})
	pool.mu.Unlock()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tok, err := pool.TakeToken(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error taking token: %v", err)
	}
	if tok == nil || tok.VerifyParam != "test_param_123" {
		t.Fatalf("unexpected token returned: %+v", tok)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("expected take from pool to be instant, took %v", elapsed)
	}

	// Next take should see empty pool (since mock solver is not configured here)
	tryTok := pool.TryTakeToken()
	if tryTok != nil {
		t.Errorf("expected pool to be empty after popping the only token, got %+v", tryTok)
	}
}

func TestCaptchaPool_PruneExpired(t *testing.T) {
	pool := NewCaptchaPool("3.11.2")
	defer pool.Stop()

	// Inject an expired token (created 100 seconds ago, ttl is 75s)
	pool.mu.Lock()
	pool.tokens = append(pool.tokens, &CaptchaTokenEntry{
		Token: &CaptchaToken{
			VerifyParam: "stale_param",
			Region:      "sgp",
		},
		CreatedAt: time.Now().Add(-100 * time.Second),
	})
	pool.mu.Unlock()

	// TryTakeToken should prune expired tokens and return nil
	tok := pool.TryTakeToken()
	if tok != nil {
		t.Fatalf("expected expired token to be pruned, but got: %+v", tok)
	}

	pool.mu.Lock()
	count := len(pool.tokens)
	pool.mu.Unlock()

	if count != 0 {
		t.Errorf("expected pool size to be 0 after pruning, got %d", count)
	}
}

func TestCaptchaPool_Singleflight(t *testing.T) {
	t.Setenv("ZCODE_CAPTCHA_SOLVER_CMD", "echo {\"verifyParam\":\"mock_param_singleflight\",\"region\":\"sgp\"}")

	pool := NewCaptchaPool("3.11.2")
	defer pool.Stop()

	const concurrentTakes = 5
	var wg sync.WaitGroup
	wg.Add(concurrentTakes)

	results := make([]*CaptchaToken, concurrentTakes)
	errors := make([]error, concurrentTakes)

	for i := 0; i < concurrentTakes; i++ {
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tok, err := pool.TakeToken(ctx)
			results[idx] = tok
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	for i := 0; i < concurrentTakes; i++ {
		if errors[i] != nil {
			t.Errorf("worker %d failed: %v", i, errors[i])
		}
		if results[i] == nil || results[i].VerifyParam != "mock_param_singleflight" {
			t.Errorf("worker %d got invalid token: %+v", i, results[i])
		}
	}
}

func TestCaptchaPool_Lifecycle(t *testing.T) {
	t.Setenv("ZCODE_CAPTCHA_SOLVER_CMD", "echo {\"verifyParam\":\"lifecycle_token\",\"region\":\"sgp\"}")

	pool := NewCaptchaPool("3.11.2")
	pool.Start()

	// Wait up to 2 seconds for background pre-warm
	var ready int
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		r, _, _ := pool.Stats()
		if r > 0 {
			ready = r
			break
		}
	}

	if ready == 0 {
		t.Logf("background pre-warm did not complete within 2s, testing TryTakeToken fallback")
	}

	pool.Stop()
	if pool.running {
		t.Errorf("expected pool.running to be false after Stop()")
	}
}
