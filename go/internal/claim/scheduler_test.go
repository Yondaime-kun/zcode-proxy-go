package claim

import (
	"testing"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
)

func TestSchedulerAccountTag(t *testing.T) {
	tests := []struct {
		name     string
		account  string
		cred     *auth.Credential
		expected string
	}{
		{
			name:     "account with long userId",
			account:  "acc1",
			cred:     &auth.Credential{UserId: "272526baf123456789"},
			expected: "[acc1 · 272526ba…]",
		},
		{
			name:     "account with short userId",
			account:  "acc2",
			cred:     &auth.Credential{UserId: "abc"},
			expected: "[acc2 · abc]",
		},
		{
			name:     "account without userId",
			account:  "acc3",
			cred:     &auth.Credential{},
			expected: "[acc3]",
		},
		{
			name:     "empty account with userId",
			account:  "",
			cred:     &auth.Credential{UserId: "272526baf123"},
			expected: "[default · 272526ba…]",
		},
		{
			name:     "nil cred and empty account",
			account:  "",
			cred:     nil,
			expected: "[default]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scheduler{
				account: tt.account,
				cred:    tt.cred,
			}
			got := s.AccountTag()
			if got != tt.expected {
				t.Errorf("AccountTag() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStartAndStopScheduler(t *testing.T) {
	cfg := &config.Config{
		Claim: config.ClaimConfig{
			PollIntervalMs: 60000,
		},
	}
	cred := &auth.Credential{
		ApiKey: "test-key",
		Jwt:    "test-jwt",
	}

	called := false
	s := StartScheduler(cfg, "acc1", cred, func(account, planID string) {
		called = true
	})
	if s == nil {
		t.Fatal("expected scheduler to not be nil")
	}

	tag := s.AccountTag()
	if tag != "[acc1]" {
		t.Errorf("expected [acc1], got %s", tag)
	}

	time.Sleep(10 * time.Millisecond)
	s.Stop()

	if called {
		t.Error("callback should not be called without valid claim")
	}
}
