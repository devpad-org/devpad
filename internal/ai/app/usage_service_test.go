package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

type fakeUsageRepo struct {
	mu       sync.Mutex
	records  []domain.UsageRecord
	slots    []domain.UsageSlot
	userID   int64
	from, to time.Time
}

func (r *fakeUsageRepo) RecordUsage(_ context.Context, rec domain.UsageRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}

func (r *fakeUsageRepo) ListUsageSlots(_ context.Context, userID int64, from, to time.Time) ([]domain.UsageSlot, error) {
	r.userID, r.from, r.to = userID, from, to
	var slots []domain.UsageSlot
	for _, s := range r.slots {
		if !s.Start.Before(from) && s.Start.Before(to) {
			slots = append(slots, s)
		}
	}
	return slots, nil
}

type fakeModelFinder map[string]string

func (f fakeModelFinder) FindModel(id string) (domain.Model, error) {
	name, ok := f[id]
	if !ok {
		return domain.Model{}, domain.ErrModelNotFound
	}
	return domain.Model{ID: id, Name: name}, nil
}

func TestUsageReportCountsPeriodsInTheViewersZone(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	// 18:30 UTC on the 28th is midnight on the 29th in Kolkata, and still
	// the afternoon of the 28th in New York.
	late := time.Date(2026, 10, 28, 18, 30, 0, 0, time.UTC)
	repo := &fakeUsageRepo{slots: []domain.UsageSlot{
		{Start: late.Add(-15 * time.Minute), ProjectID: 1, ProjectName: "shop", Model: "opus", Totals: domain.UsageTotals{Requests: 1, CostUSD: 1}},
		{Start: late, ProjectID: 1, ProjectName: "shop", Model: "opus", Totals: domain.UsageTotals{Requests: 1, CostUSD: 2}},
		{Start: late, ProjectID: 2, ProjectName: "blog", ProjectDeleted: true, Model: "local", Totals: domain.UsageTotals{Requests: 3, UnpricedRequests: 3, InputTokens: 90}},
	}}
	svc := NewUsageService(repo, fakeModelFinder{"opus": "Opus 5.5"})
	day := func(loc *time.Location, d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, loc) }

	report, err := svc.Report(context.Background(), domain.UsageQuery{UserID: 7, Period: domain.UsageDay, From: day(kolkata, 28), To: day(kolkata, 29), Location: kolkata})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if repo.userID != 7 || !repo.from.Equal(day(kolkata, 28)) || !repo.to.Equal(day(kolkata, 30)) {
		t.Fatalf("listed user %d from %s to %s", repo.userID, repo.from, repo.to)
	}
	if want := []string{"2026-10-28", "2026-10-29"}; !reflect.DeepEqual(report.Periods, want) {
		t.Fatalf("periods = %v, want %v", report.Periods, want)
	}
	wantRows := []domain.UsageReportRow{
		{Period: "2026-10-28", ProjectID: 1, Model: "opus", UsageTotals: domain.UsageTotals{Requests: 1, CostUSD: 1}},
		{Period: "2026-10-29", ProjectID: 1, Model: "opus", UsageTotals: domain.UsageTotals{Requests: 1, CostUSD: 2}},
		{Period: "2026-10-29", ProjectID: 2, Model: "local", UsageTotals: domain.UsageTotals{Requests: 3, UnpricedRequests: 3, InputTokens: 90}},
	}
	if !reflect.DeepEqual(report.Rows, wantRows) {
		t.Fatalf("rows = %+v, want %+v", report.Rows, wantRows)
	}
	if want := []domain.UsageProject{{ID: 1, Name: "shop"}, {ID: 2, Name: "blog", Deleted: true}}; !reflect.DeepEqual(report.Projects, want) {
		t.Fatalf("projects = %+v, want %+v", report.Projects, want)
	}
	// An unknown model is shown by its ID.
	if want := []domain.UsageModel{{ID: "opus", Name: "Opus 5.5"}, {ID: "local", Name: "local"}}; !reflect.DeepEqual(report.Models, want) {
		t.Fatalf("models = %+v, want %+v", report.Models, want)
	}
	if report.Totals != (domain.UsageTotals{Requests: 5, UnpricedRequests: 3, InputTokens: 90, CostUSD: 3}) {
		t.Fatalf("totals = %+v", report.Totals)
	}

	report, err = svc.Report(context.Background(), domain.UsageQuery{Period: domain.UsageDay, From: day(newYork, 28), To: day(newYork, 29), Location: newYork})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, row := range report.Rows {
		if row.Period != "2026-10-28" {
			t.Fatalf("expected every request on the 28th in New York, got %+v", report.Rows)
		}
	}
}

