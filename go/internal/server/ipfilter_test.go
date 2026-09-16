package server

import (
	"net/http"
	"testing"
)

func TestIPFilter(t *testing.T) {
	// Case 1: No whitelist or blacklist -> allow all
	f1 := NewIPFilter(nil, nil)
	if allowed, _ := f1.IsAllowed("1.2.3.4"); !allowed {
		t.Errorf("expected 1.2.3.4 to be allowed when no rules are set")
	}

	// Case 2: Blacklist single IP and CIDR
	f2 := NewIPFilter(nil, []string{"1.2.3.4", "10.0.0.0/8"})
	if allowed, _ := f2.IsAllowed("1.2.3.4"); allowed {
		t.Errorf("expected 1.2.3.4 to be blacklisted")
	}
	if allowed, _ := f2.IsAllowed("10.5.0.1"); allowed {
		t.Errorf("expected 10.5.0.1 to be blacklisted by CIDR")
	}
	if allowed, _ := f2.IsAllowed("192.168.1.1"); !allowed {
		t.Errorf("expected 192.168.1.1 to be allowed")
	}

	// Case 3: Whitelist single IP and CIDR
	f3 := NewIPFilter([]string{"127.0.0.1", "192.168.0.0/16"}, nil)
	if allowed, _ := f3.IsAllowed("127.0.0.1"); !allowed {
		t.Errorf("expected 127.0.0.1 to be allowed in whitelist")
	}
	if allowed, _ := f3.IsAllowed("192.168.1.50"); !allowed {
		t.Errorf("expected 192.168.1.50 to be allowed in whitelist CIDR")
	}
	if allowed, _ := f3.IsAllowed("8.8.8.8"); allowed {
		t.Errorf("expected 8.8.8.8 to be rejected when not in whitelist")
	}
}

func TestGetClientIP(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"

	if ip := GetClientIP(req); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}

	req.Header.Set("X-Forwarded-For", "5.6.7.8, 1.2.3.4")
	if ip := GetClientIP(req); ip != "5.6.7.8" {
		t.Errorf("expected 5.6.7.8 from X-Forwarded-For, got %s", ip)
	}

	req.Header.Set("X-Real-IP", "9.10.11.12")
	req.Header.Del("X-Forwarded-For")
	if ip := GetClientIP(req); ip != "9.10.11.12" {
		t.Errorf("expected 9.10.11.12 from X-Real-IP, got %s", ip)
	}
}
