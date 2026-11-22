package pipeline

import (
	"context"
	"errors"
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

	order := make(chan string, 3)
	wait := func(name string, pipeline int64, position int) {
		go func() {
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
	time.Sleep(10 * time.Millisecond)
	wait("older's second", 10, 3)
	time.Sleep(10 * time.Millisecond)
	wait("older's first", 10, 2)
	time.Sleep(10 * time.Millisecond)

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
	time.Sleep(10 * time.Millisecond)
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
