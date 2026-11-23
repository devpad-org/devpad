package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlotsGoToTheOldestPipelineFirst(t *testing.T) {
	slots := newJobSlots(2)
	ctx := context.Background()
	a, _ := slots.acquire(ctx, slotClaim{projectID: 1, pipelineID: 10})
	b, _ := slots.acquire(ctx, slotClaim{projectID: 1, pipelineID: 10, position: 1})
	// Another project's slots are its own.
	other, err := slots.acquire(ctx, slotClaim{projectID: 2, pipelineID: 11})
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
			release, err := slots.acquire(ctx, slotClaim{projectID: 1, pipelineID: pipeline, position: position})
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
	hold, _ := slots.acquire(context.Background(), slotClaim{projectID: 1, pipelineID: 1})
	why := errors.New("cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error)
	go func() {
		_, err := slots.acquire(ctx, slotClaim{projectID: 1, pipelineID: 2})
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
	again, err := slots.acquire(context.Background(), slotClaim{projectID: 1, pipelineID: 3})
	if err != nil {
		t.Fatal(err)
	}
	again()
}

// Proposed pipelines' jobs, which a change wrote, wait behind every
// official job, however old they are, and hold one slot at most, so a long
// proposed pipeline keeps official ones from no more than that one slot.
func TestProposedJobsNeverHoldBackOfficialOnes(t *testing.T) {
	slots := newJobSlots(3)
	ctx := context.Background()
	proposed := func(position int) slotClaim {
		return slotClaim{projectID: 1, pipelineID: 1, position: position, proposed: true}
	}
	official := func(pipeline int64, position int) slotClaim {
		return slotClaim{projectID: 1, pipelineID: pipeline, position: position}
	}
	// The oldest pipeline, a proposed one, takes one slot and no more.
	first, _ := slots.acquire(ctx, proposed(0))
	granted := make(chan string, 8)
	hold := map[string]chan struct{}{}
	for _, name := range []string{"proposed 1", "proposed 2", "official 0", "official 1", "official 2"} {
		hold[name] = make(chan struct{})
	}
	var done sync.WaitGroup
	wait := func(name string, c slotClaim) {
		done.Add(1)
		go func() {
			defer done.Done()
			release, err := slots.acquire(ctx, c)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				return
			}
			granted <- name
			<-hold[name]
			release()
		}()
	}
	wait("proposed 1", proposed(1))
	wait("proposed 2", proposed(2))
	awaitWaiters(t, slots, 1, 2)
	if n := slots.running(1); n != 1 {
		t.Fatalf("proposed jobs hold %d slots, want 1", n)
	}
	// A newer official pipeline gets the two slots left at once.
	wait("official 0", official(2, 0))
	wait("official 1", official(2, 1))
	for range 2 {
		if got := <-granted; !strings.HasPrefix(got, "official") {
			t.Fatalf("%s got a free slot", got)
		}
	}
	// Its third job waits ahead of the older proposed ones, and the
	// proposed slot that frees goes to it.
	wait("official 2", official(2, 2))
	awaitWaiters(t, slots, 1, 3)
	first()
	if got := <-granted; got != "official 2" {
		t.Fatalf("the freed slot went to %s", got)
	}
	// Once official jobs are done, proposed ones go one at a time.
	for _, name := range []string{"official 0", "official 1", "official 2"} {
		close(hold[name])
	}
	if got := <-granted; got != "proposed 1" {
		t.Fatalf("then %s", got)
	}
	select {
	case got := <-granted:
		t.Fatalf("%s ran beside another proposed job", got)
	case <-time.After(20 * time.Millisecond):
	}
	close(hold["proposed 1"])
	if got := <-granted; got != "proposed 2" {
		t.Fatalf("then %s", got)
	}
	close(hold["proposed 2"])
	done.Wait()
	if n := slots.running(1); n != 0 || len(slots.projects) != 0 {
		t.Fatalf("%d slots in use, %d projects kept", n, len(slots.projects))
	}
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
