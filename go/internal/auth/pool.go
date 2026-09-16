package auth

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	RoutingFailover   = "failover"
	RoutingRoundRobin = "round-robin"

	DefaultExhaustedCooldown = 1 * time.Hour
)

type AccountStatus struct {
	Name           string      `json:"name"`
	Credential     *Credential `json:"credential"`
	ExhaustedUntil time.Time   `json:"exhausted_until,omitempty"`
	FailureCount   int         `json:"failure_count"`
	LastUsed       time.Time   `json:"last_used,omitempty"`
}

func (a *AccountStatus) IsAvailable() bool {
	if a.ExhaustedUntil.IsZero() {
		return true
	}
	return time.Now().After(a.ExhaustedUntil)
}

type AccountPool struct {
	mu           sync.RWMutex
	accounts     []*AccountStatus
	accountMap   map[string]*AccountStatus
	currentIndex int
	mode         string
}

func NewAccountPool(store *MultiCredentialStore, mode string) *AccountPool {
	if mode == "" {
		mode = RoutingFailover
	} else {
		mode = strings.ToLower(mode)
		if mode != RoutingRoundRobin && mode != RoutingFailover {
			mode = RoutingFailover
		}
	}

	pool := &AccountPool{
		accounts:   make([]*AccountStatus, 0),
		accountMap: make(map[string]*AccountStatus),
		mode:       mode,
	}

	pool.ReloadFromStore(store)
	return pool
}

func (p *AccountPool) ReloadFromStore(store *MultiCredentialStore) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.accounts = make([]*AccountStatus, 0)
	p.accountMap = make(map[string]*AccountStatus)
	p.currentIndex = 0

	if store != nil && len(store.Accounts) > 0 {
		// If active account exists, ensure it is first in the list
		if activeCred, ok := store.Accounts[store.Active]; ok {
			status := &AccountStatus{
				Name:       store.Active,
				Credential: activeCred,
			}
			p.accounts = append(p.accounts, status)
			p.accountMap[store.Active] = status
		}

		for name, cred := range store.Accounts {
			if name == store.Active {
				continue
			}
			status := &AccountStatus{
				Name:       name,
				Credential: cred,
			}
			p.accounts = append(p.accounts, status)
			p.accountMap[name] = status
		}
	}
}

func (p *AccountPool) SetActiveAccount(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for idx, acc := range p.accounts {
		if acc.Name == name {
			if idx > 0 {
				// Move to head of pool list and set index to 0
				p.accounts[0], p.accounts[idx] = p.accounts[idx], p.accounts[0]
			}
			p.currentIndex = 0
			return
		}
	}
}

func (p *AccountPool) TotalCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.accounts)
}

func (p *AccountPool) AvailableCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, a := range p.accounts {
		if a.IsAvailable() {
			count++
		}
	}
	return count
}

func (p *AccountPool) GetNextAvailable() *AccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.accounts)
	if n == 0 {
		return nil
	}

	if p.mode == RoutingRoundRobin {
		for i := 0; i < n; i++ {
			idx := (p.currentIndex + i) % n
			acc := p.accounts[idx]
			if acc.IsAvailable() {
				p.currentIndex = (idx + 1) % n
				acc.LastUsed = time.Now()
				return acc
			}
		}
		return nil
	}

	// Default: Failover mode
	// First check current index
	if p.currentIndex < n && p.accounts[p.currentIndex].IsAvailable() {
		acc := p.accounts[p.currentIndex]
		acc.LastUsed = time.Now()
		return acc
	}

	// Current is exhausted, search next available
	for i := 0; i < n; i++ {
		idx := (p.currentIndex + i) % n
		acc := p.accounts[idx]
		if acc.IsAvailable() {
			p.currentIndex = idx
			acc.LastUsed = time.Now()
			return acc
		}
	}

	return nil
}

func (p *AccountPool) FailoverNext(failedAccountName string) *AccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.accounts)
	if n <= 1 {
		return nil
	}

	for i := 1; i < n; i++ {
		idx := (p.currentIndex + i) % n
		acc := p.accounts[idx]
		if acc.Name != failedAccountName && acc.IsAvailable() {
			p.currentIndex = idx
			acc.LastUsed = time.Now()
			return acc
		}
	}

	return nil
}

func (p *AccountPool) MarkExhausted(name string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if duration <= 0 {
		duration = DefaultExhaustedCooldown
	}

	if acc, ok := p.accountMap[name]; ok {
		acc.ExhaustedUntil = time.Now().Add(duration)
		acc.FailureCount++
		log.Printf("[pool] Account %q marked exhausted until %s (cooldown: %v)", name, acc.ExhaustedUntil.Format("15:04:05"), duration)
	}
}

func (p *AccountPool) ResetExhausted(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if acc, ok := p.accountMap[name]; ok {
		acc.ExhaustedUntil = time.Time{}
		acc.FailureCount = 0
	}
}

func (p *AccountPool) All() []*AccountStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	cp := make([]*AccountStatus, len(p.accounts))
	copy(cp, p.accounts)
	return cp
}

func (p *AccountPool) Mode() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *AccountPool) String() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return fmt.Sprintf("AccountPool[total=%d, available=%d, mode=%s]", len(p.accounts), p.AvailableCount(), p.mode)
}
