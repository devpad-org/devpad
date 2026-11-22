package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSlotsGoToTheOldestPipelineFirst(t *testing.T) {
	slots := newJobSlots(2)
	ctx := context.Background()
	a, _ := slots.acquire(ctx, 1, 10, 0)
	b, _ := slots.acquire(ctx, 1, 10, 1)
	// Another project's slots are its own.
	other, err := slots.acquire(ctx, 2, 11, 0)
	if err != nil {
		t.Fatal(err)
	}
	other()

	// Each waiter says it was granted a slot before freeing it, so the
	// order said is the order granted; done is once all have freed theirs.
	order := make(chan string, 3)
	var done sync.WaitGroup
	wait := func(name string, pipeline int64, position int) {
		done.Add(1)
		go func() {
			defer done.Done()
			release, err := slots.acquire(ctx, 1, pipeline, position)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				return
			}
			order <- name
			release()
		}()
	}
	wait("newer", 12, 0)
	wait("older's second", 10, 3)
	wait("older's first", 10, 2)
	awaitWaiters(t, slots, 1, 3)

	a()
	a() // freeing twice frees once
	if got := <-order; got != "older's first" {
		t.Fatalf("first freed slot went to %s", got)
	}
	if got := <-order; got != "older's second" {
		t.Fatalf("second went to %s", got)
	}
	if got := <-order; got != "newer" {
		t.Fatalf("third went to %s", got)
	}
	done.Wait()
	if n := slots.running(1); n != 1 {
		t.Fatalf("%d slots in use, want 1", n)
	}
	b()
	if n := slots.running(1); n != 0 || len(slots.projects) != 0 {
		t.Fatalf("%d slots in use, %d projects kept", n, len(slots.projects))
	}
}

func TestSlotWaitEndsWithItsContext(t *testing.T) {
	slots := newJobSlots(1)
	hold, _ := slots.acquire(context.Background(), 1, 1, 0)
	why := errors.New("cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error)
	go func() {
		_, err := slots.acquire(ctx, 1, 2, 0)
		done <- err
	}()
	awaitWaiters(t, slots, 1, 1)
	cancel(why)
	if err := <-done; !errors.Is(err, why) {
		t.Fatalf("got %v, want the cause", err)
	}
	// The waiter that gave up is gone: the slot frees for good.
	hold()
	if n := slots.running(1); n != 0 {
		t.Fatalf("%d slots in use", n)
	}
	again, err := slots.acquire(context.Background(), 1, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

// running reports how many of the project's slots are in use.
func (s *jobSlots) running(projectID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.projects[projectID]; p != nil {
		return p.used
	}
	return 0
}

// waiters reports how many jobs of the project wait for a slot.
func (s *jobSlots) waiters(projectID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.projects[projectID]; p != nil {
		return len(p.waiting)
	}
	return 0
}

// awaitWaiters waits until n jobs of the project wait for a slot.
func awaitWaiters(t *testing.T, s *jobSlots, projectID int64, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.waiters(projectID) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs wait for a slot, want %d", s.waiters(projectID), n)
		}
		time.Sleep(time.Millisecond)
	}
}
