package httptransport

import (
	"errors"
	"log"
	"net/http"
	"time"
	// Periods are counted in the viewer's zone, which the host may have no
	// zone database for.
	_ "time/tzdata"

	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

// HandleUsageReport returns what provider requests cost, by period, project
// and model. Query parameters: period (day, week or month), from and to
// (YYYY-MM-DD, both included), tz (an IANA zone, UTC by default) and
// scope=all, which covers every user's usage and is for admins only.
func (h *Handler) HandleUsageReport(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	query := r.URL.Query()
	loc := time.UTC
	if tz := query.Get("tz"); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			writeError(w, http.StatusBadRequest, "unknown time zone")
			return
		}
	}
	from, err := time.ParseInLocation(time.DateOnly, query.Get("from"), loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be a date as YYYY-MM-DD")
		return
	}
	to, err := time.ParseInLocation(time.DateOnly, query.Get("to"), loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be a date as YYYY-MM-DD")
		return
	}
	userID := user.ID
	switch query.Get("scope") {
	case "", "mine":
	case "all":
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "only admins can see everyone's usage")
			return
		}
		userID = 0
	default:
		writeError(w, http.StatusBadRequest, "scope must be mine or all")
		return
	}

	report, err := h.usage.Report(r.Context(), domain.UsageQuery{
		UserID:   userID,
		Period:   domain.UsagePeriod(query.Get("period")),
		From:     from,
		To:       to,
		Location: loc,
	})
	switch {
	case errors.Is(err, domain.ErrInvalidUsageQuery):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		log.Printf("failed to build usage report: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load usage")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
