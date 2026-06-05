package main

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Regression: TODOS.md "ensureInstance 持全局锁跨阻塞 spawn" (P2, 2026-06-04).
// ensureInstance now releases r.mu across the up-to-startTimeout spawn and uses
// an in-flight marker (r.starting) to single-flight concurrent (re)starts of the
// same DNS group. This test proves the marker holds: N concurrent checks for the
// same group must spawn exactly one process and share one generation, never a
// duplicate (which a naive lock-release would allow).
func TestEnsureInstanceSingleFlightsConcurrentStarts(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "ok")
	defer teardown()

	var spawns int64
	fake := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		atomic.AddInt64(&spawns, 1)
		return fake(ctx, name, args...)
	}

	nodes := fakeNodes(1, 2, 3)
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			outcome := runner.Check(nodes, time.Second)
			if !outcome.EngineAvailable {
				t.Errorf("engine should be available, got %+v", outcome)
				return
			}
			for _, node := range nodes {
				if got := outcome.Results[node.ID]; got.Status != "online" {
					t.Errorf("node %d = %+v, want online", node.ID, got)
				}
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&spawns); got != 1 {
		t.Fatalf("spawned %d processes for one group, want 1 (single-flight broken)", got)
	}
	gen, ok := instanceGeneration(runner, "")
	if !ok {
		t.Fatal("expected one persistent instance after concurrent checks")
	}
	if gen != 1 {
		t.Fatalf("generation = %d, want 1 (only one real start)", gen)
	}
}

// Regression: with the spawn moved out of the lock, reads (hasReadyInstance),
// reaps (ReapAbsent) and Close must interleave freely with in-flight starts
// without deadlocking or racing. Run under -race; a deadlock trips the test
// timeout. ReapAbsent(nil) forces stop/restart churn so the stop-concurrent-with-
// start path is exercised.
func TestEnsureInstanceConcurrentReadsReapsNoDeadlock(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "ok")
	defer teardown()

	nodes := fakeNodes(1, 2)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				runner.Check(nodes, time.Second)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = runner.hasReadyInstance()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		churn := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if churn {
				runner.ReapAbsent(nil) // reap everything -> forces a restart
			} else {
				runner.ReapAbsent(nodes) // keep the active group
			}
			churn = !churn
			time.Sleep(15 * time.Millisecond)
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Close while the runner is warm must not hang or race.
	if err := runner.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}
