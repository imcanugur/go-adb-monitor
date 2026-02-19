package pool

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestPool_ConcurrencyLimit(t *testing.T) {
	p := New(3, testLogger())

	var maxConcurrent atomic.Int32
	var current atomic.Int32

	ctx := context.Background()

	for i := 0; i < 10; i++ {
		err := p.Submit(ctx, Task{
			Name: "task",
			Fn: func(ctx context.Context) error {
				n := current.Add(1)
				for {
					prev := maxConcurrent.Load()
					if n <= prev {
						break
					}
					if maxConcurrent.CompareAndSwap(prev, n) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				current.Add(-1)
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	p.Wait()

	if max := maxConcurrent.Load(); max > 3 {
		t.Errorf("max concurrent = %d, want <= 3", max)
	}
}

func TestPool_ContextCancellation(t *testing.T) {
	p := New(1, testLogger())

	// Fill the single worker slot.
	ctx, cancel := context.WithCancel(context.Background())

	blocker := make(chan struct{})
	_ = p.Submit(ctx, Task{
		Name: "blocker",
		Fn: func(ctx context.Context) error {
			<-blocker
			return nil
		},
	})

	// Now submit another that will block on the semaphore.
	cancelCtx, cancelFn := context.WithCancel(ctx)
	cancelFn() // Cancel immediately.

	err := p.Submit(cancelCtx, Task{
		Name: "blocked",
		Fn: func(ctx context.Context) error {
			return nil
		},
	})

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	close(blocker)
	cancel()
	p.Wait()
}

func TestPool_ActiveCount(t *testing.T) {
	p := New(10, testLogger())
	if p.ActiveCount() != 0 {
		t.Errorf("ActiveCount should be 0 initially, got %d", p.ActiveCount())
	}
	if p.MaxWorkers() != 10 {
		t.Errorf("MaxWorkers should be 10, got %d", p.MaxWorkers())
	}
}
