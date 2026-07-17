package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunsJobs(t *testing.T) {
	var n atomic.Int32
	done := make(chan struct{}, 8)
	q := New(4, 8, func(ctx context.Context, key string) {
		n.Add(1)
		done <- struct{}{}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	for range 3 {
		if !q.Enqueue("repo-a") {
			t.Fatal("enqueue rejected below capacity")
		}
	}
	for range 3 {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("job did not run")
		}
	}
	if n.Load() != 3 {
		t.Errorf("ran %d jobs, want 3", n.Load())
	}
}

func TestSerializesSameKeyParallelizesDifferent(t *testing.T) {
	var mu sync.Mutex
	running := map[string]int{}
	maxSame := 0
	bothActive := make(chan struct{}, 1)

	block := make(chan struct{})
	q := New(4, 16, func(ctx context.Context, key string) {
		mu.Lock()
		running[key]++
		if running[key] > maxSame {
			maxSame = running[key]
		}
		if len(running) == 2 && running["a"] >= 1 && running["b"] >= 1 {
			select {
			case bothActive <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
		<-block
		mu.Lock()
		running[key]--
		if running[key] == 0 {
			delete(running, key)
		}
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	q.Enqueue("a")
	q.Enqueue("a") // must wait for the first "a"
	q.Enqueue("b") // must run concurrently with "a"

	select {
	case <-bothActive:
	case <-time.After(2 * time.Second):
		t.Fatal("different keys did not run in parallel")
	}
	mu.Lock()
	if maxSame > 1 {
		t.Errorf("same key ran %d-way concurrent, want serialized", maxSame)
	}
	mu.Unlock()
	close(block)
}

func TestOverflowRejects(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	q := New(1, 1, func(ctx context.Context, key string) { <-block })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	q.Enqueue("x") // taken by the worker (blocked)
	// fill the buffer, then overflow
	accepted := 0
	for range 5 {
		if q.Enqueue("x") {
			accepted++
		}
	}
	if accepted >= 5 {
		t.Error("queue never rejected despite capacity 1")
	}
}
