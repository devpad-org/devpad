package domain

import "testing"

func TestBudgetWithinSpendsFromItsParentToo(t *testing.T) {
	var taskSpent, reviewSpent float64
	task := NewBudget(10, 2, func(spent float64) { taskSpent = spent }).Owned("task #3")
	review := NewBudget(4, 0, func(spent float64) { reviewSpent = spent }).Owned("this review").Within(task)

	review.Spend(3)
	if taskSpent != 5 || reviewSpent != 3 {
		t.Fatalf("expected the review's spend recorded on both, got task $%v, review $%v", taskSpent, reviewSpent)
	}
	if review.Exhausted() || task.Exhausted() {
		t.Fatal("neither budget is spent yet")
	}

	// The review's own limit stops it, but not the task.
	review.Spend(1.5)
	if !review.Exhausted() || task.Exhausted() {
		t.Fatalf("expected only the review's budget spent: review %v, task %v", review.Exhausted(), task.Exhausted())
	}
	if got, want := review.ExhaustedMessage(), "Budget reached: this review has spent $4.50 of its $4.00 budget"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}

	// Once the task's budget is spent, every review within it stops, and is
	// told it is the task's budget that ran out, which raising only the
	// review's would not cure.
	next := NewBudget(4, 0, nil).Owned("this review").Within(task)
	task.Spend(4)
	if !next.Exhausted() {
		t.Fatal("expected a review within a spent task budget to stop")
	}
	review.SetLimit(100)
	if got, want := review.ExhaustedMessage(), "Budget reached: task #3 has spent $10.50 of its $10.00 budget"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	task.SetLimit(20)
	if next.Exhausted() || review.Exhausted() {
		t.Fatal("expected raising the task's budget to let its reviews go on")
	}
}

func TestBudgetWithoutLimitOrParent(t *testing.T) {
	var nilBudget *Budget
	nilBudget.Spend(5)
	if nilBudget.Exhausted() {
		t.Fatal("a nil budget has no limit")
	}
	b := NewBudget(0, 0, nil).Within(nil)
	b.Spend(1000)
	if b.Exhausted() {
		t.Fatal("a zero limit means no limit")
	}
	b.SetLimit(1)
	if got, want := b.ExhaustedMessage(), "Budget reached: the agent has spent $1000.00 of its $1.00 budget"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}
