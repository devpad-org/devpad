package domain

import (
	"errors"
	"time"
)

// ErrInvalidUsageQuery refuses a usage report with an unknown period or a
// range that is backwards or too long.
var ErrInvalidUsageQuery = errors.New("invalid usage query")

// UsageRecord is one provider request's usage, as the ledger keeps it.
type UsageRecord struct {
	UserID int64
	// WorkspaceID is zero for a request made outside any workspace, and
	// RunID for one made outside an agent run.
	WorkspaceID int64
	RunID       int64
	Model       string
	Usage       Usage
	At          time.Time
}

// UsageSlot totals the requests of one project and model in a quarter of an
// hour, the finest grain a report counts in: every time zone's offset is a
// whole number of quarter hours.
type UsageSlot struct {
	Start time.Time
	// ProjectID is zero for requests made outside any project.
	ProjectID      int64
	ProjectName    string
	ProjectDeleted bool
	Model          string
	Totals         UsageTotals
}

// UsageTotals adds up provider requests. CostUSD covers only the priced
// ones; UnpricedRequests counts those whose model had no known prices.
type UsageTotals struct {
	Requests         int64   `json:"requests"`
	UnpricedRequests int64   `json:"unpricedRequests"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// Add adds o to t.
func (t *UsageTotals) Add(o UsageTotals) {
	t.Requests += o.Requests
	t.UnpricedRequests += o.UnpricedRequests
	t.InputTokens += o.InputTokens
	t.OutputTokens += o.OutputTokens
	t.CacheReadTokens += o.CacheReadTokens
	t.CacheWriteTokens += o.CacheWriteTokens
	t.CostUSD += o.CostUSD
}

// UsagePeriod is how long each of a report's periods is.
type UsagePeriod string

const (
	UsageDay   UsagePeriod = "day"
	UsageWeek  UsagePeriod = "week"
	UsageMonth UsagePeriod = "month"
)

// UsageQuery asks for the usage between two dates, both included, in
// periods counted in Location. Weeks start on Monday. A zero UserID covers
// every user.
type UsageQuery struct {
	UserID   int64
	Period   UsagePeriod
	From     time.Time
	To       time.Time
	Location *time.Location
}

// UsageReport is the usage of each project and model in each period.
type UsageReport struct {
	Period UsagePeriod `json:"period"`
	// Periods are the first day of every period in the range, empty or not,
	// as YYYY-MM-DD.
	Periods  []string         `json:"periods"`
	Projects []UsageProject   `json:"projects"`
	Models   []UsageModel     `json:"models"`
	Rows     []UsageReportRow `json:"rows"`
}

// UsageProject is a project the report has usage for. ID zero stands for
// requests made outside any project.
type UsageProject struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// UsageModel is a model the report has usage for.
type UsageModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UsageReportRow is the usage of one project and model in one period.
type UsageReportRow struct {
	Period    string `json:"period"`
	ProjectID int64  `json:"projectId"`
	Model     string `json:"model"`
	UsageTotals
}
