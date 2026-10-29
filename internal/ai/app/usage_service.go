package app

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// maxUsagePeriods bounds how many periods one report may cover.
const maxUsagePeriods = 400

// UsageRepository keeps the ledger of provider requests.
type UsageRepository interface {
	RecordUsage(ctx context.Context, rec domain.UsageRecord) error
	ListUsageSlots(ctx context.Context, userID int64, from, to time.Time) ([]domain.UsageSlot, error)
}

// UsageRecorder adds provider requests to the usage ledger.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, rec domain.UsageRecord) error
}

// ModelFinder looks up a model by ID.
type ModelFinder interface {
	FindModel(modelID string) (domain.Model, error)
}

// UsageService records what provider requests cost and reports it.
type UsageService interface {
	UsageRecorder
	// Report refuses an unknown period, or a range that is backwards or
	// over maxUsagePeriods periods long, with domain.ErrInvalidUsageQuery.
	Report(ctx context.Context, q domain.UsageQuery) (*domain.UsageReport, error)
}

type usageService struct {
	repo   UsageRepository
	models ModelFinder
}

// NewUsageService creates the app service for the usage ledger. models names
// the models in reports; a model it cannot find is shown by its ID.
func NewUsageService(repo UsageRepository, models ModelFinder) UsageService {
	return &usageService{repo: repo, models: models}
}

func (s *usageService) RecordUsage(ctx context.Context, rec domain.UsageRecord) error {
	if err := s.repo.RecordUsage(ctx, rec); err != nil {
		return fmt.Errorf("recording usage: %w", err)
	}
	return nil
}

func (s *usageService) Report(ctx context.Context, q domain.UsageQuery) (*domain.UsageReport, error) {
	loc := q.Location
	if loc == nil {
		loc = time.UTC
	}
	switch q.Period {
	case domain.UsageDay, domain.UsageWeek, domain.UsageMonth:
	default:
		return nil, fmt.Errorf("%w: unknown period %q", domain.ErrInvalidUsageQuery, q.Period)
	}
	start := periodStart(q.Period, q.From, loc)
	last := periodStart(q.Period, q.To, loc)
	if last.Before(start) {
		return nil, fmt.Errorf("%w: the range ends before it starts", domain.ErrInvalidUsageQuery)
	}

	report := &domain.UsageReport{Period: q.Period, Periods: []string{}, Projects: []domain.UsageProject{}, Models: []domain.UsageModel{}, Rows: []domain.UsageReportRow{}}
	end := start
	for !end.After(last) {
		if len(report.Periods) == maxUsagePeriods {
			return nil, fmt.Errorf("%w: the range is over %d periods long", domain.ErrInvalidUsageQuery, maxUsagePeriods)
		}
		report.Periods = append(report.Periods, end.Format(time.DateOnly))
		end = nextPeriod(q.Period, end)
	}

	slots, err := s.repo.ListUsageSlots(ctx, q.UserID, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing usage: %w", err)
	}

	type rowKey struct {
		period    string
		projectID int64
		model     string
	}
	rows := map[rowKey]*domain.UsageReportRow{}
	projects := map[int64]domain.UsageProject{}
	projectCost := map[int64]float64{}
	modelCost := map[string]float64{}
	for _, slot := range slots {
		key := rowKey{periodStart(q.Period, slot.Start, loc).Format(time.DateOnly), slot.ProjectID, slot.Model}
		row := rows[key]
		if row == nil {
			row = &domain.UsageReportRow{Period: key.period, ProjectID: key.projectID, Model: key.model}
			rows[key] = row
		}
		row.Add(slot.Totals)
		report.Totals.Add(slot.Totals)
		projects[slot.ProjectID] = domain.UsageProject{ID: slot.ProjectID, Name: slot.ProjectName, Deleted: slot.ProjectDeleted}
		projectCost[slot.ProjectID] += slot.Totals.CostUSD
		modelCost[slot.Model] += slot.Totals.CostUSD
	}

	for _, row := range rows {
		report.Rows = append(report.Rows, *row)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		a, b := report.Rows[i], report.Rows[j]
		if a.Period != b.Period {
			return a.Period < b.Period
		}
		if a.ProjectID != b.ProjectID {
			return a.ProjectID < b.ProjectID
		}
		return a.Model < b.Model
	})
	// Projects and models come most costly first.
	for _, p := range projects {
		report.Projects = append(report.Projects, p)
	}
	sort.Slice(report.Projects, func(i, j int) bool {
		a, b := report.Projects[i], report.Projects[j]
		if projectCost[a.ID] != projectCost[b.ID] {
			return projectCost[a.ID] > projectCost[b.ID]
		}
		return a.ID < b.ID
	})
	for id := range modelCost {
		report.Models = append(report.Models, domain.UsageModel{ID: id, Name: s.modelName(id)})
	}
	sort.Slice(report.Models, func(i, j int) bool {
		a, b := report.Models[i], report.Models[j]
		if modelCost[a.ID] != modelCost[b.ID] {
			return modelCost[a.ID] > modelCost[b.ID]
		}
		return a.ID < b.ID
	})
	return report, nil
}

func (s *usageService) modelName(id string) string {
	if s.models == nil {
		return id
	}
	model, err := s.models.FindModel(id)
	if err != nil || model.Name == "" {
		return id
	}
	return model.Name
}

// periodStart is the start, in loc, of the period that t falls in.
func periodStart(period domain.UsagePeriod, t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	switch period {
	case domain.UsageWeek:
		// Weeks start on Monday.
		offset := (int(t.Weekday()) + 6) % 7
		return time.Date(t.Year(), t.Month(), t.Day()-offset, 0, 0, 0, 0, loc)
	case domain.UsageMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
}

// nextPeriod is the start of the period after the one starting at start.
func nextPeriod(period domain.UsagePeriod, start time.Time) time.Time {
	switch period {
	case domain.UsageWeek:
		return time.Date(start.Year(), start.Month(), start.Day()+7, 0, 0, 0, 0, start.Location())
	case domain.UsageMonth:
		return time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, start.Location())
	default:
		return time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, start.Location())
	}
}

// recordUsage passes a chat stream's events on, adding each provider
// request's usage to the ledger as rec. A request is recorded even after
// whoever reads the stream has gone, since it was still billed.
func recordUsage(ctx context.Context, recorder UsageRecorder, rec domain.UsageRecord, in <-chan domain.ClientEvent) <-chan domain.ClientEvent {
	if recorder == nil {
		return in
	}
	out := make(chan domain.ClientEvent)
	go func() {
		defer close(out)
		reading := true
		for event := range in {
			if event.Usage != nil {
				rec.Usage, rec.At = *event.Usage, time.Now()
				// Losing a request undercounts the ledger but must not stop
				// the chat.
				if err := recorder.RecordUsage(context.Background(), rec); err != nil {
					log.Printf("failed to record usage of %s: %v", rec.Model, err)
				}
			}
			if !reading {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				reading = false
			}
		}
	}()
	return out
}
