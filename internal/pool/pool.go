package pool

import (
	"context"
	"log/slog"
	"sync"
)

// Task represents a unit of work to run in the pool.
type Task struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Pool manages a bounded set of worker goroutines for device operations.
// It ensures no more than maxWorkers tasks run concurrently,
// critical for handling 150+ devices without exhausting OS resources.
type Pool struct {
	log        *slog.Logger
	maxWorkers int
	sem        chan struct{}
	wg         sync.WaitGroup
}

// New creates a pool with the given concurrency limit.
func New(maxWorkers int, log *slog.Logger) *Pool {
	if maxWorkers <= 0 {
		maxWorkers = 50
	}
	return &Pool{
		log:        log.With("component", "pool"),
		maxWorkers: maxWorkers,
		sem:        make(chan struct{}, maxWorkers),
	}
}

// Submit schedules a task for execution. It blocks if all workers are busy.
// The task respects the provided context for cancellation.
func (p *Pool) Submit(ctx context.Context, task Task) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.sem <- struct{}{}:
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()

		p.log.Debug("task started", "name", task.Name)

		if err := task.Fn(ctx); err != nil {
			if ctx.Err() == nil {
				p.log.Warn("task failed", "name", task.Name, "error", err)
			}
		} else {
			p.log.Debug("task completed", "name", task.Name)
		}
	}()

	return nil
}

// Wait blocks until all submitted tasks complete.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// ActiveCount returns the number of currently running tasks.
func (p *Pool) ActiveCount() int {
	return len(p.sem)
}

// MaxWorkers returns the pool's concurrency limit.
func (p *Pool) MaxWorkers() int {
	return p.maxWorkers
}