func TestUsageReportWeeksStartOnMondayAndMonthsOnTheFirst(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, nil)
	// The 29th of October 2026 is a Thursday.
	thursday := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)

	report, err := svc.Report(context.Background(), domain.UsageQuery{Period: domain.UsageWeek, From: thursday.AddDate(0, 0, -14), To: thursday})
	if err != nil {
		t.Fatalf("week report: %v", err)
	}
	if want := []string{"2026-10-12", "2026-10-19", "2026-10-26"}; !reflect.DeepEqual(report.Periods, want) {
		t.Fatalf("weeks = %v, want %v", report.Periods, want)
	}
	if want := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC); !repo.to.Equal(want) {
		t.Fatalf("listed up to %s, want the end of the last week %s", repo.to, want)
	}

	report, err = svc.Report(context.Background(), domain.UsageQuery{Period: domain.UsageMonth, From: thursday.AddDate(0, -2, 0), To: thursday})
	if err != nil {
		t.Fatalf("month report: %v", err)
	}
	if want := []string{"2026-08-01", "2026-09-01", "2026-10-01"}; !reflect.DeepEqual(report.Periods, want) {
		t.Fatalf("months = %v, want %v", report.Periods, want)
	}
}

func TestUsageReportRefusesBadQueries(t *testing.T) {
	svc := NewUsageService(&fakeUsageRepo{}, nil)
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for name, q := range map[string]domain.UsageQuery{
		"unknown period": {Period: "year", From: day, To: day},
		"backwards":      {Period: domain.UsageDay, From: day, To: day.AddDate(0, 0, -1)},
		"too long":       {Period: domain.UsageDay, From: day.AddDate(-2, 0, 0), To: day},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Report(context.Background(), q); !errors.Is(err, domain.ErrInvalidUsageQuery) {
				t.Fatalf("expected ErrInvalidUsageQuery, got %v", err)
			}
		})
	}
}

func TestRecordUsageKeepsRecordingAfterTheReaderLeaves(t *testing.T) {
	repo := &fakeUsageRepo{}
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan domain.ClientEvent)
	out := recordUsage(ctx, repo, domain.UsageRecord{UserID: 1, WorkspaceID: 2, RunID: 3, Model: "opus"}, in)

	cost := 0.5
	go func() {
		defer close(in)
		in <- domain.ClientEvent{TextDelta: "hi"}
		in <- domain.ClientEvent{Usage: &domain.Usage{InputTokens: 10, CostUSD: &cost}}
		// The reader has gone by now; the provider still billed this one.
		in <- domain.ClientEvent{Usage: &domain.Usage{OutputTokens: 5}}
		in <- domain.ClientEvent{Done: true}
	}()
	if event := <-out; event.TextDelta != "hi" {
		t.Fatalf("first event = %+v", event)
	}
	if event := <-out; event.Usage == nil {
		t.Fatalf("second event = %+v", event)
	}
	cancel()
	for range out {
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.records) != 2 {
		t.Fatalf("records = %+v, want both requests", repo.records)
	}
	first, second := repo.records[0], repo.records[1]
	if first.UserID != 1 || first.WorkspaceID != 2 || first.RunID != 3 || first.Model != "opus" || first.Usage.InputTokens != 10 || first.At.IsZero() {
		t.Fatalf("first record = %+v", first)
	}
	if second.Usage.OutputTokens != 5 || second.Usage.CostUSD != nil {
		t.Fatalf("second record = %+v", second)
	}
}
