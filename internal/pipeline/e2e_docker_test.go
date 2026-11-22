package pipeline

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	appdb "github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/pipeline/jobrun"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/workspace"
	"github.com/devpad-org/devpad/internal/wsservice"
)

// TestPipelineRunsInDocker runs a small pipeline end to end on a real Docker
// daemon, which must already have devpad's workspace image: a real repo
// container pins and bundles the commit, and real job containers, one with a
// Postgres of its own, clone and run it. It runs only when
// DEVPAD_DOCKER_TEST is set.
//
// It shares the daemon with whatever else uses it, a devpad of the user's
// included, so it never builds or tags an image, and everything it makes is
// named apart: its project's id, which names the repo container, network
// and volume, is far above any a real devpad reaches, and pipelines' names
// are random. Deleting the project removes the repo container; the test
// checks that the pipeline left nothing behind itself.
func TestPipelineRunsInDocker(t *testing.T) {
	if os.Getenv("DEVPAD_DOCKER_TEST") == "" {
		t.Skip("set DEVPAD_DOCKER_TEST to run against the local Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker, err := container.NewManager()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	if _, err := docker.ImageID(ctx, container.WorkspaceImage); err != nil {
		t.Skipf("no workspace image: %v", err)
	}
	useAgentFromThisTree(t)

	db, err := appdb.Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	offsetIDs(t, db, "users", "projects", "workspaces", "pipelines")
	conn := db.Conn()
	res, err := conn.Exec(`INSERT INTO users (username, email, password) VALUES ('pipeline-e2e', 'e2e@example.com', 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()
	key := make([]byte, 32)
	rand.Read(key)
	cipher, err := encrypt.NewCipher(hex.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	users := auth.NewUserRepository(conn)
	workspaces := workspace.NewService(workspace.NewRepository(conn), docker, users, cipher)
	services := wsservice.NewService(wsservice.NewRepository(conn), docker)
	projects := project.NewService(project.NewRepository(conn), docker, workspaces, users, cipher, nil)
	projects.SetSidecarServices(services)

	p, err := projects.Create(ctx, userID, project.CreateInput{Name: "pipeline-e2e", DefaultBranch: "main"})
	if err != nil {
		t.Fatalf("creating the project: %v", err)
	}
	t.Cleanup(func() {
		if err := projects.Delete(context.Background(), userID, p.ID); err != nil {
			t.Errorf("deleting the project: %v", err)
		}
	})
	if p.Status != project.StatusReady {
		t.Fatalf("project not ready: %s %s", p.Status, p.Error)
	}
	if _, err := services.AddProjectService(ctx, p.ID, wsservice.ServicePostgres, wsservice.CreateOptions{UserID: userID}); err != nil {
		t.Fatalf("adding postgres to the project: %v", err)
	}
	// The project's setting, since the repo's only commit has no file.
	config := "jobs:\n" +
		"  check:\n    run: |\n      id -un\n      pwd\n      git log --format=%s\n      echo \"HEAD=$(git rev-parse HEAD)\"\n      test -z \"$(git status --porcelain)\"\n" +
		"  db:\n    services: [postgres]\n    run: exec 3<>\"/dev/tcp/$POSTGRES_HOST/$POSTGRES_PORT\" && echo \"reached $POSTGRES_HOST\"\n" +
		"  fails:\n    run: |\n      echo about to fail\n      exit 3\n" +
		"  slow:\n    run: sleep 60\n    timeout: 2s\n"
	if _, err := projects.SetPipelineConfig(ctx, userID, p.ID, config); err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	svc := NewService(Deps{
		Repo:          NewRepository(conn),
		Projects:      projects,
		MergeRequests: &fakeMergeRequests{mrs: map[int64]*MergeRequest{}},
		Jobs:          jobrun.New(docker, services, jobrun.Config{}),
		Docker:        docker,
		BundleDir:     tempDir,
	}).(*service)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		svc.Shutdown(shutdownCtx)
	})

	started, created, err := svc.Start(ctx, StartInput{UserID: userID, ProjectID: p.ID, Branch: "main", Trigger: TriggerUser})
	if err != nil || !created {
		t.Fatalf("start: %v %v", created, err)
	}
	var got *Pipeline
	for {
		if got, err = svc.repo.Get(ctx, started.ID); err != nil {
			t.Fatal(err)
		}
		if got.Status.Finished() {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("pipeline still %s", got.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitInactive(t, svc, got.ID)

	output := func(name string) string {
		out, err := svc.JobOutput(ctx, userID, p.ID, got.ID, job(t, got, name).ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		return string(out.Data)
	}
	if got.Status != StatusFailed || got.TestedSHA != got.HeadSHA || got.Image != container.WorkspaceImage {
		t.Fatalf("pipeline: %+v", got)
	}
	check := job(t, got, "check")
	if out := output("check"); check.Status != JobPassed || !strings.Contains(out, "devpad\n/workspace\nInitial commit\nHEAD="+got.TestedSHA+"\n") {
		t.Errorf("check: %+v\n%s", check, out)
	}
	if db := job(t, got, "db"); db.Status != JobPassed || !strings.Contains(output("db"), "reached postgres\n") {
		t.Errorf("db: %+v\n%s", db, output("db"))
	}
	if fails := job(t, got, "fails"); fails.Status != JobFailed || fails.ExitCode == nil || *fails.ExitCode != 3 || !strings.Contains(output("fails"), "about to fail\n") {
		t.Errorf("fails: %+v\n%s", fails, output("fails"))
	}
	if slow := job(t, got, "slow"); slow.Status != JobTimedOut {
		t.Errorf("slow: %+v\n%s", slow, output("slow"))
	}

	// Nothing of the pipeline is left: not its jobs' containers, services
	// or networks, nor its bundle, nor its ref in the canonical repo.
	if left, err := docker.ListJobResources(ctx, container.Job{Pipeline: got.UID}); err != nil || len(left) != 0 {
		t.Errorf("left in Docker: %+v %v", left, err)
	}
	if entries, _ := os.ReadDir(tempDir); len(entries) != 0 {
		t.Errorf("left a bundle: %v", entries)
	}
	if _, err := projects.PipelineBundle(ctx, userID, p.ID, got.UID, &bytes.Buffer{}); err == nil {
		t.Errorf("the pipeline's ref is still pinned")
	}
	if got.RefPinned {
		t.Errorf("the pipeline is recorded as pinned")
	}
}

// useAgentFromThisTree builds the workspace agent from this tree for the
// repo container, which devpad updates to the agent it embeds: the one the
// image has, and the one embedded at the last build, may both predate the
// pipeline endpoints.
func useAgentFromThisTree(t *testing.T) {
	t.Helper()
	version := "pipeline-e2e-" + randomSuffix()
	bin := filepath.Join(t.TempDir(), "devpad-agent")
	cmd := exec.Command("go", "build", "-ldflags", "-X main.Version="+version, "-o", bin, "./cmd/agent")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the agent: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	oldBinary, oldVersion := agentbin.Binary, agentbin.Version
	agentbin.Binary, agentbin.Version = binary, version
	t.Cleanup(func() { agentbin.Binary, agentbin.Version = oldBinary, oldVersion })
}

// offsetIDs starts the ids of tables far above any a real devpad reaches,
// since they name Docker resources.
func offsetIDs(t *testing.T, db *appdb.DB, tables ...string) {
	t.Helper()
	n, _ := rand.Int(rand.Reader, big.NewInt(20000))
	offset := 970000 + n.Int64()
	for _, table := range tables {
		res, err := db.Conn().Exec(`UPDATE sqlite_sequence SET seq = ? WHERE name = ?`, offset, table)
		if err != nil {
			t.Fatal(err)
		}
		if updated, _ := res.RowsAffected(); updated == 0 {
			if _, err := db.Conn().Exec(`INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)`, table, offset); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func randomSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func waitInactive(t *testing.T, svc *service, id int64) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		svc.mu.Lock()
		_, active := svc.active[id]
		svc.mu.Unlock()
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline %d is still active", id)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
