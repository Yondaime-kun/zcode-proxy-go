package auth

import (
	"testing"
)

func TestPoolSetActiveAccountAndReload(t *testing.T) {
	store := &MultiCredentialStore{
		Active: "acc1",
		Accounts: map[string]*Credential{
			"acc1": {ApiKey: "key1", Provider: "zai"},
			"acc2": {ApiKey: "key2", Provider: "zai"},
		},
	}

	pool := NewAccountPool(store, RoutingFailover)
	if pool.TotalCount() != 2 {
		t.Fatalf("expected 2 accounts, got %d", pool.TotalCount())
	}

	first := pool.GetNextAvailable()
	if first == nil || first.Name != "acc1" {
		t.Fatalf("expected acc1 as first available, got %v", first)
	}

	// Switch active account to acc2
	store.Active = "acc2"
	pool.ReloadFromStore(store)

	next := pool.GetNextAvailable()
	if next == nil || next.Name != "acc2" {
		t.Fatalf("expected acc2 as active available after reload, got %v", next)
	}

	// Test SetActiveAccount directly
	pool.SetActiveAccount("acc1")
	back := pool.GetNextAvailable()
	if back == nil || back.Name != "acc1" {
		t.Fatalf("expected acc1 as active available after SetActiveAccount, got %v", back)
	}
}
