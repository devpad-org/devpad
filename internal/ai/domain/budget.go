package domain

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// ErrInvalidBudget refuses a budget that is negative or not a number.
var ErrInvalidBudget = errors.New("a budget must be zero, for no limit, or a positive amount in USD")

// ValidateBudget checks a limit in USD, where zero means no limit.
func ValidateBudget(limitUSD float64) error {
	if limitUSD < 0 || math.IsNaN(limitUSD) || math.IsInf(limitUSD, 0) {
		return ErrInvalidBudget
	}
	return nil
}

// Budget is what a run, and the sub-agents it starts, may spend on priced
// provider requests. The runs of one tree share it, so its limit covers them
// all. Requests to models without known prices cost nothing here, and a nil
// Budget has no limit.
type Budget struct {
	onSpend func(spentUSD float64)

	mu       sync.Mutex
	limitUSD float64
	spentUSD float64
}

// NewBudget returns a budget of limitUSD, of which spentUSD is already
// spent. onSpend, which may be nil, is called after each priced request with
// the total spent so far; calls for requests spent at once can arrive out of
// order. A limit of zero or less means no limit, so the budget only counts.
func NewBudget(limitUSD, spentUSD float64, onSpend func(spentUSD float64)) *Budget {
	return &Budget{limitUSD: limitUSD, spentUSD: spentUSD, onSpend: onSpend}
}

// SetLimit replaces the limit, for a budget raised or lowered while its runs
// go on. A limit of zero or less means no limit.
func (b *Budget) SetLimit(limitUSD float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limitUSD = limitUSD
}

// Spend records the cost of a request.
func (b *Budget) Spend(costUSD float64) {
	if b == nil || costUSD <= 0 {
		return
	}
	b.mu.Lock()
	b.spentUSD += costUSD
	spent := b.spentUSD
	b.mu.Unlock()
	if b.onSpend != nil {
		b.onSpend(spent)
	}
}

// Exhausted reports whether the budget has a limit and has spent it.
func (b *Budget) Exhausted() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limitUSD > 0 && b.spentUSD >= b.limitUSD
}

// ExhaustedMessage is the error of a run stopped because its budget is spent.
func (b *Budget) ExhaustedMessage() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("the agent's budget of $%.2f is spent ($%.2f)", b.limitUSD, b.spentUSD)
}

// BudgetDefaults are the limits an admin set on what agents may spend, in
// USD. Zero means no limit.
type BudgetDefaults struct {
	// TaskUSD is the budget a new task starts with, covering every round of
	// its agent and the sub-agents they start.
	TaskUSD float64 `json:"taskUsd"`
	// ReviewUSD covers one review agent and its sub-agents.
	ReviewUSD float64 `json:"reviewUsd"`
	// AssistantUSD covers one assistant run, a reply to one message, and its
	// sub-agents.
	AssistantUSD float64 `json:"assistantUsd"`
}
