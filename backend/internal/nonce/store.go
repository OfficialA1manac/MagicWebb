// Package nonce provides an in-memory SIWE nonce store with TTL.
package nonce

import (
	"sync"
	"time"
)

type record struct {
	value     string
	expiresAt time.Time
}

// Store is a thread-safe single-use nonce store.
type Store struct {
	mu      sync.Mutex
	entries map[string]record // address → record
}

// New creates a Store and starts background TTL cleanup.
func New() *Store {
	s := &Store{entries: make(map[string]record)}
	go s.cleanup()
	return s
}

// Set stores nonce for address with given TTL.
func (s *Store) Set(address, nonce string, ttl time.Duration) {
	s.mu.Lock()
	s.entries[address] = record{value: nonce, expiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()
}

// GetDel atomically retrieves and deletes the nonce (single-use).
// Returns ("", false) if not found or expired.
func (s *Store) GetDel(address string) (string, bool) {
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

func (s *Store) cleanup() {
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
