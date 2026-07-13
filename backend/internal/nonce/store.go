// Package nonce provides a single-use SIWE nonce store with TTL.
// MemStore is in-memory (single instance / tests); PgStore is Postgres-backed
// for multi-instance deployments (a nonce issued by one instance must be
// consumable by any other).
package nonce

import (
	"sync"
	"time"
)

// Store is a single-use, TTL'd nonce store keyed by address.
// The only write entry point is SetIfFree — callers that need SIWE
// should always use it to prevent one party from clobbering another's
// pending nonce.
type Store interface {
	SetIfFree(address, nonce string, ttl time.Duration) bool
	GetDel(address string) (string, bool)
}

type record struct {
	value     string
	expiresAt time.Time
}

// MemStore is a thread-safe, in-memory single-use nonce store.
type MemStore struct {
	mu      sync.Mutex
	entries map[string]record // address → record
}

// New creates an in-memory MemStore and starts background TTL cleanup.
func New() *MemStore {
	s := &MemStore{entries: make(map[string]record)}
	go s.cleanup()
	return s
}

// SetIfFree stores nonce only when no live nonce already exists. Returns
// true on insert, false when a live nonce is in place (caller should 429).
func (s *MemStore) SetIfFree(address, nonce string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ex, ok := s.entries[address]; ok && time.Now().Before(ex.expiresAt) {
		return false
	}
	s.entries[address] = record{value: nonce, expiresAt: time.Now().Add(ttl)}
	return true
}

// GetDel atomically retrieves and deletes the nonce (single-use).
// Returns ("", false) if not found or expired.
func (s *MemStore) GetDel(address string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.entries[address]
	if !ok || time.Now().After(r.expiresAt) {
		delete(s.entries, address)
		return "", false
	}
	delete(s.entries, address)
	return r.value, true
}

func (s *MemStore) cleanup() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for k, r := range s.entries {
			if now.After(r.expiresAt) {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}
