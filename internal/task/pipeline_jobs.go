package task

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Pipelines tells a task's agent the jobs its merge request's pipeline will
// run, so it can run them itself before it finishes. server.go adapts the
// pipeline service, which this package never imports.
type Pipelines interface {
	// PipelineJobs are the jobs a merge request into branch would run now:
	// those of the pipeline file at branch's tip, else of the project's
	// pipeline setting. Nil when neither has a pipeline or it doesn't parse.
	PipelineJobs(ctx context.Context, userID, projectID int64, branch string) (*PipelineJobs, error)
}

// PipelineJobs are the jobs of the pipeline a merge request would run.
type PipelineJobs struct {
	// Path is the pipeline file they come from, or "" for the project's
	// setting.
	Path string
	Jobs []PipelineJob
}

// PipelineJob is one job of a pipeline: its name, its bash script, the
// types of the project's services it gets fresh copies of, and how long it
// may run before it fails, zero when that is not known.
type PipelineJob struct {
	Name     string
	Run      string
	Services []string
	Timeout  time.Duration
}

const (
	// maxPromptJobRunBytes bounds one job's script in the implement
	// prompt, and maxPromptJobsBytes the whole section.
	maxPromptJobRunBytes = 2 << 10
	maxPromptJobsBytes   = 12 << 10
)

// SetPipelines sets what tells task agents their pipeline's jobs. Until it
// is set, their prompt names none.
func (s *service) SetPipelines(p Pipelines) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines = p
}

// pipelineJobs reads the jobs of the pipeline the task's merge request into
// target would run, or nil when there are none or they cannot be read,
// which is no reason to hold the agent back.
func (s *service) pipelineJobs(ctx context.Context, t *Task, target string) *PipelineJobs {
	s.mu.Lock()
	pipelines := s.pipelines
	s.mu.Unlock()
	if pipelines == nil {
		return nil
	}
	jobs, err := pipelines.PipelineJobs(ctx, t.UserID, t.ProjectID, target)
	if err != nil {
		log.Printf("task %d: reading the jobs of %s's pipeline for its prompt: %v", t.ID, target, err)
		return nil
	}
	return jobs
}

// pipelineJobsPrompt tells the agent the jobs its merge request into target
// will be held to, which automatic says devpad runs by itself, so that it
// runs them in its sandbox before it finishes. It is empty when there are
// none.
func pipelineJobsPrompt(target string, automatic bool, jobs *PipelineJobs) string {
	if jobs == nil || len(jobs.Jobs) == 0 {
		return ""
	}
	var b strings.Builder
	from := "the project's pipeline setting"
	if jobs.Path != "" {
		from = fmt.Sprintf("`%s` on `%s`", jobs.Path, target)
	}
	b.WriteString("## The project's pipeline\n\n")
	if automatic {
		fmt.Fprintf(&b, "When you finish, devpad runs these jobs itself, each in a fresh container, on your branch merged onto `%s`, and your work merges only once they all pass. If one fails, devpad sends you what failed. ", target)
	} else {
		fmt.Fprintf(&b, "These are the jobs of the pipeline for `%s`, which the user can run on your branch merged onto it. ", target)
	}
	fmt.Fprintf(&b, "They come from %s, not from your branch, so changing the pipeline file on your branch doesn't change them. Run each job's script in /workspace before you finish and fix what fails; a job's services are in your sandbox too, under the same variables. A job that runs longer than its timeout fails, so give the script as long to run: run_command's timeout_seconds goes up to 10 minutes, and a longer job runs with start_command and read_command_output's wait_for_exit.\n\n", from)
	used := b.Len()
	for i, j := range jobs.Jobs {
		var job strings.Builder
		fmt.Fprintf(&job, "### `%s`\n\n", j.Name)
		if len(j.Services) > 0 {
			fmt.Fprintf(&job, "Services: %s\n", strings.Join(j.Services, ", "))
		}
		if j.Timeout > 0 {
			fmt.Fprintf(&job, "Timeout: %s\n", jobTimeout(j.Timeout))
		}
		if len(j.Services) > 0 || j.Timeout > 0 {
			job.WriteString("\n")
		}
		job.WriteString(fenced("bash", clip(strings.TrimSpace(j.Run), maxPromptJobRunBytes)))
		job.WriteString("\n")
		if used+job.Len() > maxPromptJobsBytes {
			fmt.Fprintf(&b, "%d more %s left out here.", len(jobs.Jobs)-i, plural(len(jobs.Jobs)-i, "job is", "jobs are"))
			if jobs.Path != "" {
				fmt.Fprintf(&b, " Read them with `git show devpad/%s:%s`.", target, jobs.Path)
			}
			b.WriteString("\n\n")
			break
		}
		used += job.Len()
		b.WriteString(job.String())
	}
	return b.String()
}

// jobTimeout writes a job's timeout as a pipeline file would, such as
// "90s", "15m" or "1h".
func jobTimeout(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
