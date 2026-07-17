// Package queue is the in-memory ingest queue for serve
// (docs/serve-design.md §Async ingestion): bounded, worker-pooled, with
// per-key serialization so one repository never ingests concurrently while
// different repositories proceed in parallel.
//
// Deliberately not durable: a restart drops queued jobs. The ingest cursor
// catches up on the next merge (or a manual `context-diary index`) — the
// same trade Atlantis makes for in-flight operations.
package queue

import (
	"context"
	"sync"
)

// Q is a bounded job queue. A job is identified by its key; the run
// function receives it when a worker picks the job up.
type Q struct {
	jobs    chan string
	run     func(ctx context.Context, key string)
	workers int

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New builds a queue with the given worker count and buffer capacity.
func New(workers, capacity int, run func(ctx context.Context, key string)) *Q {
	return &Q{
		jobs:    make(chan string, capacity),
		run:     run,
		workers: workers,
		locks:   map[string]*sync.Mutex{},
	}
}

// Enqueue adds a job; false when the queue is full (caller surfaces 503).
func (q *Q) Enqueue(key string) bool {
	select {
	case q.jobs <- key:
		return true
	default:
		return false
	}
}

// Start launches the worker pool; workers exit when ctx is cancelled.
func (q *Q) Start(ctx context.Context) {
	for range q.workers {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case key := <-q.jobs:
					lock := q.keyLock(key)
					lock.Lock()
					q.run(ctx, key)
					lock.Unlock()
				}
			}
		}()
	}
}

func (q *Q) keyLock(key string) *sync.Mutex {
	q.mu.Lock()
	defer q.mu.Unlock()
	if l, ok := q.locks[key]; ok {
		return l
	}
	l := &sync.Mutex{}
	q.locks[key] = l
	return l
}
