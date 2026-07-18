// Package checks is the in-memory backing for Atlantis-style check detail
// pages (docs/serve-design.md §Check details): each commit status can carry
// a target_url pointing at GET /checks/{id} on this server. Lifetime
// matches Atlantis job logs — a restart clears the store; the durable
// record stays in the bot comment and the index.
//
// @index In-memory store backing Atlantis-style commit-status detail pages addressed by random capability ids.
package checks

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Check states, mirroring the GitHub status they annotate.
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateFailure = "failure"
	StateError   = "error"
)

// Check is one renderable detail page.
type Check struct {
	Title     string
	State     string
	Body      []string // pre-rendered lines (markdown-ish plain text)
	UpdatedAt time.Time
}

// Store holds checks keyed by a stable logical key (e.g. repo#pr) and
// addressed publicly by a random id (capability URL). Bounded FIFO.
type Store struct {
	mu     sync.Mutex
	cap    int
	byID   map[string]*Check
	idFor  map[string]string // logical key → id
	keyFor map[string]string // id → logical key (for eviction)
	order  []string          // logical keys, insertion order
}

// NewStore builds a store evicting the oldest key beyond cap entries.
func NewStore(capacity int) *Store {
	return &Store{
		cap:    capacity,
		byID:   map[string]*Check{},
		idFor:  map[string]string{},
		keyFor: map[string]string{},
	}
}

// Upsert creates or replaces the check for key and returns its public id,
// which is stable while the key is resident (pending → final share a URL).
//
// @intent create or replace a check detail page and return the stable capability URL id for its logical key
// @domainRule the same logical key keeps the same id while resident, so a pending status and its final result share one URL
// @domainRule evicts the oldest key beyond the capacity bound (FIFO); pages are non-durable and cleared on restart
// @mutates the in-memory check store
func (s *Store) Upsert(key, title, state string, body []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.idFor[key]
	if !ok {
		id = newID()
		s.idFor[key] = id
		s.keyFor[id] = key
		s.order = append(s.order, key)
		if len(s.order) > s.cap {
			oldest := s.order[0]
			s.order = s.order[1:]
			if oldID, ok := s.idFor[oldest]; ok {
				delete(s.byID, oldID)
				delete(s.keyFor, oldID)
				delete(s.idFor, oldest)
			}
		}
	}
	s.byID[id] = &Check{Title: title, State: state, Body: body, UpdatedAt: time.Now()}
	return id
}

// Get returns the check for a public id.
func (s *Store) Get(id string) (Check, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byID[id]
	if !ok {
		return Check{}, false
	}
	return *c, true
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable initialization state
	}
	return hex.EncodeToString(b)
}
