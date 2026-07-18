package checks

import (
	"strings"
	"testing"
)

func TestUpsertAndGet(t *testing.T) {
	s := NewStore(8)
	id := s.Upsert("repo#1", "Context check", StateFailure, []string{"missing-why: add a reason"})
	if len(id) < 16 {
		t.Errorf("id too short to be a capability url: %q", id)
	}
	c, ok := s.Get(id)
	if !ok || c.Title != "Context check" || c.State != StateFailure || len(c.Body) != 1 {
		t.Fatalf("Get = %+v ok=%v", c, ok)
	}

	// same key → same id, content replaced (pending → final in place)
	id2 := s.Upsert("repo#1", "Context check", StateSuccess, []string{"all good"})
	if id2 != id {
		t.Errorf("id changed on upsert: %q vs %q", id, id2)
	}
	c, _ = s.Get(id)
	if c.State != StateSuccess || c.Body[0] != "all good" {
		t.Errorf("not updated in place: %+v", c)
	}

	// different key → different id
	if id3 := s.Upsert("repo#2", "t", StateSuccess, nil); id3 == id {
		t.Error("distinct keys share an id")
	}
}

func TestGetUnknown(t *testing.T) {
	s := NewStore(8)
	if _, ok := s.Get("nope"); ok {
		t.Error("unknown id returned a check")
	}
}

func TestEviction(t *testing.T) {
	s := NewStore(2)
	id1 := s.Upsert("k1", "t", StateSuccess, nil)
	s.Upsert("k2", "t", StateSuccess, nil)
	s.Upsert("k3", "t", StateSuccess, nil) // evicts k1
	if _, ok := s.Get(id1); ok {
		t.Error("oldest check not evicted at cap")
	}
	// updating a resident key must not evict anything
	id3 := s.Upsert("k3", "t", StateFailure, nil)
	if _, ok := s.Get(id3); !ok {
		t.Error("update lost the check")
	}
}

func TestIDsLookRandom(t *testing.T) {
	s := NewStore(8)
	a := s.Upsert("ka", "t", StateSuccess, nil)
	b := s.Upsert("kb", "t", StateSuccess, nil)
	if a == b || strings.Contains(a, "ka") {
		t.Errorf("ids must be random capability tokens: %q %q", a, b)
	}
}
