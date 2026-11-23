package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/auth"
)

// serve sends a request to the pipeline routes as user, and decodes the
// answer.
func (e *env) serve(user int64, method, target, body string) (int, map[string]any) {
	e.t.Helper()
	h := NewHandler(nil, e.svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/pipelines", h.HandleList)
	mux.HandleFunc("POST /api/projects/{id}/pipelines", h.HandleStart)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/latest", h.HandleLatest)
	mux.HandleFunc("GET /api/projects/{id}/merge-requests/{mrId}/pipeline", h.HandleMergeRequestStatus)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}", h.HandleGet)
	mux.HandleFunc("POST /api/projects/{id}/pipelines/{pipelineId}/cancel", h.HandleCancel)
	mux.HandleFunc("GET /api/projects/{id}/pipelines/{pipelineId}/jobs/{jobId}/output", h.HandleJobOutput)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: user}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("%s %s: %d %q is not JSON", method, target, rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

func TestPipelineEndpoints(t *testing.T) {
	e := setup(t)
	e.projects.setFile(sha(2), ".devpad/pipeline.yml", "jobs:\n  talk:\n    run: block\n    timeout: 15m\n    services: [postgres]\n")
	base := fmt.Sprintf("/api/projects/%d/pipelines", e.project)

	code, out := e.serve(e.owner, http.MethodPost, base, `{"branch":"develop"}`)
	if code != http.StatusAccepted {
		t.Fatalf("start: %d %v", code, out)
	}
	p := out["pipeline"].(map[string]any)
	if p["status"] != "queued" || p["kind"] != "branch" || p["branch"] != "develop" || p["headSha"] != sha(2) || p["configSource"] != "file" || p["trigger"] != "user" {
		t.Fatalf("started: %v", p)
	}
	jobs := p["jobs"].([]any)
	j := jobs[0].(map[string]any)
	if j["name"] != "talk" || j["run"] != "block" || j["timeoutMs"] != float64(15*60*1000) || fmt.Sprint(j["services"]) != "[postgres]" || j["exitCode"] != nil {
		t.Fatalf("job: %v", j)
	}
	id := int64(p["id"].(float64))
	jobID := int64(j["id"].(float64))
	e.awaitScript("talk")

	// The same again is the pipeline already running.
	if code, out := e.serve(e.owner, http.MethodPost, base, `{"branch":"develop"}`); code != http.StatusOK || int64(out["pipeline"].(map[string]any)["id"].(float64)) != id {
		t.Fatalf("again: %d %v", code, out)
	}
	code, out = e.serve(e.owner, http.MethodGet, fmt.Sprintf("%s/%d/jobs/%d/output?offset=0", base, id, jobID), "")
	notes, _ := out["notes"].([]any)
	if code != http.StatusOK || out["done"] != false || out["data"] != "" || out["next"] != float64(0) || len(notes) == 0 {
		t.Fatalf("live output: %d %v", code, out)
	}
	if last := notes[len(notes)-1].(map[string]any); last["text"] != "running the job" || last["at"] == "" {
		t.Fatalf("live notes: %v", notes)
	}

	code, out = e.serve(e.owner, http.MethodPost, fmt.Sprintf("%s/%d/cancel", base, id), "")
	if code != http.StatusOK || out["pipeline"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", code, out)
	}
	if code, out := e.serve(e.owner, http.MethodPost, fmt.Sprintf("%s/%d/cancel", base, id), ""); code != http.StatusConflict {
		t.Fatalf("cancel again: %d %v", code, out)
	}
	code, out = e.serve(e.owner, http.MethodGet, fmt.Sprintf("%s/%d", base, id), "")
	got := out["pipeline"].(map[string]any)
	if code != http.StatusOK || got["status"] != "cancelled" || got["jobs"].([]any)[0].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("get: %d %v", code, out)
	}

	code, out = e.serve(e.owner, http.MethodGet, base+"?branch=develop&limit=5", "")
	list := out["pipelines"].([]any)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	if _, has := list[0].(map[string]any)["jobs"].([]any)[0].(map[string]any)["run"]; has {
		t.Fatalf("the list carries scripts: %v", list[0])
	}

	code, out = e.serve(e.owner, http.MethodGet, fmt.Sprintf("%s/latest?branch=main&branch=develop&mergeRequestId=%d", base, e.mr), "")
	// The only pipeline was cancelled, which says nothing of the commits,
	// so no branch has one that counts; each branch's commit comes along.
	tips, _ := out["branchTips"].(map[string]any)
	if latest := out["pipelines"].([]any); code != http.StatusOK || len(latest) != 0 || tips["develop"] != sha(2) || tips["main"] != sha(1) || len(tips) != 2 {
		t.Fatalf("latest: %d %v", code, out)
	}
	// develop's commit has its pipeline cancelled, which the badge says;
	// neither branch nor the merge request runs by itself.
	branches, _ := out["branches"].(map[string]any)
	develop, _ := branches["develop"].(map[string]any)
	settled, _ := develop["settled"].(map[string]any)
	if develop["automatic"] != false || settled == nil || settled["retrying"] != false || !strings.Contains(fmt.Sprint(settled["reason"]), "was cancelled") ||
		settled["pipeline"].(map[string]any)["id"] != float64(id) || branches["main"].(map[string]any)["settled"] != nil {
		t.Fatalf("latest branches: %v", branches)
	}

	code, out = e.serve(e.owner, http.MethodGet, fmt.Sprintf("/api/projects/%d/merge-requests/%d/pipeline", e.project, e.mr), "")
	if st, _ := out["status"].(map[string]any); code != http.StatusOK || st["open"] != true || st["configured"] != true || st["passed"] != false || st["current"] != nil || st["targetSha"] != sha(2) || st["source"] != "file" ||
		st["automatic"] != false || st["settled"] != nil {
		t.Fatalf("merge request status: %d %v", code, out)
	}
	// One that can no longer merge has no pipeline to run.
	e.mrs.mu.Lock()
	e.mrs.mrs[e.mr].Open = false
	e.mrs.mu.Unlock()
	code, out = e.serve(e.owner, http.MethodGet, fmt.Sprintf("/api/projects/%d/merge-requests/%d/pipeline", e.project, e.mr), "")
	if st, _ := out["status"].(map[string]any); code != http.StatusOK || st["open"] != false || st["source"] != "none" || st["configured"] != false {
		t.Fatalf("a closed merge request's status: %d %v", code, out)
	}
	e.mrs.mu.Lock()
	e.mrs.mrs[e.mr].Open = true
	e.mrs.mu.Unlock()

	for _, tc := range []struct {
		name   string
		user   int64
		method string
		target string
		body   string
		want   int
	}{
		{"a stranger's list", e.stranger, http.MethodGet, base, "", http.StatusForbidden},
		{"a stranger's start", e.stranger, http.MethodPost, base, `{"branch":"develop"}`, http.StatusForbidden},
		{"a stranger's output", e.stranger, http.MethodGet, fmt.Sprintf("%s/%d/jobs/%d/output", base, id, jobID), "", http.StatusForbidden},
		{"an unprotected branch", e.owner, http.MethodPost, base, `{"branch":"feature"}`, http.StatusBadRequest},
		{"a missing merge request", e.owner, http.MethodPost, base, `{"mergeRequestId":999}`, http.StatusNotFound},
		{"a bad body", e.owner, http.MethodPost, base, `branch`, http.StatusBadRequest},
		{"a stranger's latest", e.stranger, http.MethodGet, base + "/latest?branch=develop", "", http.StatusForbidden},
		{"a stranger's merge request status", e.stranger, http.MethodGet, fmt.Sprintf("/api/projects/%d/merge-requests/%d/pipeline", e.project, e.mr), "", http.StatusForbidden},
		{"a missing merge request's status", e.owner, http.MethodGet, fmt.Sprintf("/api/projects/%d/merge-requests/999/pipeline", e.project), "", http.StatusNotFound},
		{"a bad merge request id", e.owner, http.MethodGet, base + "/latest?mergeRequestId=x", "", http.StatusBadRequest},
		{"too many at once", e.owner, http.MethodGet, base + "/latest?" + strings.Repeat("branch=b&", MaxLatestSubjects+1), "", http.StatusBadRequest},
		{"a bad limit", e.owner, http.MethodGet, base + "?limit=1000", "", http.StatusBadRequest},
		{"a bad offset", e.owner, http.MethodGet, fmt.Sprintf("%s/%d/jobs/%d/output?offset=-1", base, id, jobID), "", http.StatusBadRequest},
		{"a missing pipeline", e.owner, http.MethodGet, fmt.Sprintf("%s/%d", base, id+50), "", http.StatusNotFound},
		{"a missing job", e.owner, http.MethodGet, fmt.Sprintf("%s/%d/jobs/%d/output", base, id, jobID+50), "", http.StatusNotFound},
		{"a missing project", e.owner, http.MethodGet, "/api/projects/999/pipelines", "", http.StatusNotFound},
	} {
		if code, out := e.serve(tc.user, tc.method, tc.target, tc.body); code != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.name, code, out, tc.want)
		}
	}

	// A project being deleted starts none, and nothing starts while devpad
	// stops.
	e.svc.ProjectDeleting(context.Background(), e.project)
	if code, out := e.serve(e.owner, http.MethodPost, base, `{"branch":"develop"}`); code != http.StatusConflict {
		t.Errorf("start while the project is deleted: %d %v", code, out)
	}
	e.svc.ProjectDeleteEnded(e.project)
	e.shutdown()
	if code, out := e.serve(e.owner, http.MethodPost, base, `{"branch":"develop"}`); code != http.StatusServiceUnavailable {
		t.Errorf("start while devpad stops: %d %v", code, out)
	}
}
